package server

import (
	"cmp"
	"encoding/json"
	"net/http"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/scmlink"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// errorResponse is the JSON body for all 4xx/5xx responses.
type errorResponse struct {
	Error string `json:"error"`
}

// respondJSON serialises v as JSON, sets Content-Type, and writes status.
// Any marshalling error results in a 500 with a plain-text body instead.
func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

// respondError writes an errorResponse as JSON.
func respondError(w http.ResponseWriter, status int, msg string) {
	respondJSON(w, status, errorResponse{Error: msg})
}

// unavailable is returned when apiDeps is nil (no k8s config found).
func unavailable(w http.ResponseWriter) {
	respondError(w, http.StatusServiceUnavailable, "Kubernetes client not available")
}

// conditionsToDTO converts a slice of metav1.Condition to ConditionDTO.
func conditionsToDTO(conditions []metav1.Condition) []ConditionDTO {
	if len(conditions) == 0 {
		return nil
	}
	out := make([]ConditionDTO, len(conditions))
	for i, c := range conditions {
		out[i] = ConditionDTO{
			Type:               c.Type,
			Status:             string(c.Status),
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: c.LastTransitionTime.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return out
}

// UI states that are not the reason of a Ready condition.
const (
	stateFailed   = "Failed"
	stateWaiting  = "Waiting"
	stateDeleting = "Deleting"
)

// waitingReasons are the Ready reasons of an Order that waits for its
// Product rather than having failed.
var waitingReasons = []string{
	deliveryv1alpha1.ReasonProductNotFound,
	deliveryv1alpha1.ReasonProductNotReady,
	deliveryv1alpha1.ReasonProductDeleting,
}

// stateFromConditions derives a UI-friendly state string from a conditions slice.
func stateFromConditions(conditions []metav1.Condition) string {
	for _, c := range conditions {
		if c.Type == deliveryv1alpha1.ConditionTypeReady {
			if c.Status == metav1.ConditionFalse {
				if slices.Contains(waitingReasons, c.Reason) {
					return stateWaiting
				}
				return stateFailed
			}
			return c.Reason
		}
	}
	return "Unknown"
}

// orderToDTO converts a Order CRD object and the name of its currently active
// Preparation (from the linked Serving) into a OrderDTO.
func orderToDTO(r deliveryv1alpha1.Order, activePreparation string) OrderDTO {
	dto := OrderDTO{
		Name:       r.Name,
		Namespace:  r.Namespace,
		Labels:     r.Labels,
		ProductRef: ProductRefDTO{Name: r.Spec.ProductRef.Name},
		Destination: func() OCIDestinationDTO {
			if d := destinationToDTO(r.Spec.Destination); d != nil {
				return *d
			}
			return OCIDestinationDTO{}
		}(),
		EffectiveDestination: func() string {
			if r.Spec.Destination == nil {
				return artifact.DefaultDestination(r.Namespace, r.Name)
			}
			if r.Spec.Destination.OCI != "" {
				return r.Spec.Destination.OCI
			}
			if r.Spec.Destination.PantryRef != nil {
				// URL is resolved at runtime from the Pantry; omit from DTO.
				return ""
			}
			return artifact.DefaultDestination(r.Namespace, r.Name)
		}(),
		Patches:           patchesToDTO(r.Spec.Patches),
		Edits:             patchesToDTO(r.Spec.Edits),
		Mode:              string(r.EffectivePromotionMode()),
		Approvals:         approvalPolicyToDTO(r.ApprovalPolicy()),
		State:             stateFromConditions(r.Status.Conditions),
		LatestRevision:    r.Status.LatestPreparationName,
		ActivePreparation: activePreparation,
		Conditions:        conditionsToDTO(r.Status.Conditions),
	}

	if r.Spec.Values != nil {
		dto.Values = r.Spec.Values.Raw
	}

	if !r.CreationTimestamp.IsZero() {
		t := r.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}
	return dto
}

// renderToDTO converts a Render CRD spec into a RenderDTO, returning nil when r is nil.
func renderToDTO(r *deliveryv1alpha1.Render) *RenderDTO {
	if r == nil {
		return nil
	}

	var dto RenderDTO

	if h := r.Helm; h != nil {
		var vals json.RawMessage
		if h.Values != nil {
			vals = h.Values.Raw
		}
		dto.Helm = &HelmRenderDTO{
			ReleaseName: h.ReleaseName,
			Namespace:   h.Namespace,
			IncludeCRDs: h.IncludeCRDs,
			Values:      vals,
		}
	}

	if m := r.Manifest; m != nil {
		dto.Manifest = &ManifestRenderDTO{
			Layout: string(m.Layout),
		}
	}

	if dto.Helm == nil && dto.Manifest == nil {
		return nil
	}

	return &dto
}

// renderFromDTO converts a RenderDTO (from a request body) into a Render CRD spec.
// Returns nil when dto is nil or contains no helm or manifest block.
func renderFromDTO(dto *RenderDTO) *deliveryv1alpha1.Render {
	if dto == nil {
		return nil
	}

	var r deliveryv1alpha1.Render

	if h := dto.Helm; h != nil {
		var vals *apiextensionsv1.JSON
		if len(h.Values) > 0 {
			vals = &apiextensionsv1.JSON{Raw: h.Values}
		}
		r.Helm = &deliveryv1alpha1.HelmRender{
			ReleaseName: h.ReleaseName,
			Namespace:   h.Namespace,
			IncludeCRDs: h.IncludeCRDs,
			Values:      vals,
		}
	}

	if m := dto.Manifest; m != nil {
		r.Manifest = &deliveryv1alpha1.ManifestRender{
			Layout: deliveryv1alpha1.FileLayout(m.Layout),
		}
	}

	if r.Helm == nil && r.Manifest == nil {
		return nil
	}

	return &r
}

// preparationToDTO converts a Preparation CRD object into a PreparationDTO.
// isActive is true when this Preparation is the one currently deployed by its
// Order's Serving.
func preparationToDTO(p deliveryv1alpha1.Preparation, isActive bool) PreparationDTO {
	dto := PreparationDTO{
		Name:      p.Name,
		Namespace: p.Namespace,
		Order:     p.Spec.OrderName,
		Artifact: ArtifactDTO{
			OCIRef: p.Spec.Artifact.OCIRef,
			Digest: p.Spec.Artifact.Digest,
			Signed: p.Spec.Artifact.Signed,
		},
		ConfigHash:    p.Spec.ConfigHash,
		State:         stateFromConditions(p.Status.Conditions),
		IsActive:      isActive,
		CommitMessage: p.Spec.CommitMessage,
		ParentDigest:  p.Spec.ParentDigest,
		GitSource: GitSourceDTO{
			Repo:       p.Spec.GitSource.Repo,
			Tag:        p.Spec.GitSource.Tag,
			CommitHash: p.Spec.GitSource.CommitHash,
			SourceLink: toSourceLinkDTO(scmlink.Build(p.Spec.GitSource.Repo, p.Spec.GitSource.Tag, p.Spec.GitSource.CommitHash)),
		},
		Renderer: RendererDTO{
			Type:    string(p.Spec.Renderer.Type),
			Version: p.Spec.Renderer.Version,
			Builder: p.Spec.Renderer.Builder,
		},
		Conditions: conditionsToDTO(p.Status.Conditions),
		Approval:   preparationApprovalToDTO(p),
	}
	if !p.CreationTimestamp.IsZero() {
		t := p.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}
	return dto
}

// approvalPolicyToDTO converts an ApprovalPolicy; nil stays nil.
func approvalPolicyToDTO(p *deliveryv1alpha1.ApprovalPolicy) *ApprovalPolicyDTO {
	if p == nil {
		return nil
	}
	return &ApprovalPolicyDTO{RequiredApprovals: p.RequiredApprovals, AllowedGroups: p.AllowedGroups}
}

// approvalPolicyFromDTO converts a request policy; nil disables the gate.
func approvalPolicyFromDTO(dto *ApprovalPolicyDTO) *deliveryv1alpha1.ApprovalPolicy {
	if dto == nil {
		return nil
	}
	return &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: dto.RequiredApprovals, AllowedGroups: dto.AllowedGroups}
}

// preparationApprovalToDTO summarizes the approval gate from the
// controller-owned Preparation status.
func preparationApprovalToDTO(p deliveryv1alpha1.Preparation) *PreparationApprovalDTO {
	if p.Spec.ApprovalPolicy == nil {
		return nil
	}
	dto := &PreparationApprovalDTO{
		Policy:            *approvalPolicyToDTO(p.Spec.ApprovalPolicy),
		State:             deliveryv1alpha1.ReasonAwaitingApprovals,
		RequiredApprovals: p.Spec.ApprovalPolicy.RequiredApprovals,
	}
	if c := apimeta.FindStatusCondition(p.Status.Conditions, deliveryv1alpha1.ConditionTypeApproved); c != nil {
		dto.Approved = c.Status == metav1.ConditionTrue
		dto.State = c.Reason
		dto.Message = c.Message
	}
	s := p.Status.Approval
	if s == nil {
		return dto
	}
	dto.ApprovedCount = s.ApprovedCount
	dto.RejectedCount = s.RejectedCount
	dto.IneligibleCount = s.IneligibleCount
	dto.SubmissionCount = s.SubmissionCount
	for _, v := range s.Votes {
		dto.Votes = append(dto.Votes, ApprovalVoteDTO{
			ApprovalName: v.ApprovalName,
			Username:     v.Username,
			Subject:      v.Subject,
			Decision:     string(v.Decision),
			Result:       string(v.Result),
			SubmittedAt:  v.SubmittedTime.UTC(),
		})
	}
	if s.SealedTime != nil {
		t := s.SealedTime.UTC()
		dto.SealedAt = &t
	}
	if s.Attestation != nil {
		dto.Attestation = s.Attestation.OCIRef
	}
	return dto
}

// approvalToDTO converts an Approval CRD object into an ApprovalDTO.
func approvalToDTO(a deliveryv1alpha1.Approval) ApprovalDTO {
	dto := ApprovalDTO{
		Name:           a.Name,
		Namespace:      a.Namespace,
		Order:          a.Spec.OrderName,
		Preparation:    a.Spec.PreparationRef.Name,
		ArtifactDigest: a.Spec.PreparationRef.ArtifactDigest,
		Approver: ApproverDTO{
			Issuer:   a.Spec.Approver.Issuer,
			Subject:  a.Spec.Approver.Subject,
			Username: a.Spec.Approver.Username,
			Email:    a.Spec.Approver.Email,
			Groups:   a.Spec.Approver.Groups,
		},
		Decision:    string(a.Spec.Decision),
		Comment:     a.Spec.Comment,
		SubmittedAt: a.Spec.SubmittedTime.UTC(),
	}
	if c := apimeta.FindStatusCondition(a.Status.Conditions, deliveryv1alpha1.ConditionTypeCounted); c != nil {
		dto.Counted = c.Status == metav1.ConditionTrue
		dto.Reason = c.Reason
		dto.Message = c.Message
	}
	return dto
}

// toSourceLinkDTO maps the scmlink package result to the API DTO.
func toSourceLinkDTO(l *scmlink.SourceLink) *SourceLinkDTO {
	if l == nil {
		return nil
	}
	return &SourceLinkDTO{URL: l.URL, Label: l.Label}
}

// patchesFromDTO converts a slice of PatchDTO (from a request body) to the
// equivalent CRD patch type. Used by both the create and update order handlers.
func patchesFromDTO(dtos []PatchDTO) []deliveryv1alpha1.Patch {
	patches := make([]deliveryv1alpha1.Patch, len(dtos))
	for i, p := range dtos {
		patches[i] = deliveryv1alpha1.Patch{
			Target: deliveryv1alpha1.PatchTarget{
				Kind:      p.Target.Kind,
				Name:      p.Target.Name,
				Namespace: p.Target.Namespace,
			},
			Set: p.Set,
		}
	}
	return patches
}

// patchesToDTO converts CRD patches to their DTO representation.
func patchesToDTO(patches []deliveryv1alpha1.Patch) []PatchDTO {
	out := make([]PatchDTO, len(patches))
	for i, p := range patches {
		out[i] = PatchDTO{
			Target: PatchTargetDTO{
				Kind:      p.Target.Kind,
				Name:      p.Target.Name,
				Namespace: p.Target.Namespace,
			},
			Set: p.Set,
		}
	}
	return out
}

// valuesFromDTO converts raw Helm values from a request body; empty and null
// values convert to nil.
func valuesFromDTO(raw json.RawMessage) *apiextensionsv1.JSON {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return &apiextensionsv1.JSON{Raw: raw}
}

// pantryRefToDTO converts a PantryRef; nil stays nil.
func pantryRefToDTO(ref *deliveryv1alpha1.PantryRef) *PantryRefDTO {
	if ref == nil {
		return nil
	}
	return &PantryRefDTO{Name: ref.Name}
}

// pantryRefFromDTO converts a PantryRefDTO; nil stays nil.
func pantryRefFromDTO(dto *PantryRefDTO) *deliveryv1alpha1.PantryRef {
	if dto == nil {
		return nil
	}
	return &deliveryv1alpha1.PantryRef{Name: dto.Name}
}

// sourceToDTO converts an OCISource to its DTO representation.
func sourceToDTO(src deliveryv1alpha1.OCISource) OCISourceDTO {
	return OCISourceDTO{OCI: src.OCI, Version: src.Version, PantryRef: pantryRefToDTO(src.PantryRef)}
}

// destinationToDTO converts an OCIDestination; nil stays nil.
func destinationToDTO(dest *deliveryv1alpha1.OCIDestination) *OCIDestinationDTO {
	if dest == nil {
		return nil
	}
	return &OCIDestinationDTO{OCI: dest.OCI, PantryRef: pantryRefToDTO(dest.PantryRef)}
}

// destinationFromDTO converts an OCIDestinationDTO pointer to an OCIDestination spec.
// Returns nil when dto is nil or both oci and pantryRef are absent.
func destinationFromDTO(dto *OCIDestinationDTO) *deliveryv1alpha1.OCIDestination {
	if dto == nil || (dto.OCI == "" && dto.PantryRef == nil) {
		return nil
	}
	return &deliveryv1alpha1.OCIDestination{OCI: dto.OCI, PantryRef: pantryRefFromDTO(dto.PantryRef)}
}

// activePreparationFor returns the observed preparation name for the Order
// identified by namespace/name from the provided list of Servings.
// Returns an empty string when no matching Serving is found.
func activePreparationFor(namespace, orderName string, servings []deliveryv1alpha1.Serving) string {
	for _, s := range servings {
		if s.Namespace == namespace && s.Spec.OrderName == orderName {
			return s.Status.ObservedPreparationName
		}
	}
	return ""
}

// enrichOrders joins a slice of Orders with a slice of Servings to build
// OrderDTOs with ActivePreparation populated.
func enrichOrders(orders []deliveryv1alpha1.Order, servings []deliveryv1alpha1.Serving) []OrderDTO {
	// serving key: "<namespace>/<spec.order>" → serving
	servingMap := make(map[string]*deliveryv1alpha1.Serving, len(servings))
	for i := range servings {
		s := &servings[i]
		servingMap[s.Namespace+"/"+s.Spec.OrderName] = s
	}

	out := make([]OrderDTO, len(orders))
	for i, r := range orders {
		var activePrep string
		if s, ok := servingMap[r.Namespace+"/"+r.Name]; ok {
			activePrep = s.Status.ObservedPreparationName
		}
		out[i] = orderToDTO(r, activePrep)
	}
	return out
}

// enrichPreparations joins Preparations with Servings to determine IsActive.
func enrichPreparations(preps []deliveryv1alpha1.Preparation, servings []deliveryv1alpha1.Serving) []PreparationDTO {
	// active key: "<namespace>/<spec.order>" → observedPreparation name
	activeMap := make(map[string]string, len(servings))
	for _, s := range servings {
		if s.Status.ObservedPreparationName != "" {
			activeMap[s.Namespace+"/"+s.Spec.OrderName] = s.Status.ObservedPreparationName
		}
	}

	out := make([]PreparationDTO, len(preps))
	for i, p := range preps {
		key := p.Namespace + "/" + p.Spec.OrderName
		isActive := activeMap[key] == p.Name
		out[i] = preparationToDTO(p, isActive)
	}

	// Sort newest-first by CreatedAt so the latest preparation is always at the top.
	slices.SortFunc(out, func(a, b PreparationDTO) int {
		if a.CreatedAt == nil {
			return 1
		}
		if b.CreatedAt == nil {
			return -1
		}
		return b.CreatedAt.Compare(*a.CreatedAt)
	})

	return out
}

// servingToDTO converts a Serving CRD object into a ServingDTO.
func servingToDTO(s deliveryv1alpha1.Serving) ServingDTO {
	dto := ServingDTO{
		Name:                s.Name,
		Namespace:           s.Namespace,
		Order:               s.Spec.OrderName,
		DesiredPreparation:  s.Spec.PreparationName,
		TargetPreparation:   s.Status.TargetPreparationName,
		ObservedPreparation: s.Status.ObservedPreparationName,
		DeployedDigest:      s.Status.DeployedDigest,
		State:               stateFromConditions(s.Status.Conditions),
		Conditions:          conditionsToDTO(s.Status.Conditions),
	}
	if !s.CreationTimestamp.IsZero() {
		t := s.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}
	return dto
}

// servingsToDTO converts a slice of Serving CRD objects into ServingDTOs.
func servingsToDTO(servings []deliveryv1alpha1.Serving) []ServingDTO {
	out := make([]ServingDTO, len(servings))
	for i, s := range servings {
		out[i] = servingToDTO(s)
	}
	return out
}

// --- Product Conversions ---

// productToDTO converts a Product CRD object into a ProductDTO.
func productToDTO(p deliveryv1alpha1.Product) ProductDTO {
	dto := ProductDTO{
		Name:       p.Name,
		Namespace:  p.Namespace,
		Source:     sourceToDTO(p.Spec.Source),
		Vendor:     destinationToDTO(p.Spec.Vendor),
		Render:     renderToDTO(p.Spec.Render),
		Patches:    patchesToDTO(p.Spec.Patches),
		Overrides:  overridesToDTO(p.Spec.Overrides),
		State:      stateFromConditions(p.Status.Conditions),
		Conditions: conditionsToDTO(p.Status.Conditions),
	}
	if p.DeletionTimestamp != nil {
		t := p.DeletionTimestamp.UTC()
		dto.DeletionTimestamp = &t
		dto.State = stateDeleting
	}
	if src := p.Status.Source; src != nil {
		dto.ResolvedSource = &ProductSourceStatusDTO{
			OCI:       src.OCI,
			Version:   src.Version,
			PantryRef: pantryRefToDTO(src.PantryRef),
			Digest:    src.Digest,
		}
	}
	if !p.CreationTimestamp.IsZero() {
		t := p.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}
	return dto
}

// productsToDTO converts a slice of Product CRD objects into ProductDTOs.
func productsToDTO(products []deliveryv1alpha1.Product) []ProductDTO {
	out := make([]ProductDTO, len(products))
	for i, p := range products {
		out[i] = productToDTO(p)
	}
	return out
}

// productSpecFromDTO builds a ProductSpec from the fields of a create or
// update request.
func productSpecFromDTO(source OCISourceDTO, vendor *OCIDestinationDTO, render *RenderDTO, patches []PatchDTO, overrides OverridesDTO) deliveryv1alpha1.ProductSpec {
	spec := deliveryv1alpha1.ProductSpec{
		Source:    deliveryv1alpha1.OCISource{OCI: source.OCI, Version: source.Version, PantryRef: pantryRefFromDTO(source.PantryRef)},
		Render:    renderFromDTO(render),
		Patches:   patchesFromDTO(patches),
		Overrides: overridesFromDTO(overrides),
	}
	// An empty vendor destination is valid: the controller vendors to the
	// in-cluster registry.
	if vendor != nil {
		spec.Vendor = &deliveryv1alpha1.OCIDestination{OCI: vendor.OCI, PantryRef: pantryRefFromDTO(vendor.PantryRef)}
	}
	return spec
}

// overridesToDTO converts Overrides to its DTO representation. An unset
// policy is reported as None, which is what it means.
func overridesToDTO(o deliveryv1alpha1.Overrides) OverridesDTO {
	dto := OverridesDTO{
		Values: ValueOverridesDTO{
			Policy: string(cmp.Or(o.Values.Policy, deliveryv1alpha1.OverridePolicyNone)),
			Paths:  o.Values.Paths,
		},
		Patches: PatchOverridesDTO{Policy: string(cmp.Or(o.Patches.Policy, deliveryv1alpha1.OverridePolicyNone))},
	}
	for _, a := range o.Patches.Targets {
		dto.Patches.Targets = append(dto.Patches.Targets, AllowedPatchTargetDTO{
			Target: PatchTargetDTO{Kind: a.Target.Kind, Name: a.Target.Name, Namespace: a.Target.Namespace},
			Paths:  a.Paths,
		})
	}
	return dto
}

// overridesFromDTO converts an OverridesDTO into the CRD type. An empty
// policy is left for the API server to default.
func overridesFromDTO(dto OverridesDTO) deliveryv1alpha1.Overrides {
	o := deliveryv1alpha1.Overrides{
		Values: deliveryv1alpha1.ValueOverrides{
			Policy: deliveryv1alpha1.OverridePolicy(dto.Values.Policy),
			Paths:  dto.Values.Paths,
		},
		Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicy(dto.Patches.Policy)},
	}
	for _, a := range dto.Patches.Targets {
		o.Patches.Targets = append(o.Patches.Targets, deliveryv1alpha1.AllowedPatchTarget{
			Target: deliveryv1alpha1.PatchTarget{Kind: a.Target.Kind, Name: a.Target.Name, Namespace: a.Target.Namespace},
			Paths:  a.Paths,
		})
	}
	return o
}

// pantryToDTO converts a Pantry CRD object into a PantryDTO.
func pantryToDTO(p deliveryv1alpha1.Pantry) PantryDTO {
	dto := PantryDTO{
		Name:         p.Name,
		Namespace:    p.Namespace,
		URL:          p.Spec.URL,
		Description:  p.Spec.Description,
		State:        stateFromConditions(p.Status.Conditions),
		ReferrersAPI: string(p.Status.ReferrersAPI),
		Conditions:   conditionsToDTO(p.Status.Conditions),
	}

	if p.Spec.SecretRef != nil {
		dto.SecretRef = p.Spec.SecretRef.Name
	}

	if !p.CreationTimestamp.IsZero() {
		t := p.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}

	return dto
}

// pantriesFromList converts a PantryList into a slice of PantryDTOs.
func pantriesFromList(list deliveryv1alpha1.PantryList) []PantryDTO {
	out := make([]PantryDTO, len(list.Items))
	for i, p := range list.Items {
		out[i] = pantryToDTO(p)
	}
	return out
}
