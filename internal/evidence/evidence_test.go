package evidence

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

const (
	digest = "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	order  = "shop/podinfo-prod"
)

func testPreparation(policy *deliveryv1alpha1.ApprovalPolicy) *deliveryv1alpha1.Preparation {
	return &deliveryv1alpha1.Preparation{
		Name:      "podinfo-prod-c3c3c3c3c3c3",
		Namespace: "shop",
		UID:       "6f0c",
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName:      "podinfo-prod",
			Artifact:       deliveryv1alpha1.Artifact{OCIRef: "oci://registry.example/shop/podinfo-prod@" + digest, Digest: digest},
			ApprovalPolicy: policy,
		},
	}
}

// sign pushes the signature and the provenance of prep's artifact, rendered
// for renderedFor, signed by signer.
func sign(t *testing.T, registry *oci.FakeClient, signer *signing.Signer, prep *deliveryv1alpha1.Preparation, renderedFor string) {
	t.Helper()

	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	require.NoError(t, err)

	created := time.Date(2026, 10, 12, 9, 14, 3, 0, time.UTC)
	_, err = attestation.Sign(context.Background(), registry, signer, subject, created)
	require.NoError(t, err)

	namespace, name, _ := strings.Cut(renderedFor, "/")
	_, err = provenance.Attach(context.Background(), registry, signer, subject, &artifact.Config{
		SchemaVersion: artifact.ConfigSchemaVersion,
		Order:         &artifact.ConfigObject{Namespace: namespace, Name: name},
		CreatedTime:   created.Format(time.RFC3339),
		Source:        artifact.ConfigSource{OCI: "oci://ghcr.io/example/podinfo", Version: "1.0.0", Digest: digest},
		Renderer:      artifact.ConfigRenderer{Type: "Manifest", Version: "v1", Builder: "test"},
	})
	require.NoError(t, err)
}

// archive pushes the sealed approvals of prep with one vote of decision.
func archive(t *testing.T, registry *oci.FakeClient, signer *signing.Signer, prep *deliveryv1alpha1.Preparation, decision deliveryv1alpha1.ApprovalDecision) {
	t.Helper()

	submitted := metav1.NewMicroTime(time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	approvals := []deliveryv1alpha1.Approval{{
		Name:              "vote",
		Namespace:         prep.Namespace,
		CreationTimestamp: metav1.NewTime(submitted.Time),
		Spec: deliveryv1alpha1.ApprovalSpec{
			OrderName:      prep.Spec.OrderName,
			PreparationRef: deliveryv1alpha1.ApprovalPreparationReference{Name: prep.Name, UID: prep.UID, ArtifactDigest: digest},
			Approver:       deliveryv1alpha1.Approver{Issuer: "https://idp.example", Subject: "alice", Groups: []string{"release"}},
			Decision:       decision,
			SubmittedTime:  submitted,
		},
	}}

	sealed := approval.NewSealTime(submitted.Add(time.Minute))
	prep.Status.Approval = approval.Evaluate(prep, approvals).Status
	prep.Status.Approval.SealedTime = &sealed

	_, err := approval.Archive(context.Background(), registry, signer, prep, approvals)
	require.NoError(t, err)
}

func requireRejected(t *testing.T, err error, reason string) {
	t.Helper()

	var rejected *Rejected
	require.True(t, errors.As(err, &rejected), "want a rejection, got %v", err)
	require.Equal(t, reason, rejected.Reason)
}

func TestVerify(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	trust := signer.TrustSet()

	t.Run("accepts a signed artifact rendered for the Order", func(t *testing.T) {
		registry := oci.NewFakeClient(nil)
		prep := testPreparation(nil)
		sign(t, registry, signer, prep, order)

		require.NoError(t, Verify(ctx, registry, trust, prep, order))
	})

	t.Run("rejects an unsigned artifact", func(t *testing.T) {
		requireRejected(t, Verify(ctx, oci.NewFakeClient(nil), trust, testPreparation(nil), order), deliveryv1alpha1.ReasonSignatureMissing)
	})

	t.Run("rejects an artifact signed by an untrusted key", func(t *testing.T) {
		registry := oci.NewFakeClient(nil)
		prep := testPreparation(nil)
		sign(t, registry, signingtest.NewSigner(t), prep, order)

		requireRejected(t, Verify(ctx, registry, trust, prep, order), deliveryv1alpha1.ReasonSignatureInvalid)
	})

	t.Run("rejects an artifact rendered for another Order", func(t *testing.T) {
		registry := oci.NewFakeClient(nil)
		prep := testPreparation(nil)
		sign(t, registry, signer, prep, "shop/podinfo-dev")

		requireRejected(t, Verify(ctx, registry, trust, prep, order), deliveryv1alpha1.ReasonSignatureInvalid)
	})

	t.Run("rejects a reference that does not name the digest", func(t *testing.T) {
		prep := testPreparation(nil)
		prep.Spec.Artifact.Digest = "sha256:d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"

		requireRejected(t, Verify(ctx, oci.NewFakeClient(nil), trust, prep, order), deliveryv1alpha1.ReasonSignatureInvalid)
	})

	policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1, AllowedGroups: []string{"release"}}

	t.Run("requires sealed approvals when a policy applies", func(t *testing.T) {
		registry := oci.NewFakeClient(nil)
		prep := testPreparation(policy)
		sign(t, registry, signer, prep, order)
		requireRejected(t, Verify(ctx, registry, trust, prep, order), deliveryv1alpha1.ReasonSignatureMissing)

		archive(t, registry, signer, prep, deliveryv1alpha1.ApprovalDecisionApprove)
		require.NoError(t, Verify(ctx, registry, trust, prep, order))
	})

	t.Run("rejects sealed approvals that did not approve", func(t *testing.T) {
		registry := oci.NewFakeClient(nil)
		prep := testPreparation(policy)
		sign(t, registry, signer, prep, order)
		archive(t, registry, signer, prep, deliveryv1alpha1.ApprovalDecisionReject)

		requireRejected(t, Verify(ctx, registry, trust, prep, order), deliveryv1alpha1.ReasonSignatureInvalid)
	})

	t.Run("passes registry errors through", func(t *testing.T) {
		err := Verify(ctx, unreadable{oci.NewFakeClient(nil)}, trust, testPreparation(nil), order)
		require.Error(t, err)
		var rejected *Rejected
		require.False(t, errors.As(err, &rejected))
	})
}

// unreadable is a registry whose referrers cannot be listed.
type unreadable struct {
	*oci.FakeClient
}

func (unreadable) ListReferrers(context.Context, oci.Reference, string) ([]oci.Descriptor, error) {
	return nil, errors.New("registry unreachable")
}
