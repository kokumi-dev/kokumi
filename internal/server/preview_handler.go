package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/namespace"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PreviewOrderRequest is the body for POST /api/v1/orders/preview.
// It mirrors CreateOrderRequest but omits promotion and commit message since
// no artifact is pushed. The destination only names where vendored images
// would be pulled from.
type PreviewOrderRequest struct {
	Namespace   string             `json:"namespace"`
	Name        string             `json:"name"`
	ProductRef  ProductRefDTO      `json:"productRef"`
	Values      json.RawMessage    `json:"values,omitempty"`
	Destination *OCIDestinationDTO `json:"destination,omitempty"`
	Patches     []PatchDTO         `json:"patches,omitempty"`
	Edits       []PatchDTO         `json:"edits,omitempty"`
}

// PreviewProductRequest is the body for POST /api/v1/products/preview.
// It renders a Product spec, saved or not, without any Order on top.
type PreviewProductRequest struct {
	Namespace string           `json:"namespace"`
	Name      string           `json:"name"`
	Source    OCISourceDTO     `json:"source"`
	Render    *RenderDTO       `json:"render,omitempty"`
	Patches   []PatchDTO       `json:"patches,omitempty"`
	Images    ProductImagesDTO `json:"images"`
}

// preview is a resolved preview request, ready to render.
type preview struct {
	spec         *resolve.EffectiveSpec
	name         string
	namespace    string
	source       deliveryv1alpha1.OCISource
	sourceClient oci.Client
	// imageClient reads the images' registries; nil reads them anonymously.
	imageClient oci.Client
	mirrors     []images.Mirror
	// destination is the oci:// URL images would be vendored into; empty
	// for a Product, which has none.
	destination string
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
	imageOpts.Client = p.imageClient
	imageOpts.Mirrors = p.mirrors
	imageOpts.Locked = p.locked

	return artifact.PreviewRequest{
		Source:       artifact.Source{OCI: p.source.OCI, Version: p.source.Version, Digest: converted.Source.Digest},
		SourceClient: p.sourceClient,
		Render:       converted.Render,
		Patches:      converted.Patches,
		Edits:        converted.Edits,
		Images:       imageOpts,
		Destination:  p.destination,
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

	p, ok := resolvePreviewSource(deps, w, r, uc, spec, req.Name, ns)
	if !ok {
		return nil, false
	}

	if p.destination, err = previewDestination(r.Context(), uc, req.Destination, ns, req.Name); err != nil {
		respondForbiddenOrError(w, err, "failed to resolve destination")
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

// previewDestination returns the oci:// URL an Order with the given
// destination pushes to, as the Order controller resolves it.
func previewDestination(ctx context.Context, uc *userClient, dest *OCIDestinationDTO, ns, name string) (string, error) {
	if dest == nil || (dest.OCI == "" && dest.PantryRef == nil) {
		return artifact.DefaultDestination(ns, name), nil
	}
	if dest.PantryRef == nil {
		return dest.OCI, nil
	}

	pantry := &deliveryv1alpha1.Pantry{}
	if err := uc.get(ctx, types.NamespacedName{Namespace: ns, Name: dest.PantryRef.Name}, pantry); err != nil {
		return "", err
	}
	return pantry.Spec.URL, nil
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
		Images:  productImagesFromDTO(req.Images),
	}

	return resolvePreviewSource(deps, w, r, uc, spec, req.Name, ns)
}

// resolvePreviewSource resolves the effective source, fetching Pantry
// credentials only when the source references a Pantry (which requires
// get secrets as the user's mapped ServiceAccount). Plain OCI sources pass
// through untouched so users without Secret access can still preview them.
// Images are resolved through the Kitchen's mirrors, with the Product's pull
// Secret when the user may read it.
func resolvePreviewSource(deps *apiDeps, w http.ResponseWriter, r *http.Request, uc *userClient, spec *resolve.EffectiveSpec, name, ns string) (*preview, bool) {
	source, sourceClient, err := resolveSourceAs(r.Context(), uc, spec.Source, ns)
	if err != nil {
		respondForbiddenOrError(w, err, "failed to resolve source")
		return nil, false
	}

	p := &preview{
		spec:         spec,
		name:         name,
		namespace:    ns,
		source:       source,
		sourceClient: sourceClient,
		imageClient:  imageClientAs(r.Context(), deps, uc, spec.Images, ns),
	}
	if kitchen, err := deps.kitchen(); err == nil {
		p.mirrors = resolve.ImageMirrors(kitchen)
	}

	return p, true
}

// imageClientAs returns a client with the credentials of the image pull
// Secret, read with the user's permissions. A preview resolves images on a
// best-effort basis, so a Secret the user cannot read leaves the registries
// anonymous instead of failing the preview.
func imageClientAs(ctx context.Context, deps *apiDeps, uc *userClient, spec deliveryv1alpha1.ProductImages, ns string) oci.Client {
	if spec.PullSecretRef == nil {
		return nil
	}

	reader, err := uc.readerFor(ctx, "get", secretsResource, ns)
	if err != nil {
		deps.logger.V(1).Info("Previewing images without the pull Secret", "namespace", ns, "secret", spec.PullSecretRef.Name, "reason", err.Error())
		return nil
	}

	creds, err := credential.NewKubeResolver(reader).CredentialsForSecret(ctx, ns, spec.PullSecretRef.Name)
	if err != nil {
		deps.logger.V(1).Info("Previewing images without the pull Secret", "namespace", ns, "secret", spec.PullSecretRef.Name, "reason", err.Error())
		return nil
	}

	return oci.NewAuthenticatedORASClient(creds)
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
