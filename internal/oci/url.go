package oci

import "strings"

// ExtractHost returns the registry hostname (and port, if present) from an OCI URL.
// Examples:
//
//	"oci://ghcr.io/my-org/my-app" → "ghcr.io"
//	"oci://registry.local:5000/charts/app" → "registry.local:5000"
func ExtractHost(ociURL string) string {
	trimmed := strings.TrimPrefix(ociURL, "oci://")
	return strings.SplitN(trimmed, "/", 2)[0]
}
