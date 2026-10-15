package artifact

import "fmt"

// DefaultRegistryHost is the in-cluster OCI registry hostname.
// This is intentionally a constant to make it easy to refactor later
// (e.g. read from an environment variable or service discovery).
const DefaultRegistryHost = "kokumi-registry.kokumi.svc.cluster.local:5000"

// DefaultDestination returns the OCI destination URL used when no explicit
// destination has been configured. It follows the pattern
// oci://<DefaultRegistryHost>/<namespace>/<name>.
func DefaultDestination(namespace, name string) string {
	return fmt.Sprintf("oci://%s/%s/%s", DefaultRegistryHost, namespace, name)
}

// DefaultVendorDestination returns the OCI URL a Product vendors its source
// to when no explicit destination has been configured. It follows the pattern
// oci://<DefaultRegistryHost>/<namespace>/vendor/<product>, so the copy never
// shares a repository, and with it the version tag, with the Preparations of
// an Order of the same name.
func DefaultVendorDestination(namespace, product string) string {
	return fmt.Sprintf("oci://%s/%s/vendor/%s", DefaultRegistryHost, namespace, product)
}
