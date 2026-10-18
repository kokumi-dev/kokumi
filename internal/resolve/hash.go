package resolve

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// CalculateSpecHash returns the config hash of an Order: the SHA-256 of its
// canonical intent (see Intent.Canonical). Equal hashes mean equal intent.
func CalculateSpecHash(canonical []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
}

// CalculateProductHash computes a stable SHA-256 hash over the inputs a
// Product's source is resolved from: the source and the URL it is vendored
// to. Render config, patches, overrides and the Registries are left out;
// they change what Orders render, or how Kokumi connects, not the source.
// vendorURL is empty when the Product does not vendor its source.
func CalculateProductHash(source deliveryv1alpha1.OCISource, vendorURL string) (string, error) {
	data, err := json.Marshal(struct {
		Source    deliveryv1alpha1.OCISource `json:"source"`
		VendorURL string                     `json:"vendorURL,omitempty"`
	}{source, vendorURL})
	if err != nil {
		return "", fmt.Errorf("failed to encode Product source for hashing: %w", err)
	}

	return fmt.Sprintf("sha256:%x", sha256.Sum256(data)), nil
}
