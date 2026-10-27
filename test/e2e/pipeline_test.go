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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Promotion pipelines", Label("promotion"), func() {
	Context("the promotion-pipeline example: dev, then staging, then prod after a 60s soak", Ordered, func() {
		var ns string
		var dev, staging, prod *deliveryv1alpha1.Preparation

		BeforeAll(func() {
			ns = framework.CreateNamespace("pipeline")
			framework.ApplyExample(ns, "promotion-pipeline")
		})

		It("renders a Preparation of the same Product revision per stage, each running the same pinned image", func() {
			dev = framework.LatestPreparation(ns, "podinfo-dev")
			staging = framework.LatestPreparation(ns, "podinfo-staging")
			prod = framework.LatestPreparation(ns, "podinfo-prod")
			Expect(staging.Spec.ProductRevision).To(Equal(dev.Spec.ProductRevision))
			Expect(prod.Spec.ProductRevision).To(Equal(dev.Spec.ProductRevision))

			Expect(dev.Spec.Images).To(HaveExactElements(HaveField("Ref", "ghcr.io/stefanprodan/podinfo:6.14.1")))
			image := dev.Spec.Images[0].Ref + "@" + dev.Spec.Images[0].Digest
			deployment := framework.RenderedObject(framework.PullPreparation(dev), "Deployment", "podinfo-dev")
			Expect(framework.Field(deployment, "spec", "template", "spec", "containers")).To(
				ContainElement(HaveKeyWithValue("image", image)), "the manifests run the image by the digest its tag resolved to")
		})

		It("deploys dev right away", func() {
			framework.WaitForDeployment(ns, "podinfo-dev", dev.Spec.Artifact.Digest)
		})

		It("holds staging until dev runs the change healthily", func() {
			Eventually(framework.Serving(ns, "podinfo-staging")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonNotDeployedUpstream))
			framework.ExpectNoApplication(ns, "podinfo-staging")

			framework.SyncHealthy(ns, "podinfo-dev", dev.Spec.Artifact.Digest)
			framework.WaitForDeployment(ns, "podinfo-staging", staging.Spec.Artifact.Digest)
		})

		It("queues a promotion to prod until staging ran the change healthily for 60s", func() {
			framework.Promote(ns, "podinfo-prod", prod.Name)
			Eventually(framework.Serving(ns, "podinfo-prod")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonNotDeployedUpstream))

			framework.SyncHealthy(ns, "podinfo-staging", staging.Spec.Artifact.Digest)
			Eventually(framework.Serving(ns, "podinfo-prod")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonSoaking))
			framework.ExpectNoApplication(ns, "podinfo-prod")
		})

		It("deploys prod once the soak ends", func() {
			framework.WaitForDeployment(ns, "podinfo-prod", prod.Spec.Artifact.Digest)
			Expect(framework.Serving(ns, "podinfo-prod")()).To(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeUpstreamDeployed, deliveryv1alpha1.ReasonSatisfied))
		})

		It("records each healthy deployment in the Serving's history", func() {
			framework.SyncHealthy(ns, "podinfo-prod", prod.Spec.Artifact.Digest)
			Eventually(framework.Serving(ns, "podinfo-prod"), time.Minute).Should(
				HaveField("Status.History", HaveExactElements(SatisfyAll(
					HaveField("PreparationName", prod.Name),
					HaveField("PromotionRecord", HavePrefix("sha256:")),
					HaveField("DeploymentRecord", HavePrefix("sha256:")),
				))))
		})

		It("signs promotion and deployment records that stock cosign verifies", func() {
			key := framework.KitchenPublicKey()
			for _, record := range []struct{ predicateType, ref string }{
				{"https://kokumi.dev/attestations/promotion/v1", framework.PreparationRef(prod)},
				{"https://kokumi.dev/attestations/deployment/v1", framework.PreparationRef(prod)},
				{"https://kokumi.dev/attestations/deployment/v1", framework.PreparationRef(staging)},
			} {
				Expect(framework.CosignVerifyAttestation(key, record.ref, record.predicateType)).To(Succeed(),
					"stock cosign verifies the %s record of %s", record.predicateType, record.ref)
			}
		})
	})
})
