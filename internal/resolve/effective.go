// Package resolve computes the effective rendering spec of an Order by merging
// the Order over its Product.
// It is a pure transform layer: no I/O, no Kubernetes client calls.
package resolve

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/images"
)

// EffectiveSpec holds the resolved source, render config, patches, and edits
// that the Order controller renders.
type EffectiveSpec struct {
	Source deliveryv1alpha1.OCISource
	// SourceDigest pins Source to the digest the Product resolved, so that
	// every variant of the Product renders the same artifact. Empty when
	// rendering a Product spec that was not resolved, as in a preview.
	SourceDigest string
	// SourceVerified records who signed the source, when the Product
	// verified it.
	SourceVerified *deliveryv1alpha1.SourceVerification
	Render         *deliveryv1alpha1.Render
	Patches        []deliveryv1alpha1.Patch
	Edits          []deliveryv1alpha1.Patch
	// Images configures how the images of the rendered manifests are
	// handled. It always comes from the Product.
	Images deliveryv1alpha1.ProductImages
	// ImagesVendorURL is the location the images are vendored below, at
	// <ImagesVendorURL>/images, under the Vendor policy: the Product's
	// vendored source.
	ImagesVendorURL string
	// RegistryRefs are the Product's: they connect to its source, its
	// images and their mirrors, and its vendor location.
	RegistryRefs []corev1.LocalObjectReference
	// Product identifies the Product the spec was merged from.
	Product ProductIdentity
	// ProductRevision is the Product revision the spec renders (see
	// ProductRevision).
	ProductRevision string
	// TargetNamespace is the namespace the Order deploys into (see
	// TargetNamespace).
	TargetNamespace string
}

// ProductIdentity identifies the Product an EffectiveSpec was merged from.
type ProductIdentity struct {
	Name string
	// ConfigHash is the Product's config hash at merge time.
	ConfigHash string
}

// ProductReady reports whether Orders may render from product: its source is
// resolved for the current generation and its Ready condition is True.
func ProductReady(product *deliveryv1alpha1.Product) bool {
	if product.Status.Source == nil {
		return false
	}

	ready := meta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)

	return ready != nil && ready.Status == metav1.ConditionTrue && ready.ObservedGeneration == product.Generation
}

// ForProduct builds the EffectiveSpec of an Order of product.
// The Product's advertised source (status.source) is used as-is and pinned to
// its digest; the Product's render config and patches are merged with the
// Order's values and patches, which must be allowed by the Product's
// overrides.
func ForProduct(product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order) (*EffectiveSpec, error) {
	if product.Status.Source == nil {
		return nil, fmt.Errorf("product %q source is not resolved yet", product.Name)
	}

	render, err := mergeRender(product, order)
	if err != nil {
		return nil, err
	}

	patches, err := mergePatches(product, order)
	if err != nil {
		return nil, err
	}

	if err := validateEdits(product, order); err != nil {
		return nil, err
	}

	revision, err := ProductRevision(product)
	if err != nil {
		return nil, err
	}

	effective := &EffectiveSpec{
		Source: deliveryv1alpha1.OCISource{
			URL:     product.Status.Source.URL,
			Version: product.Status.Source.Version,
		},
		SourceDigest:    product.Status.Source.Digest,
		SourceVerified:  product.Status.Source.Verified,
		Render:          render,
		Patches:         patches,
		Edits:           order.Spec.Edits,
		Images:          *product.Spec.Images.DeepCopy(),
		RegistryRefs:    slices.Clone(product.Spec.RegistryRefs),
		Product:         ProductIdentity{Name: product.Name, ConfigHash: product.Status.ConfigHash},
		ProductRevision: revision,
		TargetNamespace: TargetNamespace(product, order),
	}
	if effective.Images.EffectivePolicy() == deliveryv1alpha1.ImagePolicyVendor {
		effective.ImagesVendorURL = product.Status.Source.URL
	}
	return effective, nil
}

// TargetNamespace returns the namespace order deploys into: its own
// targetNamespace, else its Product's, else the Order's namespace.
func TargetNamespace(product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order) string {
	return cmp.Or(order.Spec.TargetNamespace, product.Spec.TargetNamespace, order.Namespace)
}

// LockedImages returns the images of preparation, whose digests Pin and
// Vendor keep when its Order renders again.
func LockedImages(preparation *deliveryv1alpha1.Preparation) []images.Image {
	if preparation == nil || len(preparation.Spec.Images) == 0 {
		return nil
	}

	locked := make([]images.Image, 0, len(preparation.Spec.Images))
	for _, img := range preparation.Spec.Images {
		locked = append(locked, images.Image{
			Ref:       img.Ref,
			Name:      img.Name,
			Digest:    img.Digest,
			MediaType: img.MediaType,
			Vendored:  img.Vendored,
		})
	}
	return locked
}

// ImageMirrors returns the image mirrors of kitchen, without the oci://
// scheme of their locations.
func ImageMirrors(kitchen *deliveryv1alpha1.Kitchen) []images.Mirror {
	if kitchen == nil || kitchen.Spec.Images == nil {
		return nil
	}

	mirrors := make([]images.Mirror, 0, len(kitchen.Spec.Images.Mirrors))
	for _, m := range kitchen.Spec.Images.Mirrors {
		mirrors = append(mirrors, images.Mirror{Prefix: m.Prefix, Location: strings.TrimPrefix(m.Location, "oci://")})
	}
	return mirrors
}
