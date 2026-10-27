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
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

// Approvals are submitted through the API server, which records who approved
// from the user's OIDC login, so the specs sign in through Dex as the UI does.
var _ = Describe("Approvals", Label("approvals"), Serial, func() {
	Context("the approvals example, with two release approvers who sign in through OIDC", Ordered, func() {
		var ns, approvals string
		var server *framework.Server
		var preparation *deliveryv1alpha1.Preparation

		approve := func(token string) (int, string) {
			resp, body := server.Do(http.MethodPost, approvals, token, map[string]string{"decision": "Approve"})
			return resp.StatusCode, string(body)
		}

		BeforeAll(func() {
			By("signing users in through Dex, as make oidc sets up for local development")
			framework.EnableOIDC()
			server = framework.ForwardServer()

			ns = framework.CreateNamespace("approvals")
			framework.ApplyExample(ns, "approvals")
			preparation = framework.LatestPreparation(ns, "podinfo-approvals")
			approvals = "/api/v1/preparations/" + ns + "/" + preparation.Name + "/approvals"
		})

		It("holds the Preparation until it is approved", func() {
			Eventually(framework.Serving(ns, "podinfo-approvals")).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeApproved, deliveryv1alpha1.ReasonAwaitingApprovals))
			framework.ExpectNoApplication(ns, "podinfo-approvals")
		})

		It("refuses an approval by the shared admin account, which identifies no person", func() {
			status, body := approve(server.Login("admin", "admin"))
			Expect(status).To(Equal(http.StatusForbidden), body)
		})

		It("keeps holding the Preparation after the first of the two approvals", func() {
			status, body := approve(server.LoginOIDC("approver1@kokumi.dev", "password"))
			Expect(status).To(Equal(http.StatusCreated), body)

			Consistently(framework.Serving(ns, "podinfo-approvals"), 10*time.Second).Should(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeApproved, deliveryv1alpha1.ReasonAwaitingApprovals))
			framework.ExpectNoApplication(ns, "podinfo-approvals")
		})

		It("deploys the Preparation once a second release approver approves it", func() {
			status, body := approve(server.LoginOIDC("approver2@kokumi.dev", "password"))
			Expect(status).To(Equal(http.StatusCreated), body)

			framework.WaitForDeployment(ns, "podinfo-approvals", preparation.Spec.Artifact.Digest)
			Expect(framework.Serving(ns, "podinfo-approvals")()).To(framework.HaveConditionReason(
				deliveryv1alpha1.ConditionTypeApproved, deliveryv1alpha1.ReasonApproved))
		})

		It("seals the approvals as an attestation that stock cosign verifies", func() {
			Eventually(framework.Preparation(ns, preparation.Name)).Should(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeApprovalsSealed, metav1.ConditionTrue))
			Expect(framework.CosignVerifyAttestation(framework.KitchenPublicKey(),
				framework.PreparationRef(preparation), "https://kokumi.dev/attestations/approval/v1")).To(Succeed())
		})

		It("refuses votes once the approvals are sealed", func() {
			status, body := approve(server.LoginOIDC("editor@kokumi.dev", "password"))
			Expect(status).To(Equal(http.StatusConflict), body)
		})
	})
})
