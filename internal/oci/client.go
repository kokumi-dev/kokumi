package oci

import "context"

// Client defines the interface for interacting with an OCI registry.
type Client interface {
	// Pull fetches an OCI artifact from a registry into targetDir and returns its digest.
	Pull(ctx context.Context, ref, tag, targetDir string) (digest string, err error)

	// Push pushes an OCI artifact from sourceDir to a registry and returns its digest.
	Push(ctx context.Context, ref, tag, sourceDir string) (digest string, err error)
}
