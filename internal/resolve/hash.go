package resolve

import (
	"crypto/sha256"
	"fmt"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

// CalculateSpecHash returns the config hash of an Order: the SHA-256 of its
// canonical intent (see Intent.Canonical). Equal hashes mean equal intent.
func CalculateSpecHash(canonical []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
}

// CalculateMenuHash computes a stable SHA-256 hash over the Menu spec inputs
// that determine the content of a published (rendered) artifact: source,
// vendor mode and destination, render config, and patches.
func CalculateMenuHash(menu deliveryv1alpha1.MenuSpec) (string, error) {
	var builder strings.Builder

	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)

	if err := encoder.Encode(menu); err != nil {
		return "", fmt.Errorf("failed to encode menu spec for hashing: %w", err)
	}

	encoder.Close() //nolint:errcheck

	hash := sha256.Sum256([]byte(builder.String()))

	return fmt.Sprintf("sha256:%x", hash), nil
}
