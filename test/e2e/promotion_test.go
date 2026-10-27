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
	"github.com/onsi/gomega/types"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Promotion", Label("promotion"), func() {
	Context("an Order with Manual promotion: the getting-started example", Ordered, func() {
		var ns string
		var first, second *deliveryv1alpha1.Preparation

		BeforeAll(func() {
			ns = framework.CreateNamespace("promotion")
			framework.ApplyExample(ns, "getting-started")
			first = framework.LatestPreparation(ns, "podinfo")
		})

		It("deploys nothing until a Preparation is promoted", func() {
			_, err := framework.Serving(ns, "podinfo")()
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the Order has no Serving before its first promotion")
			framework.ExpectNoApplication(ns, "podinfo")
		})

		It("deploys a promoted Preparation through an Argo CD Application of its digest", func() {
			framework.Promote(ns, "podinfo", first.Name)
			framework.WaitForDeployment(ns, "podinfo", first.Spec.Artifact.Digest)

			app, err := framework.Application(ns, "podinfo")()
			Expect(err).NotTo(HaveOccurred())
			Expect(app.Object["spec"]).To(SatisfyAll(
				HaveKeyWithValue("source", HaveKeyWithValue("repoURL",
					framework.Ref(first.Spec.Artifact.OCIRef).OCIRepositoryReference())),
				HaveKeyWithValue("destination", HaveKeyWithValue("namespace", ns)),
			), "Argo CD pulls the Preparation from the registry and applies it to the Order's namespace")

			framework.SyncHealthy(ns, "podinfo", first.Spec.Artifact.Digest)
			Eventually(framework.Serving(ns, "podinfo")).Should(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed))
		})

		It("deploys a newer Preparation once it is promoted, and records the one it replaces as superseded", func() {
			framework.Update(ns, "podinfo", func(o *deliveryv1alpha1.Order) {
				o.Spec.Values = framework.Values(`{"ui":{"message":"Hello again"}}`)
			})
			second = framework.LatestPreparation(ns, "podinfo", first.Name)

			framework.Promote(ns, "podinfo", second.Name)
			framework.WaitForDeployment(ns, "podinfo", second.Spec.Artifact.Digest)
			framework.SyncHealthy(ns, "podinfo", second.Spec.Artifact.Digest)

			Eventually(framework.Serving(ns, "podinfo")).Should(HaveField("Status.History", HaveExactElements(
				healthy(second),
				superseded(first),
			)))
		})

		It("rolls back by promoting the earlier Preparation again, without rendering it again", func() {
			framework.Promote(ns, "podinfo", first.Name)
			framework.WaitForDeployment(ns, "podinfo", first.Spec.Artifact.Digest)
			framework.SyncHealthy(ns, "podinfo", first.Spec.Artifact.Digest)

			Eventually(framework.Serving(ns, "podinfo")).Should(HaveField("Status.History", HaveExactElements(
				healthy(first),
				superseded(second),
				superseded(first),
			)))
			Expect(framework.Preparations(ns, "podinfo")()).To(HaveLen(2), "a rollback moves a pointer and renders nothing")
		})
	})
})

// healthy matches the history entry of a deployment of preparation that Argo
// CD reported healthy.
func healthy(preparation *deliveryv1alpha1.Preparation) types.GomegaMatcher {
	return SatisfyAll(HaveField("PreparationName", preparation.Name), HaveField("HealthyTime", Not(BeNil())))
}

// superseded matches the history entry of a deployment of preparation that
// another deployment replaced.
func superseded(preparation *deliveryv1alpha1.Preparation) types.GomegaMatcher {
	return SatisfyAll(
		HaveField("PreparationName", preparation.Name),
		HaveField("EndReason", deliveryv1alpha1.DeploymentEndReasonSuperseded),
	)
}
