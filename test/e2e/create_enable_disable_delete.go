// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package e2e_test

import (
	"context"
	"time"

	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"
	e2e "github.com/gardener/gardener/test/e2e/gardener"
	"github.com/gardener/gardener/test/framework"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-shoot-rsyslog-relp/pkg/apis/rsyslog/v1alpha1"
	"github.com/gardener/gardener-extension-shoot-rsyslog-relp/test/common"
)

var _ = Describe("Shoot Rsyslog Relp Extension Tests", func() {
	test := func(f *framework.ShootCreationFramework, shootMutateFn func(shoot *gardencorev1beta1.Shoot) error, additionalLogEntries ...common.LogEntry) {
		It("Create Shoot, enable shoot-rsyslog-relp extension then disable it and delete Shoot", Offset(1), func() {
			ctx, cancel := context.WithTimeout(parentCtx, 20*time.Minute)
			DeferCleanup(cancel)

			By("Create Shoot")
			Expect(f.CreateShootAndWaitForCreation(ctx, false)).To(Succeed())
			f.Verify()

			ctx, cancel = context.WithTimeout(parentCtx, 1*time.Minute)
			DeferCleanup(cancel)

			By("Create NetworkPolicy to allow traffic from Shoot nodes to the rsyslog-relp echo server")
			Expect(createNetworkPolicyForEchoServer(ctx, f.ShootFramework.SeedClient, f.ShootFramework.ShootSeedNamespace())).To(Succeed())

			By("Install rsyslog-relp unit on Shoot nodes")
			common.ForEachNode(ctx, f.ShootFramework.ShootClient, func(ctx context.Context, node *corev1.Node) {
				installRsyslogRelp(ctx, f.Logger, f.ShootFramework.ShootClient, node.Name)
			})

			By("Enable the shoot-rsyslog-relp extension")
			ctx, cancel = context.WithTimeout(parentCtx, 15*time.Minute)
			DeferCleanup(cancel)
			Expect(f.UpdateShoot(ctx, f.Shoot, shootMutateFn)).To(Succeed())

			By("Verify shoot-rsyslog-relp works")
			ctx, cancel = context.WithTimeout(parentCtx, 5*time.Minute)
			DeferCleanup(cancel)

			verifier := common.NewVerifier(f.Logger, f.ShootFramework.ShootClient, f.ShootFramework.SeedClient, f.Shoot.Spec.Provider.Type, f.ShootFramework.Project.Name, f.Shoot.Name, string(f.Shoot.UID), false, "")

			common.ForEachNode(ctx, f.ShootFramework.ShootClient, func(ctx context.Context, node *corev1.Node) {
				verifier.VerifyExtensionForNode(ctx, node.Name, additionalLogEntries...)
			})

			By("Disable the shoot-rsyslog-relp extension")
			ctx, cancel = context.WithTimeout(parentCtx, 10*time.Minute)
			DeferCleanup(cancel)
			Expect(f.UpdateShoot(ctx, f.Shoot, func(shoot *gardencorev1beta1.Shoot) error {
				common.RemoveRsyslogRelpExtension(shoot)
				return nil
			})).To(Succeed())

			By("Verify that shoot-rsyslog-relp extension is disabled")
			ctx, cancel = context.WithTimeout(parentCtx, 5*time.Minute)
			DeferCleanup(cancel)

			common.ForEachNode(ctx, f.ShootFramework.ShootClient, func(ctx context.Context, node *corev1.Node) {
				verifier.VerifyExtensionDisabledForNode(ctx, node.Name)
			})

			By("Delete Shoot")
			ctx, cancel = context.WithTimeout(parentCtx, 15*time.Minute)
			DeferCleanup(cancel)
			Expect(f.DeleteShootAndWaitForDeletion(ctx, f.Shoot)).To(Succeed())
		})
	}

	Context("shoot-rsyslog-relp extension with tls disabled", Label("tls-disabled"), func() {
		f := defaultShootCreationFramework()
		f.Shoot = e2e.DefaultShoot("e2e-rslog-relp")

		enableExtensionFunc := func(shoot *gardencorev1beta1.Shoot) error {
			common.AddOrUpdateRsyslogRelpExtension(shoot, common.WithAuditConfig(&v1alpha1.AuditConfig{Enabled: false}))
			return nil
		}

		test(f, enableExtensionFunc)
	})

	Context("shoot-rsyslog-relp extension with tls and openssl enabled", Label("tls-enabled"), func() {
		var createdResources []client.Object
		f := defaultShootCreationFramework()
		f.Shoot = e2e.DefaultShoot("e2e-rslog-tls")

		enableExtensionFunc := func(shoot *gardencorev1beta1.Shoot) error {
			common.AddOrUpdateRsyslogRelpExtension(shoot, common.WithPort(443), common.WithTLSWithSecretRefNameAndTLSLib("rsyslog-relp-tls", "openssl"), common.WithAuditConfig(&v1alpha1.AuditConfig{Enabled: false}))
			common.AddOrUpdateResourceReference(shoot, "rsyslog-relp-tls", "Secret", createdResources[0].GetName())
			return nil
		}

		BeforeEach(func() {
			ctx, cancel := context.WithTimeout(parentCtx, 1*time.Minute)
			DeferCleanup(cancel)

			By("Create rsyslog-relp tls Secret")
			var err error
			createdResources, err = common.CreateResourcesFromFile(ctx, f.GardenClient.Client(), f.ProjectNamespace, "../common/testdata/tls")
			Expect(err).NotTo(HaveOccurred())
			Expect(createdResources).ToNot(BeEmpty())
		})

		AfterEach(func() {
			ctx, cancel := context.WithTimeout(parentCtx, 1*time.Minute)
			DeferCleanup(cancel)

			for _, resource := range createdResources {
				Expect(f.GardenClient.Client().Delete(ctx, resource)).To(Or(Succeed(), BeNotFoundError()))
			}
		})

		test(f, enableExtensionFunc)
	})

	Context("shoot-rsyslog-relp extension with filtering messages by regexes", Label("messageContent-filtering"), func() {
		f := defaultShootCreationFramework()
		f.Shoot = e2e.DefaultShoot("e2e-rslog-filter")

		additionalLogEntries := []common.LogEntry{
			{Program: "filter-program", Severity: "3", Message: "this included log should get sent to echo server", ShouldBeForwarded: true},
			{Program: "filter-program", Severity: "3", Message: "this excluded log should not get sent to echo server", ShouldBeForwarded: false},
		}
		enableExtensionFunc := func(shoot *gardencorev1beta1.Shoot) error {
			loggingRule := v1alpha1.LoggingRule{
				ProgramNames: []string{"filter-program"},
				Severity:     ptr.To(3),
				MessageContent: &v1alpha1.MessageContent{
					Regex:   ptr.To("included"),
					Exclude: ptr.To("excluded"),
				},
			}

			common.AddOrUpdateRsyslogRelpExtension(shoot, common.AppendLoggingRule(loggingRule), common.WithAuditConfig(&v1alpha1.AuditConfig{Enabled: false}))
			return nil
		}

		test(f, enableExtensionFunc, additionalLogEntries...)
	})
})
