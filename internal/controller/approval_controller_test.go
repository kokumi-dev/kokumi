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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// createTestApproval submits a vote the way the kokumi server would.
func createTestApproval(ctx context.Context, prep *deliveryv1alpha1.Preparation, subject string, groups []string, decision deliveryv1alpha1.ApprovalDecision) *deliveryv1alpha1.Approval {
	a := &deliveryv1alpha1.Approval{
		GenerateName: prep.Name + "-",
		Namespace:    prep.Namespace,
		Spec: deliveryv1alpha1.ApprovalSpec{
			OrderName: prep.Spec.OrderName,
			PreparationRef: deliveryv1alpha1.ApprovalPreparationReference{
				Name:           prep.Name,
				UID:            prep.UID,
				ArtifactDigest: prep.Spec.Artifact.Digest,
			},
			Approver: deliveryv1alpha1.Approver{
				Issuer:   testIssuer,
				Subject:  subject,
				Username: subject,
				Groups:   groups,
			},
			Decision:      decision,
			SubmittedTime: metav1.NewMicroTime(time.Now()),
		},
	}
	Expect(k8sClient.Create(ctx, a)).To(Succeed())
	return a
}

// deleteApprovalsOfOrder removes the Approvals of orderName; envtest runs
// without the admission policies that forbid this in a real cluster.
func deleteApprovalsOfOrder(ctx context.Context, orderName string) {
	Expect(k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Approval{},
		client.InNamespace(testNamespace), client.MatchingFields{deliveryv1alpha1.FieldOrderName: orderName})).To(Succeed())
}

var _ = Describe("Approval Controller", func() {
	ctx := context.Background()
	release := []string{testApproverGroup}

	reconcileApproval := func(a *deliveryv1alpha1.Approval) *metav1.Condition {
		r := &ApprovalReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(a), a)).To(Succeed())
		return apimeta.FindStatusCondition(a.Status.Conditions, deliveryv1alpha1.ConditionTypeCounted)
	}

	Context("When the referenced Preparation does not exist", func() {
		orphanApproval := func() *deliveryv1alpha1.Approval {
			a := createTestApproval(ctx, &deliveryv1alpha1.Preparation{
				Name:      "missing-preparation",
				Namespace: testNamespace,
				UID:       "00000000-0000-0000-0000-000000000000",
				Spec: deliveryv1alpha1.PreparationSpec{
					OrderName: "missing-order",
					Artifact:  deliveryv1alpha1.Artifact{Digest: "sha256:3333333333333333333333333333333333333333333333333333333333333333"},
				},
			}, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, a) })
			return a
		}

		It("reports the vote as not counted", func() {
			cond := reconcileApproval(orphanApproval())
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonPreparationNotFound))
		})

		It("rejects changes to the vote", func() {
			a := orphanApproval()

			a.Spec.Decision = deliveryv1alpha1.ApprovalDecisionReject
			Expect(k8sClient.Update(ctx, a)).To(MatchError(ContainSubstring("Approval spec is immutable")))
		})
	})

	Context("When the referenced Preparation has an approval policy", func() {
		const (
			orderName = "approval-verdicts"
			prepName  = "approval-verdicts-p1"
			digest    = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
			other     = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
		)
		var prep *deliveryv1alpha1.Preparation

		BeforeEach(func() {
			prep = createTestPreparation(ctx, prepName, orderName, digest,
				&deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1, AllowedGroups: release})
			DeferCleanup(func() {
				deleteApprovalsOfOrder(ctx, orderName)
				_ = k8sClient.Delete(ctx, prep)
			})
		})

		It("counts only the latest vote of each eligible approver", func() {
			first := createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionReject)
			latest := createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			ineligible := createTestApproval(ctx, prep, "mallory", []string{testIneligibleGroup}, deliveryv1alpha1.ApprovalDecisionApprove)

			Expect(reconcileApproval(first).Reason).To(Equal(deliveryv1alpha1.ReasonSuperseded))
			cond := reconcileApproval(latest)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonCounted))
			Expect(reconcileApproval(ineligible).Reason).To(Equal(deliveryv1alpha1.ReasonNotEligible))
		})

		It("ignores votes cast for a different artifact", func() {
			forged := createTestApproval(ctx, &deliveryv1alpha1.Preparation{
				ObjectMeta: prep.ObjectMeta,
				Spec:       deliveryv1alpha1.PreparationSpec{OrderName: orderName, Artifact: deliveryv1alpha1.Artifact{Digest: other}},
			}, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)

			Expect(reconcileApproval(forged).Reason).To(Equal(deliveryv1alpha1.ReasonDigestMismatch))
		})

		It("ignores votes submitted after the approvals were sealed", func() {
			sealedTime := metav1.NewTime(time.Now().Add(-time.Minute).Truncate(time.Second))
			prep.Status.Approval = &deliveryv1alpha1.PreparationApprovalStatus{RequiredApprovals: 1, SealedTime: &sealedTime}
			Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())

			late := createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			Expect(reconcileApproval(late).Reason).To(Equal(deliveryv1alpha1.ReasonSubmittedAfterSeal))
		})
	})
})
