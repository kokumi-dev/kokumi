package resolve

import (
	"bytes"
	"encoding/json"
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Intent is the merged, effective configuration that determines a rendered
// artifact. It is the single input of the config hash and is stored verbatim
// in the artifact's config blob.
//
// It deliberately leaves out everything that does not change what is rendered
// or what must hold before it is served: the Product reference, the
// Registries and their credentials, the image mirrors, the digests images
// resolve to, the promotion mode, and the commit message.
type Intent struct {
	// Source is the resolved source artifact.
	Source IntentSource `json:"source"`
	// ProductRevision identifies the Product change the artifact renders
	// (see ProductRevision). Upstream stages are matched by it, so a Product
	// change renders every Order again, also one whose own values or
	// patches hide it.
	ProductRevision string `json:"productRevision"`
	// Destination is the resolved OCI URL the artifact is pushed to.
	Destination string `json:"destination"`
	// TargetNamespace is the namespace the artifact is rendered for and
	// deploys into on every cluster.
	TargetNamespace string `json:"targetNamespace"`
	// Render is the render configuration after merging the Order over its
	// Product.
	Render *deliveryv1alpha1.Render `json:"render,omitempty"`
	// Patches are the patches after merging the Order over its Product.
	Patches []deliveryv1alpha1.Patch `json:"patches,omitempty"`
	// Edits are applied on top of the patches.
	Edits []deliveryv1alpha1.Patch `json:"edits,omitempty"`
	// Images is how the images of the rendered manifests are handled. It is
	// absent under the default Record policy without field specs.
	Images *IntentImages `json:"images,omitempty"`
	// Promotion holds the gates the artifact must pass before it is served,
	// with the upstream stages sorted by Order.
	Promotion *deliveryv1alpha1.PromotionGates `json:"promotion,omitempty"`
}

// IntentSource is the resolved source of an Intent.
type IntentSource struct {
	// OCI is the resolved OCI URL of the source artifact.
	OCI string `json:"oci"`
	// Version is the tag the source is resolved from.
	Version string `json:"version"`
	// Digest pins the source to the artifact the Product resolved, so that
	// the Product resolving a new artifact changes the intent.
	Digest string `json:"digest"`
}

// IntentImages is the image handling that is part of an Intent.
type IntentImages struct {
	// Policy is the image policy.
	Policy deliveryv1alpha1.ImagePolicy `json:"policy"`
	// FieldSpecs locate image fields besides the default ones.
	FieldSpecs []deliveryv1alpha1.ImageFieldSpec `json:"fieldSpecs,omitempty"`
	// Vendor is the location the images are vendored below under the
	// Vendor policy, so that moving it renders the manifests again.
	Vendor string `json:"vendor,omitempty"`
}

// NewIntent builds the Intent of order from its effective spec and its
// destination URL.
func NewIntent(order *deliveryv1alpha1.Order, effective *EffectiveSpec, destination string) Intent {
	return Intent{
		Source:          IntentSource{OCI: effective.Source.URL, Version: effective.Source.Version, Digest: effective.SourceDigest},
		ProductRevision: effective.ProductRevision,
		Destination:     destination,
		TargetNamespace: effective.TargetNamespace,
		Render:          effective.Render,
		Patches:         effective.Patches,
		Edits:           effective.Edits,
		Images:          intentImages(effective.Images, effective.ImagesVendorURL),
		Promotion:       order.PromotionGates(),
	}
}

// intentImages returns the image handling of images vendored below
// vendorURL, nil under the default Record policy without field specs.
func intentImages(images deliveryv1alpha1.ProductImages, vendorURL string) *IntentImages {
	policy := images.EffectivePolicy()
	if policy == deliveryv1alpha1.ImagePolicyRecord && len(images.FieldSpecs) == 0 {
		return nil
	}
	return &IntentImages{Policy: policy, FieldSpecs: images.FieldSpecs, Vendor: vendorURL}
}

// Canonical returns the canonical JSON encoding of the intent: compact, with
// object keys sorted at every level and numbers kept as written, so that
// equal intents encode to equal bytes regardless of how inline values were
// written.
func (i Intent) Canonical() ([]byte, error) {
	canonical, err := canonicalJSON(i)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize intent: %w", err)
	}
	return canonical, nil
}

// canonicalJSON returns the canonical JSON encoding of v (see
// Intent.Canonical).
func canonicalJSON(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	// Decoding into any and encoding again sorts the keys of raw JSON
	// fields such as Helm values. UseNumber keeps numbers out of float64,
	// which would round large integers.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}

	var canonical bytes.Buffer

	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(generic); err != nil {
		return nil, err
	}

	return bytes.TrimSuffix(canonical.Bytes(), []byte("\n")), nil
}
