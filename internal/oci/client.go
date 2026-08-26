package oci

import "context"

// HelmChartLayerMediaType is the CNCF Helm OCI media type for chart content.
const HelmChartLayerMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// FluxContentMediaType is the Flux OCI media type for a kustomize/manifest
// content layer. Unlike ORAS "file" artifacts, Flux layers carry no
// io.deis.oras.content.unpack annotation, so they must be extracted explicitly.
const FluxContentMediaType = "application/vnd.cncf.flux.content.v1.tar+gzip"

// AnnotationParentDigest is the OCI manifest annotation key that records the digest
// of the artifact produced by the immediately preceding Preparation for the same Order.
// Its presence on a manifest makes the revision chain explicit and tamper-evident.
const AnnotationParentDigest = "kokumi.dev/parent"

// Client defines the interface for interacting with an OCI registry.
type Client interface {
	// Pull fetches an OCI artifact from a registry into targetDir.
	// It resolves the artifact by ref.Reference() (digest when set, else tag).
	// It returns the media type of the primary layer, the manifest digest, the
	// manifest annotations, and any error.
	// For Helm charts the media type is HelmChartLayerMediaType and the blob is
	// written to targetDir/chart.tgz. For all other artifacts the layer's files
	// are extracted into targetDir.
	Pull(ctx context.Context, ref Reference, targetDir string) (mediaType, digest string, annotations map[string]string, err error)

	// Push pushes an OCI artifact from sourceDir to a registry and returns its digest.
	// annotations are attached as OCI manifest annotations; pass nil for none.
	Push(ctx context.Context, ref Reference, sourceDir string, annotations map[string]string) (digest string, err error)

	// ListTags returns the list of tags available for the given repository reference.
	// The ref should not include a tag or digest. Returns an error if the registry
	// is unreachable or the repository does not exist.
	ListTags(ctx context.Context, ref Reference) ([]string, error)
}
