package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/service"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PreviewOrderRequest is the body for POST /api/v1/orders/preview.
// It mirrors CreateOrderRequest but omits destination and commit message
// since no artifact is pushed.
type PreviewOrderRequest struct {
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Source    OCISourceDTO `json:"source"`
	MenuRef   *MenuRefDTO  `json:"menuRef,omitempty"`
	Render    *RenderDTO   `json:"render,omitempty"`
	Patches   []PatchDTO   `json:"patches,omitempty"`
	Edits     []PatchDTO   `json:"edits,omitempty"`
}

// handlePreviewOrder handles POST /api/v1/orders/preview.
// It resolves the effective spec, pulls and renders the source artifact,
// applies patches/edits, and returns the resulting manifest as text/plain.
// Nothing is pushed to a registry.
func handlePreviewOrder(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		var req PreviewOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
			return
		}

		name := req.Name
		namespace := req.Namespace
		if namespace == "" {
			namespace = "default"
		}

		order := &deliveryv1alpha1.Order{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: deliveryv1alpha1.OrderSpec{
				Render:  renderFromDTO(req.Render),
				Patches: patchesFromDTO(req.Patches),
				Edits:   patchesFromDTO(req.Edits),
			},
		}

		if req.Source.OCI != "" {
			order.Spec.Source = &deliveryv1alpha1.OCISource{
				OCI:     req.Source.OCI,
				Version: req.Source.Version,
			}
		}

		if req.MenuRef != nil {
			order.Spec.MenuRef = &deliveryv1alpha1.MenuRef{Name: req.MenuRef.Name}
		}

		var spec *resolve.EffectiveSpec
		var specErr error

		if req.MenuRef != nil {
			menu := &deliveryv1alpha1.Menu{}
			if err := deps.reader.Get(r.Context(), types.NamespacedName{Name: req.MenuRef.Name}, menu); err != nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %q not found", req.MenuRef.Name))
				return
			}
			spec, specErr = resolve.ForMenu(menu, order)
		} else {
			spec, specErr = resolve.FromOrder(order)
		}

		if specErr != nil {
			respondError(w, http.StatusUnprocessableEntity, specErr.Error())
			return
		}

		svc := service.NewOrderService(deps.ociClient, deps.fs, "")

		manifest, err := svc.PreviewOrder(
			r.Context(),
			spec.Source,
			spec.Render,
			spec.Patches,
			spec.Edits,
			name,
			namespace,
		)
		if err != nil {
			deps.logger.Error(err, "Failed to preview Order")
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to render preview: %s", err))
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifest)
	}
}
