package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/renderer"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreviewOrderRequest is the body for POST /api/v1/orders/preview.
// It mirrors CreateOrderRequest but omits promotion, commit message and
// destination since no artifact is pushed.
type PreviewOrderRequest struct {
	Namespace       string          `json:"namespace"`
	Name            string          `json:"name"`
	ProductRef      ProductRefDTO   `json:"productRef"`
	TargetNamespace string          `json:"targetNamespace,omitempty"`
	Values          json.RawMessage `json:"values,omitempty"`
	Patches         []PatchDTO      `json:"patches,omitempty"`
	Edits           []PatchDTO      `json:"edits,omitempty"`
}

// PreviewProductRequest is the body for POST /api/v1/products/preview.
// It renders a Product spec, saved or not, without any Order on top.
type PreviewProductRequest struct {
	Namespace       string             `json:"namespace"`
	Name            string             `json:"name"`
	Source          OCISourceDTO       `json:"source"`
	Vendor          *OCIDestinationDTO `json:"vendor,omitempty"`
	Render          *RenderDTO         `json:"render,omitempty"`
	TargetNamespace string             `json:"targetNamespace,omitempty"`
	Patches         []PatchDTO         `json:"patches,omitempty"`
	Images          ProductImagesDTO   `json:"images"`
	RegistryRefs    []RegistryRefDTO   `json:"registryRefs,omitempty"`
}

// preview is a resolved preview request, ready to render.
type preview struct {
	spec      *resolve.EffectiveSpec
	name      string
	namespace string
	// scope connects to the source and the images through the Product's
	// Registries, as the user.
	scope   registry.Scope
	mirrors []images.Mirror
	// locked are the images of the Order's latest Preparation, whose
	// digests Pin and Vendor keep; empty for a Product.
	locked []images.Image
}

// resolvePreviewFunc decodes a preview request and resolves what to render.
// It writes the HTTP error response itself and returns false when the request
// should not proceed.
type resolvePreviewFunc func(deps *apiDeps, w http.ResponseWriter, r *http.Request) (*preview, bool)

// handlePreviewOrder handles POST /api/v1/orders/preview.
// It merges the Order over its Product, pulls and renders the source
// artifact, applies patches and edits, and returns the rendered files.
// Nothing is pushed to a registry.
func handlePreviewOrder(deps *apiDeps) http.HandlerFunc {
	return handlePreview(deps, resolveOrderPreview)
}

// handlePreviewProduct handles POST /api/v1/products/preview. It renders a
// Product on its own and returns the rendered files.
func handlePreviewProduct(deps *apiDeps) http.HandlerFunc {
	return handlePreview(deps, resolveProductPreview)
}

// handlePreview renders a preview and returns its files in the source
// artifact's layout. A single rendered manifest is one file, manifest.yaml.
func handlePreview(deps *apiDeps, resolvePreview resolvePreviewFunc) http.HandlerFunc {
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
			if isDenied(err) || registry.Reason(err, "") != "" {
				respondConnectError(w, err)
				return
			}
			// The request asks for a patch or edit the source cannot take.
			if _, ok := errors.AsType[*renderer.PatchError](err); ok {
				respondError(w, http.StatusUnprocessableEntity, err.Error())
				return
			}
			deps.logger.Error(err, "Failed to render preview", "namespace", p.namespace, "name", p.name)
			respondError(w, http.StatusBadGateway, fmt.Sprintf("failed to render preview: %s", err))
			return
		}

		respondJSON(w, http.StatusOK, artifactFilesToDTO(files))
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

	imageOpts := converted.Images
	imageOpts.VendorURL = p.spec.ImagesVendorURL
	imageOpts.Mirrors = p.mirrors
	imageOpts.Locked = p.locked

	return artifact.PreviewRequest{
		Source:    converted.Source,
		Product:   p.scope,
		Render:    converted.Render,
		Patches:   converted.Patches,
		Edits:     converted.Edits,
		Images:    imageOpts,
		Name:      p.name,
		Namespace: p.spec.TargetNamespace,
	}, nil
}

// resolveOrderPreview builds the Order from the request and merges it over
// its Product.
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
			ProductRef:      deliveryv1alpha1.ProductRef{Name: req.ProductRef.Name},
			TargetNamespace: req.TargetNamespace,
			Values:          valuesFromDTO(req.Values),
			Patches:         patchesFromDTO(req.Patches),
			Edits:           patchesFromDTO(req.Edits),
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

	p, ok := resolvePreviewSource(deps, w, r, spec, req.Name, ns)
	if !ok {
		return nil, false
	}
	p.locked = lockedImagesAs(r.Context(), deps, uc, spec.Images, ns, req.Name)

	return p, true
}

// lockedImagesAs returns the images of the named Order's latest Preparation,
// read with the user's permissions, so that a preview under Pin or Vendor
// keeps the digests the render keeps. A new Order, or one the user cannot
// read, locks nothing.
func lockedImagesAs(ctx context.Context, deps *apiDeps, uc *userClient, spec deliveryv1alpha1.ProductImages, ns, name string) []images.Image {
	if spec.EffectivePolicy() == deliveryv1alpha1.ImagePolicyRecord {
		return nil
	}

	order := &deliveryv1alpha1.Order{}
	if err := uc.get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order); err != nil {
		if client.IgnoreNotFound(err) != nil {
			deps.logger.V(1).Info("Previewing images without the Order's locked digests", "namespace", ns, "order", name, "reason", err.Error())
		}
		return nil
	}
	if order.Status.LatestPreparationName == "" {
		return nil
	}

	preparation := &deliveryv1alpha1.Preparation{}
	if err := uc.get(ctx, types.NamespacedName{Namespace: ns, Name: order.Status.LatestPreparationName}, preparation); err != nil {
		deps.logger.V(1).Info("Previewing images without the Order's locked digests", "namespace", ns, "order", name, "reason", err.Error())
		return nil
	}
	return resolve.LockedImages(preparation)
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

	spec := &resolve.EffectiveSpec{
		Source:       deliveryv1alpha1.OCISource{URL: req.Source.URL, Version: req.Source.Version},
		Render:       renderFromDTO(req.Render),
		Patches:      patchesFromDTO(req.Patches),
		Images:       productImagesFromDTO(req.Images),
		RegistryRefs: registryRefsFromDTO(req.RegistryRefs),
		// Without an Order, the Product deploys into its own namespace,
		// unless it sets one.
		TargetNamespace: resolve.TargetNamespace(
			&deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{TargetNamespace: req.TargetNamespace}},
			&deliveryv1alpha1.Order{Namespace: ns}),
	}
	if spec.Images.EffectivePolicy() == deliveryv1alpha1.ImagePolicyVendor {
		spec.ImagesVendorURL = artifact.DefaultVendorDestination(ns, req.Name)
		if req.Vendor != nil && req.Vendor.URL != "" {
			spec.ImagesVendorURL = req.Vendor.URL
		}
	}

	return resolvePreviewSource(deps, w, r, spec, req.Name, ns)
}

// resolvePreviewSource prepares the preview of spec: it connects to the
// source and the images through the Product's Registries as the user, and
// resolves the images through the Kitchen's mirrors.
func resolvePreviewSource(deps *apiDeps, w http.ResponseWriter, r *http.Request, spec *resolve.EffectiveSpec, name, ns string) (*preview, bool) {
	scope, err := deps.scopeFor(r, ns, spec.RegistryRefs)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve identity")
		return nil, false
	}

	p := &preview{spec: spec, name: name, namespace: ns, scope: scope}
	if kitchen, err := deps.kitchen(); err == nil {
		p.mirrors = resolve.ImageMirrors(kitchen)
	}

	return p, true
}
