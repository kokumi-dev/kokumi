package oci

import (
	"context"
	"errors"
)

// ErrIsIndex is returned when a single manifest was expected but the reference
// resolves to an image index.
var ErrIsIndex = errors.New("reference is an image index")

// AnnotationParentDigest is the OCI manifest annotation key that records the digest
// of the artifact produced by the immediately preceding Preparation for the same Order.
// Its presence on a manifest makes the revision chain explicit and tamper-evident.
const AnnotationParentDigest = "kokumi.dev/parent"

// AnnotationApprovalPolicy is the OCI manifest annotation key that records the
// JSON-encoded approval policy the artifact was rendered under.
const AnnotationApprovalPolicy = "kokumi.dev/approval-policy"

// PushOptions configures Push. The zero value pushes a single-layer artifact
// with an unknown artifact type and no config blob.
type PushOptions struct {
	// Annotations are attached as OCI manifest annotations.
	Annotations map[string]string
	// ArtifactType is the manifest artifactType. It defaults to
	// application/vnd.unknown.artifact.v1.
	ArtifactType string
	// Config is the optional config blob.
	Config []byte
	// ConfigMediaType is the media type of Config. It is required when Config
	// is set.
	ConfigMediaType string
}

// CopyOptions configures Copy.
type CopyOptions struct {
	// IncludeReferrers also copies the referrers of every copied manifest,
	// recursively.
	IncludeReferrers bool
}

// Descriptor describes content by digest, as an OCI descriptor does.
type Descriptor struct {
	MediaType string
	// ArtifactType is the type of the artifact a manifest describes. For an
	// image manifest without one it is its config's media type, as the OCI
	// referrers API reports it.
	ArtifactType string
	Digest       string
	Size         int64
	Annotations  map[string]string
}

// Manifest is the content of an image manifest or an image index.
type Manifest struct {
	// Descriptor describes the manifest itself. Its Annotations are the
	// manifest's own.
	Descriptor
	// Config is the config blob of an image manifest.
	Config *Descriptor
	// Layers are the layers of an image manifest.
	Layers []Descriptor
	// Manifests are the children of an image index.
	Manifests []Descriptor
	// Subject is the manifest this one is a referrer of.
	Subject *Descriptor
}

// Client defines the interface for interacting with an OCI registry.
type Client interface {
	// Pull fetches an OCI artifact from a registry into targetDir.
	// It resolves the artifact by ref.Reference() (digest when set, else tag).
	// It returns the media type of the primary layer, the manifest digest, the
	// manifest annotations, and any error.
	// For Helm charts the media type is HelmChartLayerMediaType and the blob is
	// written to targetDir/chart.tgz. For all other artifacts the layer's files
	// are extracted into targetDir.
	// It returns ErrIsIndex when ref resolves to an image index.
	Pull(ctx context.Context, ref Reference, targetDir string) (mediaType, digest string, annotations map[string]string, err error)

	// Push pushes an OCI artifact from sourceDir to a registry and returns its digest.
	Push(ctx context.Context, ref Reference, sourceDir string, opts PushOptions) (digest string, err error)

	// ListTags returns the list of tags available for the given repository reference.
	// The ref should not include a tag or digest. Returns an error if the registry
	// is unreachable or the repository does not exist.
	ListTags(ctx context.Context, ref Reference) ([]string, error)

	// Resolve resolves ref (tag or digest) to the manifest digest of the artifact.
	Resolve(ctx context.Context, ref Reference) (digest string, err error)

	// Copy copies the artifact at srcRef (in this client's registry) and
	// everything it references (index children, config, layers) to dstRef (in
	// target's registry), preserving the original manifests and media types.
	Copy(ctx context.Context, target Client, srcRef, dstRef Reference, opts CopyOptions) error

	// FetchConfig returns the config blob of the manifest at ref.
	FetchConfig(ctx context.Context, ref Reference) (mediaType string, data []byte, err error)

	// PushReferrer pushes artifact as an OCI 1.1 referrer of the manifest at
	// subject (which must carry a digest) and returns the referrer's digest.
	PushReferrer(ctx context.Context, subject Reference, artifact ReferrerArtifact) (digest string, err error)

	// ListReferrers lists the referrers of subject. An empty artifactType
	// lists all of them.
	ListReferrers(ctx context.Context, subject Reference, artifactType string) ([]Descriptor, error)

	// FetchReferrer returns the single-blob payload of the referrer at ref,
	// or ErrPayloadTooLarge when it exceeds MaxReferrerPayloadSize.
	FetchReferrer(ctx context.Context, ref Reference) (ReferrerArtifact, error)

	// PushIndex pushes an OCI image index whose children already exist in
	// ref's repository and returns its digest. It reads the Digest and
	// Annotations of each child.
	PushIndex(ctx context.Context, ref Reference, artifactType string, children []Descriptor, annotations map[string]string) (digest string, err error)

	// FetchManifest returns the image manifest or image index at ref.
	FetchManifest(ctx context.Context, ref Reference) (Manifest, error)
}
