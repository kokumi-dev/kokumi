package renderer

import (
	"crypto/sha256"
	"fmt"
	"strings"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

// CalculateSpecHash computes a stable SHA-256 hash over the complete set of inputs
// that determine the content of a rendered artifact.
func CalculateSpecHash(spec deliveryv1alpha1.OrderSpec) (string, error) {
	var builder strings.Builder

	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)

	var oci, version string
	if spec.Source != nil {
		oci = spec.Source.OCI
		version = spec.Source.Version
	}

	var menuRef string
	if spec.MenuRef != nil {
		menuRef = spec.MenuRef.Name
	}

	if err := encoder.Encode(struct {
		OCI     string                   `yaml:"oci,omitempty"`
		Version string                   `yaml:"version,omitempty"`
		MenuRef string                   `yaml:"menuRef,omitempty"`
		Render  *deliveryv1alpha1.Render `yaml:"render,omitempty"`
		Patches []deliveryv1alpha1.Patch `yaml:"patches,omitempty"`
		Edits   []deliveryv1alpha1.Patch `yaml:"edits,omitempty"`
	}{
		OCI:     oci,
		Version: version,
		MenuRef: menuRef,
		Render:  spec.Render,
		Patches: spec.Patches,
		Edits:   spec.Edits,
	}); err != nil {
		return "", fmt.Errorf("failed to encode spec for hashing: %w", err)
	}

	encoder.Close() //nolint:errcheck

	hash := sha256.Sum256([]byte(builder.String()))

	return fmt.Sprintf("sha256:%x", hash), nil
}
