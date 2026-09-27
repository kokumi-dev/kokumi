package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/index"
)

const (
	maxApprovalBodyBytes = 16 << 10
	maxApprovalComment   = 1024
	maxNamePrefix        = 200
	approverHashLen      = 10
)

// handleListPreparationApprovals handles GET /api/v1/preparations/{namespace}/{name}/approvals.
// It returns the full, chronologically ordered vote history of a Preparation.
func handleListPreparationApprovals(deps *apiDeps) http.HandlerFunc {
	return listApprovals(deps, index.FieldPreparationRefName)
}

// handleListOrderApprovals handles GET /api/v1/orders/{namespace}/{name}/approvals.
// It includes votes whose Preparation no longer exists.
func handleListOrderApprovals(deps *apiDeps) http.HandlerFunc {
	return listApprovals(deps, index.FieldOrderName)
}

func listApprovals(deps *apiDeps, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}

		list := &deliveryv1alpha1.ApprovalList{}
		if err := uc.list(r.Context(), list,
			client.InNamespace(r.PathValue("namespace")),
			client.MatchingFields{field: r.PathValue("name")},
		); err != nil {
			respondForbiddenOrError(w, err, "failed to list approvals")
			return
		}

		slices.SortFunc(list.Items, func(a, b deliveryv1alpha1.Approval) int {
			if c := a.CreationTimestamp.Compare(b.CreationTimestamp.Time); c != 0 {
				return c
			}
			if c := a.Spec.SubmittedTime.Compare(b.Spec.SubmittedTime.Time); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})

		out := make([]ApprovalDTO, len(list.Items))
		for i := range list.Items {
			out[i] = approvalToDTO(list.Items[i])
		}
		respondJSON(w, http.StatusOK, out)
	}
}

// handleSubmitApproval handles POST /api/v1/preparations/{namespace}/{name}/approvals.
// The approver is taken exclusively from the verified OIDC session; the
// request body only carries the decision and an optional comment. The
// Approval is created with the server's own identity, which is the only
// identity the approval-integrity admission policy accepts.
func handleSubmitApproval(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil || deps.approvalWriter == nil {
			unavailable(w)
			return
		}

		namespace := r.PathValue("namespace")
		name := r.PathValue("name")

		id := identityFromRequest(r)
		if id == nil {
			respondError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if id.Provider != providerOIDC || id.Issuer == "" {
			respondError(w, http.StatusForbidden, "approvals require a personal OIDC login; the shared admin account cannot approve")
			return
		}

		var req SubmitApprovalRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxApprovalBodyBytes)).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}
		decision := deliveryv1alpha1.ApprovalDecision(req.Decision)
		if decision != deliveryv1alpha1.ApprovalDecisionApprove && decision != deliveryv1alpha1.ApprovalDecisionReject {
			respondError(w, http.StatusBadRequest, "decision must be Approve or Reject")
			return
		}
		if utf8.RuneCountInString(req.Comment) > maxApprovalComment {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("comment must not exceed %d characters", maxApprovalComment))
			return
		}

		uc, err := deps.resolveUserClient(r)
		if err != nil {
			respondForbiddenOrError(w, err, "failed to resolve identity")
			return
		}
		allowed, err := uc.authorized(r.Context(), "approve", "preparations", namespace, name)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to check approval permission")
			return
		}
		if !allowed {
			respondError(w, http.StatusForbidden, fmt.Sprintf("not authorized to approve preparation %s/%s", namespace, name))
			return
		}

		prep := &deliveryv1alpha1.Preparation{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, prep); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("preparation %s/%s not found", namespace, name))
				return
			}
			respondForbiddenOrError(w, err, "failed to get preparation")
			return
		}
		policy := prep.Spec.ApprovalPolicy
		switch {
		case policy == nil:
			respondError(w, http.StatusConflict, "preparation does not require approval")
			return
		case !apimeta.IsStatusConditionTrue(prep.Status.Conditions, deliveryv1alpha1.ConditionTypeReady):
			respondError(w, http.StatusConflict, "preparation is not ready")
			return
		case approval.IsSealed(prep):
			respondError(w, http.StatusConflict, "approvals of this preparation are sealed because it was promoted")
			return
		}

		existing := &deliveryv1alpha1.ApprovalList{}
		if err := deps.approvalWriter.List(r.Context(), existing,
			client.InNamespace(namespace),
			client.MatchingFields{index.FieldPreparationRefName: name},
		); err != nil {
			respondError(w, http.StatusInternalServerError, "failed to list existing approvals")
			return
		}
		if msg := rejectVote(prep, existing.Items, id, decision); msg != "" {
			respondError(w, http.StatusConflict, msg)
			return
		}

		approval := newApproval(prep, existing.Items, id, decision, req.Comment)
		if err := deps.approvalWriter.Create(r.Context(), approval); err != nil {
			if apierrors.IsAlreadyExists(err) {
				respondError(w, http.StatusConflict, "another vote from you was recorded at the same time; reload to see it")
				return
			}
			deps.logger.Error(err, "Failed to create Approval", "namespace", namespace, "preparation", name)
			if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
				respondError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			respondError(w, http.StatusInternalServerError, "failed to submit approval")
			return
		}

		deps.logger.Info("Submitted Approval", "namespace", namespace, "preparation", name,
			"approval", approval.Name, "subject", id.Subject, "decision", decision)
		respondJSON(w, http.StatusCreated, approvalToDTO(*approval))
	}
}

// rejectVote returns a reason when the vote must not be recorded: a repeat of
// the approver's current decision, or a new approver beyond the bounded
// number of approvers per Preparation.
func rejectVote(prep *deliveryv1alpha1.Preparation, existing []deliveryv1alpha1.Approval, id *Identity, decision deliveryv1alpha1.ApprovalDecision) string {
	res := approval.Evaluate(prep, existing)
	for _, v := range res.Status.Votes {
		if v.Issuer == id.Issuer && v.Subject == id.Subject {
			if v.Decision == decision {
				return fmt.Sprintf("your current vote is already %s", decision)
			}
			return ""
		}
	}
	if len(res.Status.Votes) >= approval.MaxApprovers {
		return fmt.Sprintf("preparation already has the maximum of %d approvers", approval.MaxApprovers)
	}
	return ""
}

// newApproval builds the Approval for a verified identity. Only groups
// relevant to the policy are recorded to keep the record minimal.
//
// The name is derived from the Preparation, the approver and the number of
// votes the approver already cast on it. Concurrent submissions of the same
// approver therefore compete for the same name and the API server admits only
// one of them.
func newApproval(prep *deliveryv1alpha1.Preparation, existing []deliveryv1alpha1.Approval, id *Identity, decision deliveryv1alpha1.ApprovalDecision, comment string) *deliveryv1alpha1.Approval {
	var groups []string
	for _, g := range id.Groups {
		if slices.Contains(prep.Spec.ApprovalPolicy.AllowedGroups, g) && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}

	seq := 0
	for i := range existing {
		if existing[i].Spec.Approver.Issuer == id.Issuer && existing[i].Spec.Approver.Subject == id.Subject {
			seq++
		}
	}
	sum := sha256.Sum256([]byte(id.Issuer + "\n" + id.Subject))

	prefix := prep.Name
	if len(prefix) > maxNamePrefix {
		prefix = prefix[:maxNamePrefix]
	}

	return &deliveryv1alpha1.Approval{
		Name:      strings.TrimRight(prefix, "-.") + "-" + hex.EncodeToString(sum[:])[:approverHashLen] + "-" + strconv.Itoa(seq),
		Namespace: prep.Namespace,
		Spec: deliveryv1alpha1.ApprovalSpec{
			OrderName: prep.Spec.OrderName,
			PreparationRef: deliveryv1alpha1.ApprovalPreparationReference{
				Name:           prep.Name,
				UID:            prep.UID,
				ArtifactDigest: prep.Spec.Artifact.Digest,
			},
			Approver: deliveryv1alpha1.Approver{
				Issuer:   id.Issuer,
				Subject:  id.Subject,
				Username: id.Username,
				Email:    id.Email,
				Groups:   groups,
			},
			Decision:      decision,
			Comment:       comment,
			SubmittedTime: metav1.NewMicroTime(time.Now()),
		},
	}
}
