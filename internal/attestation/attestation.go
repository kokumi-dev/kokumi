package attestation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	// MediaTypeStatement is the media type of an unsigned in-toto statement.
	MediaTypeStatement = "application/vnd.in-toto+json"

	statementType = "https://in-toto.io/Statement/v1"
	sha256Prefix  = "sha256:"
)

// Statement is an in-toto v1 statement.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     any       `json:"predicate"`
}

// Subject identifies the attested artifact.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// NewStatement returns the JSON-encoded statement that predicate holds for
// the artifact subjectName with the given "sha256:<hex>" digest. Encoding is
// deterministic for deterministic predicates.
func NewStatement(subjectName, digest, predicateType string, predicate any) ([]byte, error) {
	hex, ok := strings.CutPrefix(digest, sha256Prefix)
	if !ok || hex == "" {
		return nil, fmt.Errorf("unsupported subject digest %q: only sha256 is supported", digest)
	}
	data, err := json.Marshal(Statement{
		Type:          statementType,
		Subject:       []Subject{{Name: subjectName, Digest: map[string]string{"sha256": hex}}},
		PredicateType: predicateType,
		Predicate:     predicate,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding in-toto statement: %w", err)
	}
	return data, nil
}

// Push attaches statement to the manifest at subject as an OCI referrer of
// artifactType and returns the referrer's digest. created is recorded as
// org.opencontainers.image.created so equal statements yield equal digests.
func Push(ctx context.Context, client oci.Client, subject oci.Reference, artifactType string, statement []byte, created time.Time) (string, error) {
	digest, err := client.PushReferrer(ctx, subject, oci.ReferrerArtifact{
		ArtifactType: artifactType,
		MediaType:    MediaTypeStatement,
		Payload:      statement,
		Annotations: map[string]string{
			ocispec.AnnotationCreated: created.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return "", fmt.Errorf("pushing attestation for %s: %w", subject, err)
	}
	return digest, nil
}
