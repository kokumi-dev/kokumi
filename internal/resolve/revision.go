package resolve

import (
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// productPart is the part of the intent every Order of a Product shares
// before its own values, patches and gates: what the Product contributes to
// the rendered output.
type productPart struct {
	Source  productPartSource        `json:"source"`
	Render  *deliveryv1alpha1.Render `json:"render,omitempty"`
	Patches []deliveryv1alpha1.Patch `json:"patches,omitempty"`
	Images  *IntentImages            `json:"images,omitempty"`
}

// productPartSource pins the source by digest only: vendoring changes where
// the source is pulled from, not what it is.
type productPartSource struct {
	Digest string `json:"digest"`
}

// ProductRevision returns the Product revision of product: the SHA-256 of
// the canonical JSON of its part of the intent. That is the resolved source
// digest, the render config before an Order's values are merged, the
// patches, and the image handling with the location images are vendored
// below, which the rendered manifests pull from. The source URL and version,
// the overrides and the Registries change no output and are left out.
//
// The revision links the Preparations the Orders of a Product render from
// one Product change, as GitOps Promoter's dry commit links the hydrated
// commits of its environments: upstream stages are matched by it.
func ProductRevision(product *deliveryv1alpha1.Product) (string, error) {
	canonical, err := productRevisionCanonical(product)
	if err != nil {
		return "", err
	}
	return CalculateSpecHash(canonical), nil
}

// productRevisionCanonical returns the canonical JSON the revision of
// product is the SHA-256 of.
func productRevisionCanonical(product *deliveryv1alpha1.Product) ([]byte, error) {
	part := productPart{
		Render:  product.Spec.Render,
		Patches: product.Spec.Patches,
	}
	vendorURL := ""
	if source := product.Status.Source; source != nil {
		part.Source.Digest = source.Digest
		if product.Spec.Images.EffectivePolicy() == deliveryv1alpha1.ImagePolicyVendor {
			vendorURL = source.URL
		}
	}
	part.Images = intentImages(product.Spec.Images, vendorURL)

	canonical, err := canonicalJSON(part)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize the revision of Product %q: %w", product.Name, err)
	}
	return canonical, nil
}
