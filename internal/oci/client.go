package oci

import "context"

// HelmChartLayerMediaType is the CNCF Helm OCI media type for chart content.
const HelmChartLayerMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// AnnotationParentDigest is the OCI manifest annotation key that records the digest
// of the artifact produced by the immediately preceding Preparation for the same Order.
// Its presence on a manifest makes the revision chain explicit and tamper-evident.
const AnnotationParentDigest = "kokumi.dev/parent"

// Client defines the interface for interacting with an OCI registry.
type Client interface {
	// Pull fetches an OCI artifact from a registry into targetDir.
	// It returns the media type of the primary layer, the manifest digest, and any error.
	// For Helm charts the media type is HelmChartLayerMediaType and the blob is
	// written to targetDir/chart.tgz. For all other artifacts manifest.yaml is written.
	Pull(ctx context.Context, ref, tag, targetDir string) (mediaType, digest string, err error)

	// Push pushes an OCI artifact from sourceDir to a registry and returns its digest.
	// annotations are attached as OCI manifest annotations; pass nil for none.
	Push(ctx context.Context, ref, tag, sourceDir string, annotations map[string]string) (digest string, err error)
}
