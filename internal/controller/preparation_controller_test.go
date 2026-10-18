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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
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
							URL:        testOCIRef,
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
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				APIReader:  k8sClient,
				Registries: fakeRegistries(fakeOCI),
				Signing:    testSigning,
			}

			// Finalizer, signed evidence, approvals: one reconcile each.
			for range 3 {
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			By("reporting that no approval is required without a policy")
			Expect(k8sClient.Get(ctx, typeNamespacedName, preparation)).To(Succeed())
			Expect(apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(preparation.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)).To(BeTrue())
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

		var (
			fakeOCI *oci.FakeClient
			signer  *signing.Provider
		)
		reconcilePrepResult := func() (reconcile.Result, error) {
			r := &PreparationReconciler{
				Client:     k8sClient,
				Scheme:     k8sClient.Scheme(),
				APIReader:  k8sClient,
				Registries: fakeRegistries(fakeOCI),
				Signing:    signer,
			}
			return r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		}
		reconcilePrep := func() error {
			_, err := reconcilePrepResult()
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
			Expect(reconcilePrep()).To(Succeed()) // signs, attaches provenance, marks Ready
			Expect(reconcilePrep()).To(Succeed()) // aggregates the votes
			return getPrep()
		}

		BeforeEach(func() {
			fakeOCI = oci.NewFakeClient(nil)
			signer = testSigning
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

		It("signs and attaches provenance exactly once and only then becomes ready", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)

			By("staying not ready while the registry rejects the attestations")
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)
			fakeOCI.PushReferrerErr = errors.New("registry unavailable")
			Expect(reconcilePrep()).To(MatchError(ContainSubstring("registry unavailable")))
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeFalse())
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonSignFailed))
			Expect(prep.Status.Attestations).To(BeEmpty())

			By("becoming ready once both attestations are recorded")
			fakeOCI.PushReferrerErr = nil
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeProvenanceAttached)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(prep.Status.Attestations).To(HaveLen(2))
			Expect(fakeOCI.Referrers).To(HaveLen(2))

			subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
			Expect(err).NotTo(HaveOccurred())
			for _, want := range []struct {
				attestationType deliveryv1alpha1.AttestationType
				predicateType   string
			}{
				{deliveryv1alpha1.AttestationTypeSignature, attestation.SignaturePredicateType},
				{deliveryv1alpha1.AttestationTypeProvenance, provenance.PredicateType},
			} {
				evidence, err := attestation.Find(ctx, fakeOCI, testSigner.TrustSet(), subject, want.predicateType)
				Expect(err).NotTo(HaveOccurred())
				Expect(attestationDigest(prep, want.attestationType)).To(Equal(evidence.Digest))
			}

			By("pushing nothing new on later reconciles")
			Expect(reconcilePrep()).To(Succeed())
			Expect(reconcilePrep()).To(Succeed())
			Expect(fakeOCI.Referrers).To(HaveLen(2))
		})

		It("reuses the referrers it pushed when the status write was lost", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)
			Expect(reconcilePrep()).To(Succeed())
			first := getPrep().Status.Attestations

			By("forgetting the recorded evidence, as after a crash before the status write")
			prep = getPrep()
			prep.Status.Attestations = nil
			prep.Status.Conditions = nil
			Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())

			Expect(reconcilePrep()).To(Succeed())
			Expect(getPrep().Status.Attestations).To(Equal(first))
			Expect(fakeOCI.Referrers).To(HaveLen(2), "the same referrers are found, not pushed again")
		})

		It("signs a Preparation that was ready before signing existed", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)

			prep = getPrep()
			apimeta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
				Type: deliveryv1alpha1.ConditionTypeProvenanceAttached, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonProvenanceAttached,
			})
			apimeta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
				Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason,
			})
			Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())

			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)).To(BeTrue())
			Expect(prep.Status.Attestations).To(HaveLen(2), "the provenance is attached again as a signed bundle")
		})

		It("waits for the signing key", func() {
			prep := createTestPreparation(ctx, prepName, orderName, digest, nil)
			seedArtifact(fakeOCI, prep.Spec.Artifact.OCIRef, digest)
			signer = signing.NewProvider(SigningInputs(k8sClient, testNamespace, "no-signing-key"))

			result, err := reconcilePrepResult()
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			prep = getPrep()
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonSigningKeyNotReady))
			Expect(apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady).Reason).To(Equal(deliveryv1alpha1.ReasonAwaitingSignature))
			Expect(fakeOCI.Referrers).To(BeEmpty())
		})

		It("stays not ready and retries while the artifact cannot be read", func() {
			createTestPreparation(ctx, prepName, orderName, digest, nil)

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

			err = reconcilePrep()
			Expect(errors.Is(err, reconcile.TerminalError(nil))).To(BeTrue(), "the artifact is immutable, so the failure is terminal: %v", err)
			prep = getPrep()
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeFalse())
			cond := apimeta.FindStatusCondition(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeSigned)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonSignFailed))
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

			By("sealing the votes")
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(prep.Status.Approval.SealedTime).NotTo(BeNil())
			Expect(prep.Status.Approval.Attestation).To(BeNil(), "the votes are sealed before they are archived")
			Expect(referrersOfPredicate(fakeOCI, approval.AttestationPredicateType)).To(BeEmpty())

			By("archiving them in the pass the seal triggers")
			Expect(reconcilePrep()).To(Succeed())
			prep = getPrep()
			Expect(prep.Status.Approval.Attestation).NotTo(BeNil())
			Expect(apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeApprovalsSealed)).To(BeTrue())
			approvals := referrersOfPredicate(fakeOCI, approval.AttestationPredicateType)
			Expect(approvals).To(HaveLen(1))
			Expect(approvals[0].Subject.OCIString()).To(Equal(prep.Spec.Artifact.OCIRef))

			subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
			Expect(err).NotTo(HaveOccurred())
			evidence, err := attestation.Find(ctx, fakeOCI, testSigner.TrustSet(), subject, approval.AttestationPredicateType)
			Expect(err).NotTo(HaveOccurred())
			Expect(evidence.Digest).To(Equal(prep.Status.Approval.Attestation.Digest))
			Expect(string(evidence.Statement)).To(ContainSubstring(`"subject":"alice"`))

			By("ignoring votes after the seal")
			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionReject)
			Expect(reconcilePrep()).To(Succeed())
			Expect(approvedReason(getPrep())).To(Equal(deliveryv1alpha1.ReasonApproved))
			Expect(referrersOfPredicate(fakeOCI, approval.AttestationPredicateType)).To(HaveLen(1), "the attestation is pushed exactly once")
		})

		It("retries archiving without resealing when the registry push fails", func() {
			prep := setup(1)
			createTestApproval(ctx, prep, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			targetPreparation()

			fakeOCI.PushReferrerErr = errors.New("registry unavailable")
			Expect(reconcilePrep()).To(Succeed()) // seals the votes
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

// referrersOfPredicate returns the Sigstore bundle referrers of the given
// predicate type pushed to fake.
func referrersOfPredicate(fake *oci.FakeClient, predicateType string) []oci.PushedReferrer {
	var referrers []oci.PushedReferrer
	for _, r := range fake.Referrers {
		if r.Artifact.ArtifactType == oci.SigstoreBundleMediaType && r.Artifact.Annotations[oci.AnnotationBundlePredicateType] == predicateType {
			referrers = append(referrers, r)
		}
	}
	return referrers
}

// attestationDigest returns the digest of the attestation of type
// recorded on prep.
func attestationDigest(prep *deliveryv1alpha1.Preparation, attestationType deliveryv1alpha1.AttestationType) string {
	for _, a := range prep.Status.Attestations {
		if a.Type == attestationType {
			return a.Digest
		}
	}
	return ""
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
				URL:        testOCIRef,
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
