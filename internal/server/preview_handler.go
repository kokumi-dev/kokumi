package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreviewOrderRequest is the body for POST /api/v1/orders/preview.
// It mirrors CreateOrderRequest but omits destination, promotion, and commit
// message since no artifact is pushed.
type PreviewOrderRequest struct {
	Namespace  string          `json:"namespace"`
	Name       string          `json:"name"`
	ProductRef ProductRefDTO   `json:"productRef"`
	Values     json.RawMessage `json:"values,omitempty"`
	Patches    []PatchDTO      `json:"patches,omitempty"`
	Edits      []PatchDTO      `json:"edits,omitempty"`
}

// PreviewProductRequest is the body for POST /api/v1/products/preview.
// It renders a Product spec, saved or not, without any Order on top.
type PreviewProductRequest struct {
	Namespace string       `json:"namespace"`
	Name      string       `json:"name"`
	Source    OCISourceDTO `json:"source"`
	Render    *RenderDTO   `json:"render,omitempty"`
	Patches   []PatchDTO   `json:"patches,omitempty"`
}

// preview is a resolved preview request, ready to render.
type preview struct {
	spec         *resolve.EffectiveSpec
	name         string
	namespace    string
	source       deliveryv1alpha1.OCISource
	sourceClient oci.Client
}

// resolvePreviewFunc decodes a preview request and resolves what to render.
// It writes the HTTP error response itself and returns false when the request
// should not proceed.
type resolvePreviewFunc func(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*preview, bool)

// handlePreviewOrder handles POST /api/v1/orders/preview.
// It merges the Order over its Product, pulls and renders the source
// artifact, applies patches and edits, and returns the resulting manifest as
// text/plain. Nothing is pushed to a registry.
func handlePreviewOrder(deps *apiDeps) http.HandlerFunc {
	return handlePreviewManifest(deps, resolveOrderPreview)
}

// handlePreviewOrderFiles handles POST /api/v1/orders/preview/files.
// It runs the same pipeline as the preview endpoint but returns the rendered
// files individually, preserving the source artifact's file layout.
func handlePreviewOrderFiles(deps *apiDeps) http.HandlerFunc {
	return handlePreviewFiles(deps, resolveOrderPreview)
}

// handlePreviewProduct handles POST /api/v1/products/preview. It renders a
// Product on its own and returns the manifest as text/plain.
func handlePreviewProduct(deps *apiDeps) http.HandlerFunc {
	return handlePreviewManifest(deps, resolveProductPreview)
}

// handlePreviewProductFiles handles POST /api/v1/products/preview/files.
func handlePreviewProductFiles(deps *apiDeps) http.HandlerFunc {
	return handlePreviewFiles(deps, resolveProductPreview)
}

// handlePreviewManifest renders a preview into a single manifest.
func handlePreviewManifest(deps *apiDeps, resolvePreview resolvePreviewFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		p, ok := resolvePreview(deps, w, r)
		if !ok {
			return
		}

		req, err := p.request()
		if err != nil {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		manifest, err := deps.pipeline.PreviewManifest(r.Context(), req)
		if err != nil {
			deps.logger.Error(err, "Failed to render preview", "namespace", p.namespace, "name", p.name)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to render preview: %s", err))
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(manifest)
	}
}

// handlePreviewFiles renders a preview into its individual files.
func handlePreviewFiles(deps *apiDeps, resolvePreview resolvePreviewFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps == nil {
			unavailable(w)
			return
		}

		p, ok := resolvePreview(deps, w, r)
		if !ok {
			return
		}

		req, err := p.request()
		if err != nil {
			respondError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}

		files, err := deps.pipeline.Preview(r.Context(), req)
		if err != nil {
			deps.logger.Error(err, "Failed to render preview files", "namespace", p.namespace, "name", p.name)
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

// request converts the preview into a pipeline preview request. It fails on
// render config the pipeline cannot use, such as Helm values that are not an
// object.
func (p *preview) request() (artifact.PreviewRequest, error) {
	converted, err := p.spec.ToArtifactSpec()
	if err != nil {
		return artifact.PreviewRequest{}, fmt.Errorf("invalid render config: %w", err)
	}

	return artifact.PreviewRequest{
		Source:       artifact.Source{OCI: p.source.OCI, Version: p.source.Version, Digest: converted.Source.Digest},
		SourceClient: p.sourceClient,
		Render:       converted.Render,
		Patches:      converted.Patches,
		Edits:        converted.Edits,
		Name:         p.name,
		Namespace:    p.namespace,
	}, nil
}

// resolveOrderPreview builds the Order from the request, merges it over its
// Product, and resolves the source with Pantry credentials when needed.
func resolveOrderPreview(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*preview, bool) {
	var req PreviewOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return nil, false
	}

	ns := req.Namespace
	if ns == "" {
		ns = namespace.Default
	}

	order := &deliveryv1alpha1.Order{
		Name:      req.Name,
		Namespace: ns,
		Spec: deliveryv1alpha1.OrderSpec{
			ProductRef: deliveryv1alpha1.ProductRef{Name: req.ProductRef.Name},
			Values:     valuesFromDTO(req.Values),
			Patches:    patchesFromDTO(req.Patches),
			Edits:      patchesFromDTO(req.Edits),
		},
	}

	uc, err := deps.resolveUserClient(r)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve identity")
		return nil, false
	}

	product := &deliveryv1alpha1.Product{}
	if err := uc.get(r.Context(), types.NamespacedName{Namespace: ns, Name: req.ProductRef.Name}, product); err != nil {
		if client.IgnoreNotFound(err) == nil {
			respondError(w, http.StatusNotFound, fmt.Sprintf("product %q not found in namespace %q", req.ProductRef.Name, ns))
			return nil, false
		}
		respondForbiddenOrError(w, err, "failed to get product")
		return nil, false
	}

	spec, err := resolve.ForProduct(product, order)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, err.Error())
		return nil, false
	}

	return resolvePreviewSource(w, r, uc, spec, req.Name, ns)
}

// resolveProductPreview renders the Product spec from the request as is,
// from its declared source rather than the vendored copy, so that unsaved
// changes can be previewed.
func resolveProductPreview(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*preview, bool) {
	var req PreviewProductRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %s", err))
		return nil, false
	}

	ns := req.Namespace
	if ns == "" {
		ns = namespace.Default
	}

	uc, err := deps.resolveUserClient(r)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve identity")
		return nil, false
	}

	spec := &resolve.EffectiveSpec{
		Source: deliveryv1alpha1.OCISource{
			OCI:       req.Source.OCI,
			Version:   req.Source.Version,
			PantryRef: pantryRefFromDTO(req.Source.PantryRef),
		},
		Render:  renderFromDTO(req.Render),
		Patches: patchesFromDTO(req.Patches),
	}

	return resolvePreviewSource(w, r, uc, spec, req.Name, ns)
}

// resolvePreviewSource resolves the effective source, fetching Pantry
// credentials only when the source references a Pantry (which requires
// get secrets as the user's mapped ServiceAccount). Plain OCI sources pass
// through untouched so users without Secret access can still preview them.
func resolvePreviewSource(w http.ResponseWriter, r *http.Request, uc *userClient, spec *resolve.EffectiveSpec, name, ns string) (*preview, bool) {
	source, sourceClient, err := resolveSourceAs(r.Context(), uc, spec.Source, ns)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve source")
		return nil, false
	}

	return &preview{spec: spec, name: name, namespace: ns, source: source, sourceClient: sourceClient}, true
}

// resolveSourceAs resolves src with the user's permissions.
func resolveSourceAs(ctx context.Context, uc *userClient, src deliveryv1alpha1.OCISource, ns string) (deliveryv1alpha1.OCISource, oci.Client, error) {
	if src.PantryRef == nil {
		return src, nil, nil
	}
	reader, err := uc.readerFor(ctx, "get", secretsResource, ns)
	if err != nil {
		return deliveryv1alpha1.OCISource{}, nil, err
	}
	url, ociClient, err := credential.NewKubeResolver(reader).ResolveSource(ctx, src, ns)
	if err != nil {
		return deliveryv1alpha1.OCISource{}, nil, err
	}
	src.OCI = url
	return src, ociClient, nil
}
