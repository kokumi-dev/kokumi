package namespace

import (
	"os"
	"strings"
)

// Default is used when the running namespace cannot be determined.
const Default = "kokumi"

// Current determines the namespace the component is running in, trying the
// POD_NAMESPACE env var, then the in-cluster service account file, then a
// default.
func Current(getenv func(string) string) string {
	if ns := strings.TrimSpace(getenv("POD_NAMESPACE")); ns != "" {
		return ns
	}
	const saNamespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	if data, err := os.ReadFile(saNamespaceFile); err == nil {
		if ns := strings.TrimSpace(string(data)); ns != "" {
			return ns
		}
	}
	return Default
}
