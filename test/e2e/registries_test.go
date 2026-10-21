//go:build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

// privateRegistry is the registry fixture of registries/private-registry.yaml,
// which requires the user e2e with the password e2e-password.
const privateRegistry = "private-registry.e2e-private-registry.svc.cluster.local:5000"

var _ = Describe("Registries", Label("registries"), func() {
	Context("the private-registry example, pointed at a registry that requires a password", Ordered, func() {
		var ns string

		BeforeAll(func() {
			By("running a registry that requires a password, with the podinfo chart in it")
			framework.ApplyTestdata("", "registries/private-registry.yaml", nil)
			DeferCleanup(func() {
				_, _ = framework.Kubectl("delete", "namespace", "e2e-private-registry", "--ignore-not-found")
			})
			_, err := framework.Kubectl("rollout", "status", "deployment/private-registry",
				"-n", "e2e-private-registry", "--timeout=3m")
			Expect(err).NotTo(HaveOccurred())
			host := framework.ForwardRegistryService("e2e-private-registry", "private-registry")
			framework.CopyToRegistry("ghcr.io/stefanprodan/charts/podinfo:6.14.1", host+"/charts/podinfo:6.14.1",
				"e2e", "e2e-password")

			ns = framework.CreateNamespace("private-registry")
			framework.ApplyExampleOverlay(ns, "private-registry")
		})

		It("connects to the registry with the credentials of the Secret", func() {
			Eventually(framework.Object[deliveryv1alpha1.Registry](ns, "my-org")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRegistryConnected))
		})

		It("adds the chart's repository to the catalog", func() {
			Eventually(framework.Object[deliveryv1alpha1.Repository](ns, "podinfo-chart")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonRepositoryListed))
		})

		It("pulls the source through the Registry, says so, and renders the Order into the registry", func() {
			Eventually(framework.Product(ns, "podinfo-private"), framework.RenderTimeout).Should(SatisfyAll(
				framework.BeReady(),
				HaveField("Status.RegistryAccess", ContainElement(SatisfyAll(
					HaveField("URL", "oci://"+privateRegistry+"/charts/podinfo"),
					HaveField("Operation", deliveryv1alpha1.RegistryOperationPull),
					HaveField("Registry", "my-org"),
				))),
			))
			framework.LatestPreparation(ns, "podinfo-private")
		})

		It("refuses to push the render of the private source to the bundled registry, which anyone may read", func() {
			framework.Update(ns, "podinfo-private", func(o *deliveryv1alpha1.Order) {
				o.Spec.Destination = nil
			})
			Eventually(framework.Order(ns, "podinfo-private")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonCredentialsRequired))
		})

		It("reports the Registry Unauthorized once its credentials are wrong", func() {
			framework.ApplyTestdata(ns, "registries/wrong-credentials.yaml", nil)
			Eventually(framework.Object[deliveryv1alpha1.Registry](ns, "my-org")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonUnauthorized))
		})
	})

	Context("who may name a Secret in a Registry", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("registries")
		})

		It("refuses the editor, who may not read Secrets, a Registry that names one", func() {
			Eventually(func(g Gomega) {
				out, err := framework.KubectlAs("kokumi-editor", "apply", "-n", ns,
					"-f", framework.TestdataFile("registries/registry-with-secret.yaml", nil))
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error() + out).To(ContainSubstring("A Registry may only name a Secret its author may get"))
			}).Should(Succeed())
		})

		It("lets the editor create a Registry without credentials", func() {
			_, err := framework.KubectlAs("kokumi-editor", "apply", "-n", ns,
				"-f", framework.TestdataFile("registries/registry-without-secret.yaml", nil))
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
