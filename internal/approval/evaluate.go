package approval

import (
	"fmt"
	"math"
	"slices"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// MaxApprovers bounds the number of distinct approvers per Preparation so the
// aggregated votes fit into the Preparation status.
const MaxApprovers = 64

// Verdict describes how a single Approval is evaluated.
type Verdict struct {
	Counted bool
	Reason  string
	Message string
}

// Result is the outcome of evaluating the approval gate of a Preparation.
type Result struct {
	// Required is false when the Preparation has no approval policy.
	Required bool
	// Approved is true when the Preparation may be served.
	Approved bool
	// Reason is one of the Approved condition reasons.
	Reason  string
	Message string
	// Status is the aggregated approval status; nil when not Required.
	Status *deliveryv1alpha1.PreparationApprovalStatus
	// Verdicts holds the evaluation of each Approval keyed by name.
	Verdicts map[string]Verdict
}

// Evaluate computes the approval gate of prep from approvals, which must be
// the Approvals referencing prep by name. Only the latest vote per approver
// counts, and an eligible Reject vetoes. Once the Preparation is sealed, the
// sealed votes in its status are authoritative.
func Evaluate(prep *deliveryv1alpha1.Preparation, approvals []deliveryv1alpha1.Approval) Result {
	res := Result{Verdicts: make(map[string]Verdict, len(approvals))}
	policy := prep.Spec.ApprovalPolicy

	if policy == nil {
		res.Approved = true
		res.Reason = deliveryv1alpha1.ReasonApprovalNotRequired
		res.Message = "No approval policy applies to this Preparation"
		for i := range approvals {
			res.Verdicts[approvals[i].Name] = Verdict{
				Reason:  deliveryv1alpha1.ReasonApprovalNotRequired,
				Message: "The Preparation has no approval policy",
			}
		}
		return res
	}
	res.Required = true

	matching := make([]*deliveryv1alpha1.Approval, 0, len(approvals))
	for i := range approvals {
		a := &approvals[i]
		if a.Spec.PreparationRef.UID != prep.UID || a.Spec.PreparationRef.ArtifactDigest != prep.Spec.Artifact.Digest {
			res.Verdicts[a.Name] = Verdict{
				Reason:  deliveryv1alpha1.ReasonDigestMismatch,
				Message: "The vote was cast for a different Preparation object or artifact digest",
			}
			continue
		}
		matching = append(matching, a)
	}
	slices.SortFunc(matching, compareApprovals)

	status := &deliveryv1alpha1.PreparationApprovalStatus{
		RequiredApprovals: policy.RequiredApprovals,
		SubmissionCount:   int32(min(len(approvals), math.MaxInt32)), //nolint:gosec // bounded by min
	}

	var votes []deliveryv1alpha1.ApprovalVote
	if sealed := sealedStatus(prep); sealed != nil {
		votes = sealed.Votes
		status.SealedTime = sealed.SealedTime
		status.Attestation = sealed.Attestation
		evaluateSealed(matching, votes, res.Verdicts)
	} else {
		votes = evaluateOpen(policy, matching, res.Verdicts)
	}
	status.Votes = votes

	var rejectedBy, approvedBy []string
	for _, v := range votes {
		if v.Result != deliveryv1alpha1.VoteResultCounted {
			status.IneligibleCount++
			continue
		}
		switch v.Decision {
		case deliveryv1alpha1.ApprovalDecisionApprove:
			status.ApprovedCount++
			approvedBy = append(approvedBy, displayName(v))
		case deliveryv1alpha1.ApprovalDecisionReject:
			status.RejectedCount++
			rejectedBy = append(rejectedBy, displayName(v))
		}
	}
	res.Status = status

	switch {
	case status.RejectedCount > 0:
		res.Reason = deliveryv1alpha1.ReasonChangesRequested
		res.Message = "Changes requested by " + strings.Join(rejectedBy, ", ")
	case status.ApprovedCount >= policy.RequiredApprovals:
		res.Approved = true
		res.Reason = deliveryv1alpha1.ReasonApproved
		res.Message = fmt.Sprintf("%d of %d required approvals: approved by %s",
			status.ApprovedCount, policy.RequiredApprovals, strings.Join(approvedBy, ", "))
	default:
		res.Reason = deliveryv1alpha1.ReasonAwaitingApprovals
		res.Message = fmt.Sprintf("%d of %d required approvals", status.ApprovedCount, policy.RequiredApprovals)
	}
	return res
}

// IsSealed reports whether the votes of prep are locked.
func IsSealed(prep *deliveryv1alpha1.Preparation) bool {
	return sealedStatus(prep) != nil
}

func sealedStatus(prep *deliveryv1alpha1.Preparation) *deliveryv1alpha1.PreparationApprovalStatus {
	if prep.Status.Approval == nil || prep.Status.Approval.SealedTime == nil {
		return nil
	}
	return prep.Status.Approval
}

// evaluateOpen picks the latest vote per approver from the sorted approvals.
func evaluateOpen(policy *deliveryv1alpha1.ApprovalPolicy, sorted []*deliveryv1alpha1.Approval, verdicts map[string]Verdict) []deliveryv1alpha1.ApprovalVote {
	latest := map[string]*deliveryv1alpha1.Approval{}
	var order []string
	for _, a := range sorted {
		key := approverKey(a.Spec.Approver.Issuer, a.Spec.Approver.Subject)
		if prev, ok := latest[key]; ok {
			verdicts[prev.Name] = Verdict{
				Reason:  deliveryv1alpha1.ReasonSuperseded,
				Message: "Superseded by " + a.Name,
			}
		} else {
			order = append(order, key)
		}
		latest[key] = a
	}

	votes := make([]deliveryv1alpha1.ApprovalVote, 0, len(order))
	for _, key := range order {
		a := latest[key]
		result := deliveryv1alpha1.VoteResultCounted
		verdict := Verdict{Counted: true, Reason: deliveryv1alpha1.ReasonCounted, Message: "The vote counts towards the approval gate"}
		if !eligible(policy, a.Spec.Approver.Groups) {
			result = deliveryv1alpha1.VoteResultNotEligible
			verdict = Verdict{Reason: deliveryv1alpha1.ReasonNotEligible, Message: "The approver is not a member of any allowed group"}
		}
		verdicts[a.Name] = verdict
		votes = append(votes, deliveryv1alpha1.ApprovalVote{
			ApprovalName:  a.Name,
			Issuer:        a.Spec.Approver.Issuer,
			Subject:       a.Spec.Approver.Subject,
			Username:      a.Spec.Approver.Username,
			Decision:      a.Spec.Decision,
			Result:        result,
			SubmittedTime: a.Spec.SubmittedTime,
		})
	}
	if len(votes) > MaxApprovers {
		votes = votes[:MaxApprovers]
	}
	return votes
}

// evaluateSealed classifies approvals against the sealed votes.
func evaluateSealed(sorted []*deliveryv1alpha1.Approval, votes []deliveryv1alpha1.ApprovalVote, verdicts map[string]Verdict) {
	sealedByName := make(map[string]deliveryv1alpha1.ApprovalVote, len(votes))
	sealedByKey := make(map[string]*deliveryv1alpha1.Approval, len(votes))
	for _, v := range votes {
		sealedByName[v.ApprovalName] = v
	}
	for _, a := range sorted {
		if _, ok := sealedByName[a.Name]; ok {
			sealedByKey[approverKey(a.Spec.Approver.Issuer, a.Spec.Approver.Subject)] = a
		}
	}

	for _, a := range sorted {
		if v, ok := sealedByName[a.Name]; ok {
			if v.Result == deliveryv1alpha1.VoteResultCounted {
				verdicts[a.Name] = Verdict{Counted: true, Reason: deliveryv1alpha1.ReasonCounted, Message: "The vote is part of the sealed approvals"}
			} else {
				verdicts[a.Name] = Verdict{Reason: deliveryv1alpha1.ReasonNotEligible, Message: "The approver is not a member of any allowed group"}
			}
			continue
		}
		if s, ok := sealedByKey[approverKey(a.Spec.Approver.Issuer, a.Spec.Approver.Subject)]; ok && compareApprovals(a, s) < 0 {
			verdicts[a.Name] = Verdict{Reason: deliveryv1alpha1.ReasonSuperseded, Message: "Superseded by " + s.Name}
			continue
		}
		verdicts[a.Name] = Verdict{
			Reason:  deliveryv1alpha1.ReasonSubmittedAfterSeal,
			Message: "The vote was submitted after the approvals were sealed and is ignored",
		}
	}
}

func eligible(policy *deliveryv1alpha1.ApprovalPolicy, groups []string) bool {
	for _, g := range groups {
		if slices.Contains(policy.AllowedGroups, g) {
			return true
		}
	}
	return false
}

func approverKey(issuer, subject string) string {
	return issuer + "\x00" + subject
}

func displayName(v deliveryv1alpha1.ApprovalVote) string {
	if v.Username != "" {
		return v.Username
	}
	return v.Subject
}

// compareApprovals orders votes by creation time, then submission time, then
// name, giving a total order even for votes within the same second.
func compareApprovals(a, b *deliveryv1alpha1.Approval) int {
	if c := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); c != 0 {
		return c
	}
	if c := a.Spec.SubmittedTime.Compare(b.Spec.SubmittedTime.Time); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}
