package approval

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// sealed returns prep with its current votes sealed at base+1m.
func sealed(prep *deliveryv1alpha1.Preparation, approvals []deliveryv1alpha1.Approval) *deliveryv1alpha1.Preparation {
	sealedTime := metav1.NewTime(base.Add(time.Minute))
	prep.Status.Approval = Evaluate(prep, approvals).Status
	prep.Status.Approval.SealedTime = &sealedTime
	return prep
}

func TestGate(t *testing.T) {
	approve := []deliveryv1alpha1.Approval{vote("a", "alice", deliveryv1alpha1.ApprovalDecisionApprove, 0, 0)}

	t.Run("no policy", func(t *testing.T) {
		g := Gate(testPreparation(0), nil)
		assert.False(t, g.Blocked)
		assert.Equal(t, metav1.ConditionTrue, g.Status)
		assert.Equal(t, deliveryv1alpha1.ReasonApprovalNotRequired, g.Reason)
	})

	t.Run("awaiting approvals", func(t *testing.T) {
		g := Gate(testPreparation(1), nil)
		assert.True(t, g.Blocked)
		assert.Equal(t, metav1.ConditionFalse, g.Status)
		assert.Equal(t, deliveryv1alpha1.ReasonAwaitingApprovals, g.Reason)
	})

	t.Run("approved but not archived", func(t *testing.T) {
		g := Gate(testPreparation(1), approve)
		assert.True(t, g.Blocked)
		assert.Equal(t, deliveryv1alpha1.ReasonSealingApprovals, g.Reason)
	})

	t.Run("approved and archived", func(t *testing.T) {
		prep := sealed(testPreparation(1), approve)
		prep.Status.Approval.Attestation = &deliveryv1alpha1.ApprovalAttestation{OCIRef: "oci://registry.example/shop@" + otherDigest, Digest: otherDigest}
		apimeta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeApprovalsSealed, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonSealed,
		})
		g := Gate(prep, approve)
		assert.False(t, g.Blocked)
		assert.Equal(t, deliveryv1alpha1.ReasonApproved, g.Reason)
		assert.True(t, isArchived(prep))
	})
}

func TestArchive(t *testing.T) {
	approvals := []deliveryv1alpha1.Approval{vote("a", "alice", deliveryv1alpha1.ApprovalDecisionApprove, 0, 0)}
	prep := sealed(testPreparation(1), approvals)
	fake := oci.NewFakeClient(nil)

	att, err := Archive(context.Background(), fake, prep, approvals)
	require.NoError(t, err)
	require.Len(t, fake.Referrers, 1)
	assert.Equal(t, AttestationArtifactType, fake.Referrers[0].Artifact.ArtifactType)
	assert.Equal(t, "oci://registry.example/shop@"+att.Digest, att.OCIRef)

	again, err := Archive(context.Background(), fake, prep, approvals)
	require.NoError(t, err)
	assert.Equal(t, att, again, "archiving the same seal must be idempotent")

	fake.PushReferrerErr = errors.New("registry unavailable")
	_, err = Archive(context.Background(), fake, prep, approvals)
	require.ErrorContains(t, err, "registry unavailable")

	_, err = Archive(context.Background(), fake, testPreparation(1), approvals)
	require.Error(t, err, "unsealed approvals must not be archived")
}

func TestPolicyAnnotations(t *testing.T) {
	got, err := PolicyAnnotations(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 2, AllowedGroups: []string{testGroup}}
	got, err = PolicyAnnotations(policy)
	require.NoError(t, err)
	var decoded deliveryv1alpha1.ApprovalPolicy
	require.NoError(t, json.Unmarshal([]byte(got[oci.AnnotationApprovalPolicy]), &decoded))
	assert.Equal(t, *policy, decoded)
}

func TestNewSealTime(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 999_000_000, time.FixedZone("CEST", 2*3600))
	assert.Equal(t, metav1.NewTime(time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)), NewSealTime(now))
}
