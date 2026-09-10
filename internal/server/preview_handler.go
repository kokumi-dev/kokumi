package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/service"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

		spec, name, ns, resolvedSource, sourceClient, ok := resolvePreviewRequest(deps, w, r)
		if !ok {
			return
		}

		svc := service.NewOrderService(deps.ociClient, deps.fs, "")

		manifest, err := svc.PreviewOrder(
			r.Context(),
			resolvedSource,
			spec.Render,
			spec.Patches,
			spec.Edits,
			name,
			ns,
			sourceClient,
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

// resolvePreviewSource resolves the effective source, fetching Pantry
// credentials only when the source references a Pantry (which requires
// get secrets as the user's mapped ServiceAccount). Plain OCI sources pass
// through untouched so users without Secret access can still preview them.
func resolvePreviewSource(ctx context.Context, uc *userClient, src deliveryv1alpha1.OCISource, ns string) (deliveryv1alpha1.OCISource, oci.Client, error) {
	if src.PantryRef == nil {
		return src, nil, nil
	}
	reader, err := uc.readerFor(ctx, "get", "secrets", ns)
	if err != nil {
		return deliveryv1alpha1.OCISource{}, nil, err
	}
	return credential.NewKubeResolver(reader).ResolveSource(ctx, src, ns)
}

// handlePreviewOrderFiles handles POST /api/v1/orders/preview/files.
// It runs the same pipeline as the preview endpoint but returns the rendered
// files individually, preserving the source artifact's file layout.
func handlePreviewOrderFiles(deps *apiDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		spec, name, ns, resolvedSource, sourceClient, ok := resolvePreviewRequest(deps, w, r)
		if !ok {
			return
		}

		svc := service.NewOrderService(deps.ociClient, deps.fs, "")

		files, err := svc.PreviewFiles(
			r.Context(),
			resolvedSource,
			spec.Render,
			spec.Patches,
			spec.Edits,
			name,
			ns,
			sourceClient,
		)
		if err != nil {
			deps.logger.Error(err, "Failed to preview Order files")
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to render preview: %s", err))
			return
		}

		out := make([]ArtifactFileDTO, 0, len(files))
		for _, f := range files {
			out = append(out, ArtifactFileDTO{Path: f.Path, Content: f.Content})
		}

		respondJSON(w, http.StatusOK, out)
	}
}

// resolvePreviewRequest decodes the request body, builds the Order from the
// DTOs, resolves the effective spec (Menu-merged or plain), and resolves the
// source with Pantry credentials when needed. It writes the HTTP error
// response itself and returns nil when the request should not proceed.
func resolvePreviewRequest(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*resolve.EffectiveSpec, string, string, deliveryv1alpha1.OCISource, oci.Client, bool) {
	var req PreviewOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
	}

	name := req.Name
	ns := req.Namespace
	if ns == "" {
		ns = namespace.Default
	}

	order := &deliveryv1alpha1.Order{
		Name:      name,
		Namespace: ns,
		Spec: deliveryv1alpha1.OrderSpec{
			Render:  renderFromDTO(req.Render),
			Patches: patchesFromDTO(req.Patches),
			Edits:   patchesFromDTO(req.Edits),
		},
	}

	order.Spec.Source = sourceFromDTO(req.Source)

	if req.MenuRef != nil {
		order.Spec.MenuRef = &deliveryv1alpha1.MenuRef{Name: req.MenuRef.Name}
	}

	uc, err := deps.resolveUserClient(r)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve identity")
		return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
	}

	var spec *resolve.EffectiveSpec
	var specErr error

	if req.MenuRef != nil {
		menu := &deliveryv1alpha1.Menu{}
		if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: req.MenuRef.Name}, menu); err != nil {
			if client.IgnoreNotFound(err) == nil {
				respondError(w, http.StatusNotFound, fmt.Sprintf("menu %q not found in namespace %q", req.MenuRef.Name, ns))
				return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
			}
			respondForbiddenOrError(w, err, "failed to get menu")
			return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
		}
		spec, specErr = resolve.ForMenu(menu, order)
	} else {
		spec, specErr = resolve.FromOrder(order)
	}

	if specErr != nil {
		respondError(w, http.StatusUnprocessableEntity, specErr.Error())
		return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
	}

	// Only resolve Pantry credentials (requires get secrets) when the
	// source references a Pantry; plain OCI sources need no Secret access.
	resolvedSource, sourceClient, err := resolvePreviewSource(r.Context(), uc, spec.Source, ns)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve source")
		return nil, "", "", deliveryv1alpha1.OCISource{}, nil, false
	}

	return spec, name, ns, resolvedSource, sourceClient, true
}
