// Package provenance builds and attaches the signed SLSA provenance of
// rendered artifacts. Everything in a provenance statement is taken from the
// artifact's config blob, so it can be rebuilt at any time with equal bytes.
package provenance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
)

const (
	// PredicateType identifies the SLSA v1 provenance predicate.
	PredicateType = "https://slsa.dev/provenance/v1"

	// BuildType identifies how Kokumi renders an artifact.
	BuildType = "https://kokumi.dev/buildtypes/render/v1"

	// BuilderID identifies the Kokumi controller as the build platform. The
	// image and renderer that ran are recorded in the builder's version.
	BuilderID = "https://kokumi.dev/builders/controller/v1"

	// AnnotationVerified is the annotation of a resolved dependency that
	// records who signed it, when its signature verified.
	AnnotationVerified = "kokumi.dev/verified"

	// AnnotationVendored is the annotation of a container image dependency
	// that records where it was vendored to.
	AnnotationVendored = "kokumi.dev/vendored"

	sha256Prefix = "sha256:"
)

// ErrInvalidConfig reports that the config blob of an artifact cannot be
// turned into a provenance statement. The blob is immutable, so retrying does
// not help.
var ErrInvalidConfig = errors.New("invalid artifact config")

// Predicate is the SLSA v1 provenance predicate.
type Predicate struct {
	BuildDefinition BuildDefinition `json:"buildDefinition"`
	RunDetails      RunDetails      `json:"runDetails"`
}

// BuildDefinition describes the inputs of the render.
type BuildDefinition struct {
	BuildType            string               `json:"buildType"`
	ExternalParameters   ExternalParameters   `json:"externalParameters"`
	ResolvedDependencies []ResourceDescriptor `json:"resolvedDependencies"`
}

// ExternalParameters names what was rendered. The full parameters are the
// intent in the subject's config blob, identified by ConfigHash.
type ExternalParameters struct {
	// Order is the namespace/name of the Order.
	Order string `json:"order,omitempty"`
	// Product is the namespace/name of the Product the Order is a variant of.
	Product string `json:"product,omitempty"`
	// ConfigHash is the hash of the intent in the subject's config blob.
	ConfigHash string `json:"configHash,omitempty"`
}

// ResourceDescriptor identifies an input artifact.
type ResourceDescriptor struct {
	// URI is the oci:// URL of the artifact, with the tag it was resolved from.
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest"`
	// MediaType is the media type of a container image's manifest.
	MediaType string `json:"mediaType,omitempty"`
	// Annotations hold AnnotationVerified for a source whose signature
	// verified, and AnnotationVendored for a vendored container image.
	Annotations map[string]any `json:"annotations,omitempty"`
}

// RunDetails describes the render run.
type RunDetails struct {
	Builder  Builder  `json:"builder"`
	Metadata Metadata `json:"metadata"`
}

// Builder identifies the controller that rendered the artifact.
type Builder struct {
	ID string `json:"id"`
	// Version maps the components of the builder to their versions: "kokumi"
	// to the controller image and the lowercase renderer type to the version
	// of the rendering engine.
	Version map[string]string `json:"version"`
}

// Metadata records when the render ran.
type Metadata struct {
	StartedOn  string `json:"startedOn"`
	FinishedOn string `json:"finishedOn"`
}

// imageDependencies returns the container images the render resolved to
// digests, in the order of the config blob, which is sorted by reference.
func imageDependencies(imgs []images.Image) []ResourceDescriptor {
	var deps []ResourceDescriptor
	for _, img := range imgs {
		algorithm, encoded, ok := strings.Cut(img.Digest, ":")
		if !ok {
			continue
		}

		dep := ResourceDescriptor{
			URI:       "oci://" + img.TaggedName(),
			Digest:    map[string]string{algorithm: encoded},
			MediaType: img.MediaType,
		}
		if img.Vendored != "" {
			dep.Annotations = map[string]any{AnnotationVendored: img.Vendored}
		}
		deps = append(deps, dep)
	}
	return deps
}

// Statement returns the deterministic in-toto provenance statement for the
// artifact at subject (which must carry a digest), built from its config.
func Statement(subject oci.Reference, cfg *artifact.Config) ([]byte, error) {
	sourceHex, ok := strings.CutPrefix(cfg.Source.Digest, sha256Prefix)
	if !ok || sourceHex == "" {
		return nil, fmt.Errorf("unsupported source digest %q in artifact config", cfg.Source.Digest)
	}

	params := ExternalParameters{ConfigHash: cfg.ConfigHash}
	if cfg.Order != nil {
		params.Order = cfg.Order.Namespace + "/" + cfg.Order.Name
	}
	if cfg.Product != nil {
		params.Product = cfg.Product.Namespace + "/" + cfg.Product.Name
	}

	source := ResourceDescriptor{
		URI:    cfg.Source.OCI + ":" + cfg.Source.Version,
		Digest: map[string]string{"sha256": sourceHex},
	}
	if cfg.Source.Verified != nil {
		source.Annotations = map[string]any{AnnotationVerified: cfg.Source.Verified}
	}

	return attestation.NewStatement(subject.RepositoryReference(), subject.Digest, PredicateType, Predicate{
		BuildDefinition: BuildDefinition{
			BuildType:            BuildType,
			ExternalParameters:   params,
			ResolvedDependencies: append([]ResourceDescriptor{source}, imageDependencies(cfg.Images)...),
		},
		RunDetails: RunDetails{
			Builder: Builder{
				ID: BuilderID,
				Version: map[string]string{
					"kokumi":                           cfg.Renderer.Builder,
					strings.ToLower(cfg.Renderer.Type): cfg.Renderer.Version,
				},
			},
			Metadata: Metadata{StartedOn: cfg.CreatedTime, FinishedOn: cfg.CreatedTime},
		},
	})
}

// ReadConfig reads the config blob of the artifact at subject. A blob that
// is not a Kokumi config, or records no valid creation time, fails with
// ErrInvalidConfig.
func ReadConfig(ctx context.Context, client oci.Client, subject oci.Reference) (*artifact.Config, error) {
	mediaType, data, err := client.FetchConfig(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("reading artifact config: %w", err)
	}

	cfg, err := artifact.ParseConfig(mediaType, data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	if _, err := cfg.Created(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return cfg, nil
}

// Attach signs the provenance of the artifact at subject, built from its
// config cfg, and attaches it as an OCI referrer. It returns the referrer's
// digest. Retries are idempotent. A config that cannot be turned into a
// statement fails with ErrInvalidConfig.
func Attach(ctx context.Context, client oci.Client, signer *signing.Signer, subject oci.Reference, cfg *artifact.Config) (string, error) {
	created, err := cfg.Created()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	statement, err := Statement(subject, cfg)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return attestation.Push(ctx, client, signer, subject, PredicateType, statement, created)
}
