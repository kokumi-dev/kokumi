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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Source verification", Label("source-verification"), func() {
	Context("a chart signed keyless by its release workflow: the source-verification example", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("keyless")
			framework.ApplyExample(ns, "source-verification")
		})

		It("verifies the signature against the identity of the release workflow, and renders the Order", func() {
			Eventually(framework.Product(ns, "podinfo-verified"), framework.RenderTimeout).Should(SatisfyAll(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeSourceVerified, deliveryv1alpha1.ReasonSucceeded),
				framework.BeReady(),
			))
			framework.LatestPreparation(ns, "podinfo-verified")
		})

		It("refuses the source once the Product expects another signer", func() {
			framework.Update(ns, "podinfo-verified", func(p *deliveryv1alpha1.Product) {
				p.Spec.Source.Verify.MatchOIDCIdentity[0].Subject = `^https://github\.com/stefanprodan/other/.*$`
			})
			Eventually(framework.Product(ns, "podinfo-verified"), framework.RenderTimeout).Should(SatisfyAll(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeSourceVerified, metav1.ConditionFalse),
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeReady, metav1.ConditionFalse),
			))
		})
	})

	Context("a Product that requires a signature by a key, of a source nobody signed yet", Ordered, func() {
		var ns, repository, digest string

		BeforeAll(func() {
			ns = framework.CreateNamespace("key")
			repository = "e2e/" + ns + "/source"

			By("pushing an unsigned source to the bundled registry")
			digest = framework.PushSource(repository, "sources/configmap.yaml", nil)

			By("trusting the testdata key, and requiring its signature on the source")
			_, err := framework.Kubectl("create", "secret", "generic", "testdata-signing-key", "-n", ns,
				"--from-file=cosign.pub="+framework.TestdataKey("cosign.pub"))
			Expect(err).NotTo(HaveOccurred())
			framework.ApplyTestdata(ns, "source-verification/key-verified-product.yaml", nil)
		})

		It("reports the source unverified, and is not Ready", func() {
			Eventually(framework.Product(ns, "key-verified")).Should(SatisfyAll(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeSourceVerified, metav1.ConditionFalse),
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeReady, metav1.ConditionFalse),
			))
		})

		It("verifies the source, and becomes Ready, once the publisher signs it with the key", func() {
			framework.CosignSign(framework.TestdataKey("cosign.key"), framework.RegistryHost()+"/"+repository+"@"+digest)

			By("reconciling the Product without waiting for its retry interval")
			framework.Requeue("products.delivery.kokumi.dev", ns, "key-verified")
			Eventually(framework.Product(ns, "key-verified")).Should(SatisfyAll(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeSourceVerified, metav1.ConditionTrue),
				framework.BeReady(),
			))
		})
	})

	Context("an installation that requires verified sources and trusts the testdata key", Serial, Ordered, func() {
		var ns string

		BeforeAll(func() {
			By("requiring verified sources, and trusting the testdata key, on the Kitchen")
			framework.PatchKitchen("test/e2e/testdata/source-verification/kitchen-require-verification.yaml")
			ns = framework.CreateNamespace("required")
		})

		It("refuses a source that no trusted key signed, although its Product asks for no verification", func() {
			framework.ApplyExample(ns, "manifest-bundle")
			Eventually(framework.Product(ns, "external-secrets")).Should(SatisfyAll(
				framework.HaveConditionReason(
					deliveryv1alpha1.ConditionTypeSourceVerified, deliveryv1alpha1.ReasonVerificationRequired),
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeReady, metav1.ConditionFalse),
			))
		})

		It("renders a source signed by a trusted key, without configuration on the Product", func() {
			repository := "e2e/" + ns + "/signed"
			digest := framework.PushSource(repository, "sources/configmap.yaml", nil)
			framework.CosignSign(framework.TestdataKey("cosign.key"), framework.RegistryHost()+"/"+repository+"@"+digest)

			framework.ApplyTestdata(ns, "source-verification/unconfigured-product.yaml", nil)
			Eventually(framework.Product(ns, "unconfigured")).Should(SatisfyAll(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeSourceVerified, metav1.ConditionTrue),
				framework.BeReady(),
			))
		})
	})
})
