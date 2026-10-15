// Package resolve computes the effective rendering spec of an Order by merging
// the Order over its Product.
// It is a pure transform layer: no I/O, no Kubernetes client calls.
package resolve

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
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
	// Product identifies the Product the spec was merged from.
	Product ProductIdentity
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

	return &EffectiveSpec{
		Source: deliveryv1alpha1.OCISource{
			OCI:       product.Status.Source.OCI,
			Version:   product.Status.Source.Version,
			PantryRef: product.Status.Source.PantryRef,
		},
		SourceDigest:   product.Status.Source.Digest,
		SourceVerified: product.Status.Source.Verified,
		Render:         render,
		Patches:        patches,
		Edits:          order.Spec.Edits,
		Product:        ProductIdentity{Name: product.Name, ConfigHash: product.Status.ConfigHash},
	}, nil
}
