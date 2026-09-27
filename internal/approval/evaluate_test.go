package approval

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testDigest  = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	otherDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	testIssuer  = "https://issuer.example"
	testUID     = "prep-uid"
	testGroup   = "release"
)

var base = time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

func testPreparation(required int32) *deliveryv1alpha1.Preparation {
	p := &deliveryv1alpha1.Preparation{
		Name:      "shop-1",
		Namespace: "team-a",
		UID:       testUID,
		Spec: deliveryv1alpha1.PreparationSpec{
			OrderName: "shop",
			Artifact:  deliveryv1alpha1.Artifact{OCIRef: "oci://registry.example/shop@" + testDigest, Digest: testDigest},
		},
	}
	if required > 0 {
		p.Spec.ApprovalPolicy = &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: required, AllowedGroups: []string{testGroup}}
	}
	return p
}

// vote builds an Approval created at base+second, submitted at base+second+micro.
func vote(name, subject string, decision deliveryv1alpha1.ApprovalDecision, second, micro int, groups ...string) deliveryv1alpha1.Approval {
	if groups == nil {
		groups = []string{testGroup}
	}
	created := base.Add(time.Duration(second) * time.Second)
	return deliveryv1alpha1.Approval{
		Name:              name,
		CreationTimestamp: metav1.NewTime(created),
		Spec: deliveryv1alpha1.ApprovalSpec{
			OrderName: "shop",
			PreparationRef: deliveryv1alpha1.ApprovalPreparationReference{
				Name: "shop-1", UID: testUID, ArtifactDigest: testDigest,
			},
			Approver:      deliveryv1alpha1.Approver{Issuer: testIssuer, Subject: subject, Username: subject, Groups: groups},
			Decision:      decision,
			SubmittedTime: metav1.NewMicroTime(created.Add(time.Duration(micro) * time.Microsecond)),
		},
	}
}

func TestEvaluate(t *testing.T) {
	approve, reject := deliveryv1alpha1.ApprovalDecisionApprove, deliveryv1alpha1.ApprovalDecisionReject

	tests := []struct {
		name         string
		required     int32
		approvals    []deliveryv1alpha1.Approval
		wantApproved bool
		wantReason   string
		wantVerdicts map[string]string
	}{
		{
			name:         "no policy",
			approvals:    []deliveryv1alpha1.Approval{vote("a", "alice", approve, 0, 0)},
			wantApproved: true,
			wantReason:   deliveryv1alpha1.ReasonApprovalNotRequired,
			wantVerdicts: map[string]string{"a": deliveryv1alpha1.ReasonApprovalNotRequired},
		},
		{
			name:       "awaiting approvals",
			required:   2,
			approvals:  []deliveryv1alpha1.Approval{vote("a", "alice", approve, 0, 0)},
			wantReason: deliveryv1alpha1.ReasonAwaitingApprovals,
		},
		{
			name:         "quorum reached by distinct approvers",
			required:     2,
			approvals:    []deliveryv1alpha1.Approval{vote("a", "alice", approve, 0, 0), vote("b", "bob", approve, 1, 0)},
			wantApproved: true,
			wantReason:   deliveryv1alpha1.ReasonApproved,
		},
		{
			name:       "repeated votes of one approver count once",
			required:   2,
			approvals:  []deliveryv1alpha1.Approval{vote("a1", "alice", approve, 0, 0), vote("a2", "alice", approve, 1, 0)},
			wantReason: deliveryv1alpha1.ReasonAwaitingApprovals,
			wantVerdicts: map[string]string{
				"a1": deliveryv1alpha1.ReasonSuperseded,
				"a2": deliveryv1alpha1.ReasonCounted,
			},
		},
		{
			name:       "eligible reject vetoes",
			required:   1,
			approvals:  []deliveryv1alpha1.Approval{vote("a", "alice", approve, 0, 0), vote("b", "bob", reject, 1, 0)},
			wantReason: deliveryv1alpha1.ReasonChangesRequested,
		},
		{
			name:         "ineligible reject does not veto",
			required:     1,
			approvals:    []deliveryv1alpha1.Approval{vote("a", "alice", approve, 0, 0), vote("m", "mallory", reject, 1, 0, "other")},
			wantApproved: true,
			wantReason:   deliveryv1alpha1.ReasonApproved,
			wantVerdicts: map[string]string{"m": deliveryv1alpha1.ReasonNotEligible},
		},
		{
			name:     "latest vote wins within the same second",
			required: 1,
			approvals: []deliveryv1alpha1.Approval{
				vote("z-approve", "alice", approve, 0, 900),
				vote("a-reject", "alice", reject, 0, 100),
			},
			wantApproved: true,
			wantReason:   deliveryv1alpha1.ReasonApproved,
			wantVerdicts: map[string]string{"a-reject": deliveryv1alpha1.ReasonSuperseded},
		},
		{
			name:     "votes for another artifact are ignored",
			required: 1,
			approvals: func() []deliveryv1alpha1.Approval {
				a := vote("a", "alice", approve, 0, 0)
				a.Spec.PreparationRef.ArtifactDigest = otherDigest
				return []deliveryv1alpha1.Approval{a}
			}(),
			wantReason:   deliveryv1alpha1.ReasonAwaitingApprovals,
			wantVerdicts: map[string]string{"a": deliveryv1alpha1.ReasonDigestMismatch},
		},
		{
			name:     "votes for a recreated preparation are ignored",
			required: 1,
			approvals: func() []deliveryv1alpha1.Approval {
				a := vote("a", "alice", approve, 0, 0)
				a.Spec.PreparationRef.UID = "old-uid"
				return []deliveryv1alpha1.Approval{a}
			}(),
			wantReason:   deliveryv1alpha1.ReasonAwaitingApprovals,
			wantVerdicts: map[string]string{"a": deliveryv1alpha1.ReasonDigestMismatch},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Evaluate(testPreparation(tt.required), tt.approvals)
			assert.Equal(t, tt.wantApproved, res.Approved)
			assert.Equal(t, tt.wantReason, res.Reason)
			for name, reason := range tt.wantVerdicts {
				assert.Equal(t, reason, res.Verdicts[name].Reason, "verdict of %s", name)
			}
		})
	}
}

func TestEvaluateSealed(t *testing.T) {
	approve, reject := deliveryv1alpha1.ApprovalDecisionApprove, deliveryv1alpha1.ApprovalDecisionReject
	prep := testPreparation(1)
	early := vote("a1", "alice", reject, 0, 0)
	counted := vote("a2", "alice", approve, 1, 0)

	open := Evaluate(prep, []deliveryv1alpha1.Approval{early, counted})
	require.True(t, open.Approved)
	sealedTime := metav1.NewTime(base.Add(2 * time.Second))
	prep.Status.Approval = open.Status
	prep.Status.Approval.SealedTime = &sealedTime

	late := vote("a3", "alice", reject, 5, 0)
	lateOther := vote("b1", "bob", reject, 5, 0)
	res := Evaluate(prep, []deliveryv1alpha1.Approval{early, counted, late, lateOther})

	assert.True(t, res.Approved, "votes after the seal must not change the outcome")
	assert.Equal(t, deliveryv1alpha1.ReasonSuperseded, res.Verdicts["a1"].Reason)
	assert.Equal(t, deliveryv1alpha1.ReasonCounted, res.Verdicts["a2"].Reason)
	assert.Equal(t, deliveryv1alpha1.ReasonSubmittedAfterSeal, res.Verdicts["a3"].Reason)
	assert.Equal(t, deliveryv1alpha1.ReasonSubmittedAfterSeal, res.Verdicts["b1"].Reason)
	assert.Equal(t, int32(4), res.Status.SubmissionCount)
	assert.Equal(t, &sealedTime, res.Status.SealedTime)
}

func TestBuildAttestation(t *testing.T) {
	prep := testPreparation(1)
	approvals := make([]deliveryv1alpha1.Approval, 0, 2)
	approvals = append(approvals, vote("a1", "alice", deliveryv1alpha1.ApprovalDecisionApprove, 0, 0))

	_, err := buildAttestation(prep, approvals)
	require.Error(t, err, "unsealed approvals must not be attested")

	res := Evaluate(prep, approvals)
	sealedTime := metav1.NewTime(base.Add(time.Minute))
	prep.Status.Approval = res.Status
	prep.Status.Approval.SealedTime = &sealedTime

	first, err := buildAttestation(prep, approvals)
	require.NoError(t, err)

	withLate := append(approvals, vote("b1", "bob", deliveryv1alpha1.ApprovalDecisionReject, 120, 0))
	second, err := buildAttestation(prep, withLate)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(first, second), "the attestation must be deterministic and exclude late votes")
	assert.Contains(t, string(first), `"predicateType":"`+AttestationPredicateType+`"`)
	assert.Contains(t, string(first), `"sha256":"`+testDigest[len("sha256:"):]+`"`)
	assert.Contains(t, string(first), `"result":"Approved"`)
}
