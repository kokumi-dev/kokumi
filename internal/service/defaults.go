package service

import "fmt"

// DefaultRegistryHost is the in-cluster OCI registry hostname.
// This is intentionally a constant to make it easy to refactor later
// (e.g. read from an environment variable or service discovery).
const DefaultRegistryHost = "kokumi-registry.kokumi.svc.cluster.local:5000"

// DefaultDestination returns the OCI destination URL for an Order
// when no explicit destination has been configured.
// It follows the pattern oci://<DefaultRegistryHost>/<namespace>/<name>.
func DefaultDestination(namespace, name string) string {
	return fmt.Sprintf("oci://%s/%s/%s", DefaultRegistryHost, namespace, name)
}

// Commit message defaults used when the caller has not provided an explicit message.
const (
	InitialCommitMessage   = "Initial commit"
	AutomatedCommitMessage = "Automatically generated"
)

// DefaultCommitMessage returns the effective commit message for a Preparation.
func DefaultCommitMessage(message string, messageProvided bool, isInitial bool) string {
	if messageProvided {
		return message
	}
	if isInitial {
		return InitialCommitMessage
	}
	return AutomatedCommitMessage
}
