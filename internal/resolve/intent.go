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
	// Destination is the resolved OCI URL the artifact is pushed to.
	Destination string `json:"destination"`
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
	// Promotion holds the gates the artifact must pass before it is served.
	Promotion *IntentPromotion `json:"promotion,omitempty"`
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

// IntentPromotion holds the promotion gates that are part of an Intent.
type IntentPromotion struct {
	// Approvals is the approval policy the artifact is rendered under.
	Approvals *deliveryv1alpha1.ApprovalPolicy `json:"approvals,omitempty"`
}

// NewIntent builds the Intent of order from its effective spec and its
// destination URL.
func NewIntent(order *deliveryv1alpha1.Order, effective *EffectiveSpec, destination string) Intent {
	intent := Intent{
		Source:      IntentSource{OCI: effective.Source.URL, Version: effective.Source.Version, Digest: effective.SourceDigest},
		Destination: destination,
		Render:      effective.Render,
		Patches:     effective.Patches,
		Edits:       effective.Edits,
	}

	if policy := effective.Images.EffectivePolicy(); policy != deliveryv1alpha1.ImagePolicyRecord || len(effective.Images.FieldSpecs) > 0 {
		intent.Images = &IntentImages{Policy: policy, FieldSpecs: effective.Images.FieldSpecs, Vendor: effective.ImagesVendorURL}
	}

	if policy := order.ApprovalPolicy(); policy != nil {
		intent.Promotion = &IntentPromotion{Approvals: policy}
	}

	return intent
}

// Canonical returns the canonical JSON encoding of the intent: compact, with
// object keys sorted at every level and numbers kept as written, so that
// equal intents encode to equal bytes regardless of how inline values were
// written.
func (i Intent) Canonical() ([]byte, error) {
	data, err := json.Marshal(i)
	if err != nil {
		return nil, fmt.Errorf("failed to encode intent: %w", err)
	}

	// Decoding into any and encoding again sorts the keys of raw JSON
	// fields such as Helm values. UseNumber keeps numbers out of float64,
	// which would round large integers.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, fmt.Errorf("failed to canonicalize intent: %w", err)
	}

	var canonical bytes.Buffer

	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(generic); err != nil {
		return nil, fmt.Errorf("failed to canonicalize intent: %w", err)
	}

	return bytes.TrimSuffix(canonical.Bytes(), []byte("\n")), nil
}
