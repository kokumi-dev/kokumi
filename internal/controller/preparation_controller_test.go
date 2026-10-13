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
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

var _ = Describe("Preparation Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "preparation"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testNamespace,
		}
		preparation := &deliveryv1alpha1.Preparation{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Preparation")
			err := k8sClient.Get(ctx, typeNamespacedName, preparation)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Preparation{
					Name:      resourceName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.PreparationSpec{
						OrderName:  "order",
						ConfigHash: "sha256:448093f1b28dc7147740d8e400946e9b228650aa31a54b0ed734ca9ab0ae5b6b",
						Renderer: deliveryv1alpha1.Renderer{
							Type:    deliveryv1alpha1.RenderTypeManifest,
							Version: testVersion,
							Builder: testBuilderID,
						},
						Source: deliveryv1alpha1.OrderSource{
							OCI:        testOCIRef,
							BaseDigest: "sha256:6c2069fa6684d3659d93538331711b09a33cb42ae305802195d6a4d58847b345",
						},
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@sha256:fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc",
							Digest: "sha256:fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Preparation{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Preparation")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			Expect(k8sClient.Get(ctx, typeNamespacedName, preparation)).To(Succeed())
			fakeOCI := oci.NewFakeClient(nil)
			seedArtifact(fakeOCI, preparation.Spec.Artifact.OCIRef, preparation.Spec.Artifact.Digest)
			controllerReconciler := &PreparationReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				APIReader:      k8sClient,
				OCIClient:      fakeOCI,
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}

			// Finalizer, provenance, approvals: one reconcile each.
			for range 3 {
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("reporting that no approval is required without a policy")
			Expect(k8sClient.Get(ctx, typeNamespacedName, preparation)).To(Succeed())
			Expect(apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)).To(BeTrue())
			cond := apimeta.FindStatusCondition(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeApproved)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonApprovalNotRequired))
			Expect(preparation.Status.Approval).To(BeNil())
		})
	})

	Context("When the Preparation has an approval policy", func() {
		const (
			orderName = "prep-gated"
			prepName  = "prep-gated-p1"
			digest    = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
		)
		release := []string{testApproverGroup}
		key := types.NamespacedName{Namespace: testNamespace, Name: prepName}

		var fakeOCI *oci.FakeClient
		reconcilePrep := func() error {
			r := &PreparationReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				APIReader:      k8sClient,
				OCIClient:      fakeOCI,
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			return err
		}
		getPrep := func() *deliveryv1alpha1.Preparation {
			p := &deliveryv1alpha1.Preparation{}
			Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
			return p
		}
		approvedReason := func(p *deliveryv1alpha1.Preparation) string {
			if c := apimeta.FindStatusCondition(p.Status.Conditions, deliveryv1alpha1.ConditionTypeApproved); c != nil {
				return c.Reason
			}
			return ""
		}
		// targetPreparation simulates the Serving controller choosing prepName as promotion target.
		targetPreparation := func() {
			serving := &deliveryv1alpha1.Serving{
				Name:      orderName,
				Namespace: testNamespace,
				Spec:      deliveryv1alpha1.ServingSpec{OrderName: orderName, PreparationName: prepName},
			}
			Expect(k8sClient.Create(ctx, serving)).To(Succeed())
			serving.Status.TargetPreparationName = prepName
			Expect(k8sClient.Status().Update(ctx, serving)).To(Succeed())
		}
		setup := func(required int32) *deliveryv1alpha1.Preparation {
			prep := createTestPreparation(ctx, prepName, orderName, digest,
				&deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: required, AllowedGroups: release})
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)
			Expect(reconcilePrep()).To(Succeed()) // adds the finalizer
			Expect(reconcilePrep()).To(Succeed()) // attaches provenance, marks Ready
			Expect(reconcilePrep()).To(Succeed())
			return getPrep()
		}

		BeforeEach(func() {
			fakeOCI = oci.NewFakeClient(nil)
			DeferCleanup(func() {
				deleteApprovalsOfOrder(ctx, orderName)
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: orderName, Namespace: testNamespace})
				prep := &deliveryv1alpha1.Preparation{}
				if err := k8sClient.Get(ctx, key, prep); err == nil {
					prep.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, prep)
				}
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: prepName, Namespace: testNamespace})
			})
		})

		It("attaches provenance exactly once and only then becomes ready", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)

			By("staying not ready while the registry rejects the attestation")
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)
			Expect(reconcilePrep()).To(Succeed()) // adds the finalizer
			fakeOCI.PushReferrerErr = errors.New("registry unavailable")
			Expect(reconcilePrep()).To(MatchError(ContainSubstring("registry unavailable")))
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeFalse())
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonProvenanceAttachFailed))
			Expect(prep.Status.Attestations).To(BeEmpty())

			By("becoming ready once the attestation is recorded")
			fakeOCI.PushReferrerErr = nil
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(prep.Status.Attestations).To(HaveLen(1))
			Expect(prep.Status.Attestations[0].Type).To(Equal(deliveryv1alpha1.AttestationTypeProvenance))
			pushed := referrersOfType(fakeOCI, oci.ProvenanceArtifactType)
			Expect(pushed).To(HaveLen(1))
			Expect(pushed[0].Subject.OCIString()).To(Equal(prep.Spec.Artifact.OCIRef))
			Expect(prep.Status.Attestations[0].Digest).To(Equal(pushed[0].Digest))

			By("pushing nothing new on later reconciles")
			Expect(reconcilePrep()).To(Succeed())
			Expect(reconcilePrep()).To(Succeed())
			Expect(fakeOCI.Referrers).To(HaveLen(1))
		})

		It("stays not ready and retries while the artifact cannot be read", func() {
			createTestPreparation(ctx, prepName, orderName, digest, nil)

			Expect(reconcilePrep()).To(Succeed()) // adds the finalizer
			Expect(reconcilePrep()).To(HaveOccurred())
			prep := getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeFalse())
			Expect(fakeOCI.Referrers).To(BeEmpty())
		})

		It("is never ready and does not retry when the artifact carries no Kokumi config", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)
			ref, err := oci.Parse(prep.Spec.Artifact.OCIRef)
			Expect(err).NotTo(HaveOccurred())
			fakeOCI.Pushes = append(fakeOCI.Pushes, oci.PushedArtifact{
				Ref:     ref,
				Digest:  digest,
				Options: oci.PushOptions{Config: []byte("{}"), ConfigMediaType: "application/vnd.oci.empty.v1+json"},
			})

			Expect(reconcilePrep()).To(Succeed()) // adds the finalizer
			Expect(reconcilePrep()).To(Succeed(), "the artifact is immutable, so the failure is terminal")
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeFalse())
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonProvenanceAttachFailed))
			Expect(fakeOCI.Referrers).To(BeEmpty())
		})

		It("aggregates the latest votes into the Approved condition", func() {
			prep := setup(2)
			Expect(approvedReason(prep)).To(Equal(deliveryv1alpha1.ReasonAwaitingApprovals))
			Expect(prep.Status.Approval.RequiredApprovals).To(Equal(int32(2)))

			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			createTestApproval(ctx, prep, "bob", release, deliveryv1alpha1.ApprovalDecisionReject)
			createTestApproval(ctx, prep, "mallory", []string{testIneligibleGroup}, deliveryv1alpha1.ApprovalDecisionApprove)
			Expect(reconcilePrep()).To(Succeed())

			prep = getPrep()
			Expect(approvedReason(prep)).To(Equal(deliveryv1alpha1.ReasonChangesRequested))
			Expect(prep.Status.Approval.ApprovedCount).To(Equal(int32(1)))
			Expect(prep.Status.Approval.RejectedCount).To(Equal(int32(1)))
			Expect(prep.Status.Approval.IneligibleCount).To(Equal(int32(1)))
			Expect(prep.Status.Approval.SubmissionCount).To(Equal(int32(3)))

			createTestApproval(ctx, prep, "bob", release, deliveryv1alpha1.ApprovalDecisionApprove)
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(approvedReason(prep)).To(Equal(deliveryv1alpha1.ReasonApproved))
			Expect(prep.Status.Approval.SealedTime).To(BeNil(), "an approved Preparation that is not promoted stays open")
		})

		It("seals and archives the votes once a Serving targets the approved Preparation", func() {
			prep := setup(1)
			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			targetPreparation()
			Expect(reconcilePrep()).To(Succeed())

			prep = getPrep()
			Expect(prep.Status.Approval.SealedTime).NotTo(BeNil())
			Expect(prep.Status.Approval.Attestation).NotTo(BeNil())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeApprovalsSealed)).To(BeTrue())
			approvals := referrersOfType(fakeOCI, oci.ApprovalsArtifactType)
			Expect(approvals).To(HaveLen(1))
			Expect(approvals[0].Subject.OCIString()).To(Equal(prep.Spec.Artifact.OCIRef))
			Expect(string(approvals[0].Artifact.Payload)).To(ContainSubstring(`"subject":"alice"`))

			By("ignoring votes after the seal")
			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionReject)
			Expect(reconcilePrep()).To(Succeed())
			Expect(approvedReason(getPrep())).To(Equal(deliveryv1alpha1.ReasonApproved))
			Expect(referrersOfType(fakeOCI, oci.ApprovalsArtifactType)).To(HaveLen(1), "the attestation is pushed exactly once")
		})

		It("retries archiving without resealing when the registry push fails", func() {
			prep := setup(1)
			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			targetPreparation()

			fakeOCI.PushReferrerErr = errors.New("registry unavailable")
			Expect(reconcilePrep()).To(MatchError(ContainSubstring("registry unavailable")))
			prep = getPrep()
			sealedTime := prep.Status.Approval.SealedTime
			Expect(sealedTime).NotTo(BeNil())
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeApprovalsSealed)
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonArchiveFailed))

			fakeOCI.PushReferrerErr = nil
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(prep.Status.Approval.SealedTime).To(Equal(sealedTime))
			Expect(prep.Status.Approval.Attestation).NotTo(BeNil())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeApprovalsSealed)).To(BeTrue())
		})
	})
})

// seedArtifact records an artifact with a Kokumi config blob in fake, so the
// Preparation controller can attach provenance to it.
func seedArtifact(fake *oci.FakeClient, ociRef, digest string) {
	ref, err := oci.Parse(ociRef)
	Expect(err).NotTo(HaveOccurred())

	config, err := json.Marshal(artifact.Config{
		SchemaVersion: artifact.ConfigSchemaVersion,
		CreatedTime:   "2026-10-12T09:14:03Z",
		Source:        artifact.ConfigSource{OCI: testOCIRef, Version: testVersion, Digest: digest},
		Renderer:      artifact.ConfigRenderer{Type: "Manifest", Version: testRendererVersion, Builder: testBuilderID},
	})
	Expect(err).NotTo(HaveOccurred())

	fake.Pushes = append(fake.Pushes, oci.PushedArtifact{
		Ref:     ref,
		Digest:  digest,
		Options: oci.PushOptions{Config: config, ConfigMediaType: oci.PreparationConfigMediaType},
	})
}

// referrersOfType returns the referrers of the given artifact type pushed to fake.
func referrersOfType(fake *oci.FakeClient, artifactType string) []oci.PushedReferrer {
	var referrers []oci.PushedReferrer
	for _, r := range fake.Referrers {
		if r.Artifact.ArtifactType == artifactType {
			referrers = append(referrers, r)
		}
	}
	return referrers
}

// createTestPreparation creates a Preparation for orderName with the given
// artifact digest and approval policy snapshot.
func createTestPreparation(ctx context.Context, name, orderName, digest string, policy *deliveryv1alpha1.ApprovalPolicy) *deliveryv1alpha1.Preparation {
	prep := &deliveryv1alpha1.Preparation{
		Name:      name,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: orderName,
			Source: deliveryv1alpha1.OrderSource{
				OCI:        testOCIRef,
				BaseDigest: digest,
			},
			Renderer: deliveryv1alpha1.Renderer{
				Type:    deliveryv1alpha1.RenderTypeManifest,
				Version: testRendererVersion,
				Builder: testBuilderID,
			},
			ConfigHash: testConfigHash,
			Artifact: deliveryv1alpha1.Artifact{
				OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/" + orderName + "@" + digest,
				Digest: digest,
			},
			ApprovalPolicy: policy,
		},
	}
	Expect(k8sClient.Create(ctx, prep)).To(Succeed())
	return prep
}
