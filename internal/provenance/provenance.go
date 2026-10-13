// Package provenance builds and attaches the SLSA provenance of rendered
// artifacts. Everything in a provenance statement is taken from the
// artifact's config blob, so it can be rebuilt at any time with equal bytes.
package provenance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	// PredicateType identifies the SLSA v1 provenance predicate.
	PredicateType = "https://slsa.dev/provenance/v1"

	// BuildType identifies how Kokumi renders an artifact.
	BuildType = "https://kokumi.dev/buildtypes/render/v1"

	// BuilderID identifies the Kokumi controller as the build platform. The
	// image and renderer that ran are recorded in the builder's version.
	BuilderID = "https://kokumi.dev/builders/controller/v1"

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
	// Order is the namespace/name of the Order. Empty for artifacts
	// pre-rendered by a Menu.
	Order string `json:"order,omitempty"`
	// Menu is the namespace/name of the Menu the configuration came from.
	Menu string `json:"menu,omitempty"`
	// ConfigHash is the hash of the intent in the subject's config blob.
	ConfigHash string `json:"configHash,omitempty"`
}

// ResourceDescriptor identifies an input artifact.
type ResourceDescriptor struct {
	// URI is the oci:// URL of the artifact, with the tag it was resolved from.
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest"`
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
	if cfg.Menu != nil {
		params.Menu = cfg.Menu.Namespace + "/" + cfg.Menu.Name
	}

	return attestation.NewStatement(subject.RepositoryReference(), subject.Digest, PredicateType, Predicate{
		BuildDefinition: BuildDefinition{
			BuildType:          BuildType,
			ExternalParameters: params,
			ResolvedDependencies: []ResourceDescriptor{{
				URI:    cfg.Source.OCI + ":" + cfg.Source.Version,
				Digest: map[string]string{"sha256": sourceHex},
			}},
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

// Attach reads the config blob of the artifact at subject, builds its
// provenance statement, and pushes it as an OCI referrer. It returns the
// referrer's digest. Retries are idempotent. A config blob that cannot be
// turned into a statement fails with ErrInvalidConfig.
func Attach(ctx context.Context, client oci.Client, subject oci.Reference) (string, error) {
	mediaType, data, err := client.FetchConfig(ctx, subject)
	if err != nil {
		return "", fmt.Errorf("reading artifact config: %w", err)
	}

	statement, created, err := fromConfig(subject, mediaType, data)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return attestation.Push(ctx, client, subject, oci.ProvenanceArtifactType, statement, created)
}

// fromConfig returns the provenance statement of the artifact at subject and
// its creation time, both taken from the artifact's config blob.
func fromConfig(subject oci.Reference, mediaType string, data []byte) ([]byte, time.Time, error) {
	cfg, err := artifact.ParseConfig(mediaType, data)
	if err != nil {
		return nil, time.Time{}, err
	}

	created, err := cfg.Created()
	if err != nil {
		return nil, time.Time{}, err
	}

	statement, err := Statement(subject, cfg)
	if err != nil {
		return nil, time.Time{}, err
	}

	return statement, created, nil
}
