package server

import (
	"encoding/json"
	"net/http"
	"sort"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
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

// orderToDTO converts a Order CRD object and the name of its currently active
// Preparation (from the linked Serving) into a OrderDTO.
func orderToDTO(r deliveryv1alpha1.Order, activePreparation string) OrderDTO {
	patches := make([]PatchDTO, len(r.Spec.Patches))
	for i, p := range r.Spec.Patches {
		patches[i] = PatchDTO{
			Target: PatchTargetDTO{
				Kind:      p.Target.Kind,
				Name:      p.Target.Name,
				Namespace: p.Target.Namespace,
			},
			Set: p.Set,
		}
	}

	dto := OrderDTO{
		Name:      r.Name,
		Namespace: r.Namespace,
		Labels:    r.Labels,
		Source: OCISourceDTO{
			OCI:     r.Spec.Source.OCI,
			Version: r.Spec.Source.Version,
		},
		Destination: OCIDestinationDTO{
			OCI: r.Spec.Destination.OCI,
		},
		Render:            renderToDTO(r.Spec.Render),
		Patches:           patches,
		AutoDeploy:        r.Spec.AutoDeploy,
		Phase:             string(r.Status.Phase),
		LatestRevision:    r.Status.LatestRevision,
		ActivePreparation: activePreparation,
		Conditions:        conditionsToDTO(r.Status.Conditions),
	}
	if !r.CreationTimestamp.IsZero() {
		t := r.CreationTimestamp.UTC()
		dto.CreatedAt = &t
	}
	return dto
}

// renderToDTO converts a Render CRD spec into a RenderDTO, returning nil when r is nil.
func renderToDTO(r *deliveryv1alpha1.Render) *RenderDTO {
	if r == nil || r.Helm == nil {
		return nil
	}
	h := r.Helm
	var vals json.RawMessage
	if h.Values != nil {
		vals = h.Values.Raw
	}
	return &RenderDTO{
		Helm: &HelmRenderDTO{
			ReleaseName: h.ReleaseName,
			Namespace:   h.Namespace,
			IncludeCRDs: h.IncludeCRDs,
			Values:      vals,
		},
	}
}

// renderFromDTO converts a RenderDTO (from a request body) into a Render CRD spec.
// Returns nil when dto is nil or contains no helm block.
func renderFromDTO(dto *RenderDTO) *deliveryv1alpha1.Render {
	if dto == nil || dto.Helm == nil {
		return nil
	}
	h := dto.Helm
	var vals *apiextensionsv1.JSON
	if len(h.Values) > 0 {
		vals = &apiextensionsv1.JSON{Raw: h.Values}
	}
	return &deliveryv1alpha1.Render{
		Helm: &deliveryv1alpha1.HelmRender{
			ReleaseName: h.ReleaseName,
			Namespace:   h.Namespace,
			IncludeCRDs: h.IncludeCRDs,
			Values:      vals,
		},
	}
}

// preparationToDTO converts a Preparation CRD object into a PreparationDTO.
// isActive is true when this Preparation is the one currently deployed by its
// Order's Serving.
func preparationToDTO(p deliveryv1alpha1.Preparation, isActive bool) PreparationDTO {
	dto := PreparationDTO{
		Name:      p.Name,
		Namespace: p.Namespace,
		Order:     p.Spec.Order,
		Artifact: ArtifactDTO{
			OCIRef: p.Spec.Artifact.OCIRef,
			Digest: p.Spec.Artifact.Digest,
			Signed: p.Spec.Artifact.Signed,
		},
		ConfigHash: p.Spec.ConfigHash,
		Phase:      string(p.Status.Phase),
		IsActive:   isActive,
		Conditions: conditionsToDTO(p.Status.Conditions),
	}
	if p.Status.CreatedAt != nil && !p.Status.CreatedAt.IsZero() {
		t := p.Status.CreatedAt.UTC()
		dto.CreatedAt = &t
	}
	return dto
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

// activePreparationFor returns the observed preparation name for the Order
// identified by namespace/name from the provided list of Servings.
// Returns an empty string when no matching Serving is found.
func activePreparationFor(namespace, orderName string, servings []deliveryv1alpha1.Serving) string {
	for _, s := range servings {
		if s.Namespace == namespace && s.Spec.Order == orderName {
			return s.Status.ObservedPreparation
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
		servingMap[s.Namespace+"/"+s.Spec.Order] = s
	}

	out := make([]OrderDTO, len(orders))
	for i, r := range orders {
		var activePrep string
		if s, ok := servingMap[r.Namespace+"/"+r.Name]; ok {
			activePrep = s.Status.ObservedPreparation
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
		if s.Status.ObservedPreparation != "" {
			activeMap[s.Namespace+"/"+s.Spec.Order] = s.Status.ObservedPreparation
		}
	}

	out := make([]PreparationDTO, len(preps))
	for i, p := range preps {
		key := p.Namespace + "/" + p.Spec.Order
		isActive := activeMap[key] == p.Name
		out[i] = preparationToDTO(p, isActive)
	}

	// Sort newest-first by CreatedAt so the latest preparation is always at the top.
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == nil {
			return false
		}
		if out[j].CreatedAt == nil {
			return true
		}
		return out[i].CreatedAt.After(*out[j].CreatedAt)
	})

	return out
}

// servingToDTO converts a Serving CRD object into a ServingDTO.
func servingToDTO(s deliveryv1alpha1.Serving) ServingDTO {
	dto := ServingDTO{
		Name:                s.Name,
		Namespace:           s.Namespace,
		Order:               s.Spec.Order,
		DesiredPreparation:  s.Spec.Preparation,
		ObservedPreparation: s.Status.ObservedPreparation,
		DeployedDigest:      s.Status.DeployedDigest,
		PreparationPolicy:   string(s.Spec.PreparationPolicy.Type),
		Phase:               string(s.Status.Phase),
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
