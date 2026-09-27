package attestation

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/oci"
)

const testDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func TestNewStatement(t *testing.T) {
	const (
		subjectName   = "oci://registry.example/shop"
		predicateType = "https://example.com/predicate/v1"
	)
	predicate := map[string]string{"result": "ok"}

	data, err := NewStatement(subjectName, testDigest, predicateType, predicate)
	require.NoError(t, err)

	var got Statement
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, statementType, got.Type)
	assert.Equal(t, []Subject{{Name: subjectName, Digest: map[string]string{"sha256": testDigest[len(sha256Prefix):]}}}, got.Subject)
	assert.Equal(t, predicateType, got.PredicateType)
	assert.Equal(t, map[string]any{"result": "ok"}, got.Predicate)

	again, err := NewStatement(subjectName, testDigest, predicateType, predicate)
	require.NoError(t, err)
	assert.Equal(t, data, again, "encoding must be deterministic")
}

func TestNewStatementRejectsNonSHA256(t *testing.T) {
	_, err := NewStatement("oci://registry.example/shop", "sha512:abc", "https://example.com/predicate/v1", nil)
	require.Error(t, err)
}

func TestPush(t *testing.T) {
	fake := oci.NewFakeClient(nil)
	subject, err := oci.Parse("oci://registry.example/shop@" + testDigest)
	require.NoError(t, err)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.FixedZone("CEST", 2*3600))

	digest, err := Push(context.Background(), fake, subject, "application/vnd.example+json", []byte(`{}`), created)
	require.NoError(t, err)
	assert.NotEmpty(t, digest)

	require.Len(t, fake.Referrers, 1)
	pushed := fake.Referrers[0]
	assert.Equal(t, subject, pushed.Subject)
	assert.Equal(t, "application/vnd.example+json", pushed.Artifact.ArtifactType)
	assert.Equal(t, MediaTypeStatement, pushed.Artifact.MediaType)
	assert.Equal(t, "2026-09-25T08:00:00Z", pushed.Artifact.Annotations[ocispec.AnnotationCreated])
}
