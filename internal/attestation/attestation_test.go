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
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

const (
	testDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	predicateType = "https://example.com/predicate/v1"
	resultKey     = "result"
)

func testSubject(t *testing.T) oci.Reference {
	t.Helper()

	subject, err := oci.Parse("oci://registry.example/shop@" + testDigest)
	require.NoError(t, err)
	return subject
}

func TestNewStatement(t *testing.T) {
	const subjectName = "oci://registry.example/shop"
	predicate := map[string]string{resultKey: "ok"}

	data, err := NewStatement(subjectName, testDigest, predicateType, predicate)
	require.NoError(t, err)

	var got Statement
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, statementType, got.Type)
	assert.Equal(t, []Subject{{Name: subjectName, Digest: map[string]string{"sha256": testDigest[len(sha256Prefix):]}}}, got.Subject)
	assert.Equal(t, predicateType, got.PredicateType)
	assert.Equal(t, map[string]any{resultKey: "ok"}, got.Predicate)

	again, err := NewStatement(subjectName, testDigest, predicateType, predicate)
	require.NoError(t, err)
	assert.Equal(t, data, again, "encoding must be deterministic")
}

func TestNewStatementRejectsNonSHA256(t *testing.T) {
	_, err := NewStatement("oci://registry.example/shop", "sha512:abc", predicateType, nil)
	require.Error(t, err)
}

func TestSignatureStatement(t *testing.T) {
	data, err := SignatureStatement(testDigest)
	require.NoError(t, err)

	// The shape cosign sign v3 writes: no subject name, an empty predicate.
	assert.JSONEq(t, `{
		"_type": "https://in-toto.io/Statement/v1",
		"subject": [{"digest": {"sha256": "1111111111111111111111111111111111111111111111111111111111111111"}}],
		"predicateType": "https://sigstore.dev/cosign/sign/v1",
		"predicate": {}
	}`, string(data))
}

func TestPush(t *testing.T) {
	fake := oci.NewFakeClient(nil)
	signer := signingtest.NewSigner(t)
	subject := testSubject(t)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.FixedZone("CEST", 2*3600))

	statement, err := NewStatement("", testDigest, predicateType, map[string]string{resultKey: "ok"})
	require.NoError(t, err)

	digest, err := Push(context.Background(), fake, signer, subject, predicateType, statement, created)
	require.NoError(t, err)
	assert.NotEmpty(t, digest)

	require.Len(t, fake.Referrers, 1)
	pushed := fake.Referrers[0]
	assert.Equal(t, subject, pushed.Subject)
	assert.Equal(t, oci.SigstoreBundleMediaType, pushed.Artifact.ArtifactType)
	assert.Equal(t, oci.SigstoreBundleMediaType, pushed.Artifact.MediaType)
	assert.Equal(t, map[string]string{
		ocispec.AnnotationCreated:         "2026-09-25T08:00:00Z",
		oci.AnnotationBundleContent:       oci.BundleContentDSSEEnvelope,
		oci.AnnotationBundlePredicateType: predicateType,
		oci.AnnotationInTotoPredicateType: predicateType,
	}, pushed.Artifact.Annotations)

	verified, err := signer.TrustSet().Verify(pushed.Artifact.Payload, testDigest, predicateType)
	require.NoError(t, err)
	assert.JSONEq(t, string(statement), string(verified.Statement))

	t.Run("reuses an equal attestation", func(t *testing.T) {
		again, err := Push(context.Background(), fake, signer, subject, predicateType, statement, created)
		require.NoError(t, err)
		assert.Equal(t, digest, again)
		assert.Len(t, fake.Referrers, 1)
	})

	t.Run("pushes a different statement", func(t *testing.T) {
		other, err := NewStatement("", testDigest, predicateType, map[string]string{resultKey: "changed"})
		require.NoError(t, err)

		_, err = Push(context.Background(), fake, signer, subject, predicateType, other, created)
		require.NoError(t, err)
		assert.Len(t, fake.Referrers, 2)
	})

	t.Run("does not reuse an attestation of another key", func(t *testing.T) {
		_, err := Push(context.Background(), fake, signingtest.NewSigner(t), subject, predicateType, statement, created)
		require.NoError(t, err)
		assert.Len(t, fake.Referrers, 3)
	})
}

func TestFind(t *testing.T) {
	signer := signingtest.NewSigner(t)
	subject := testSubject(t)
	created := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	statement, err := NewStatement("", testDigest, predicateType, map[string]string{resultKey: "ok"})
	require.NoError(t, err)

	t.Run("finds a verified attestation", func(t *testing.T) {
		fake := oci.NewFakeClient(nil)
		digest, err := Push(context.Background(), fake, signer, subject, predicateType, statement, created)
		require.NoError(t, err)

		evidence, err := Find(context.Background(), fake, signer.TrustSet(), subject, predicateType)
		require.NoError(t, err)
		assert.Equal(t, digest, evidence.Digest)
		assert.Equal(t, signer.KeyID(), evidence.KeyID)

		var predicate map[string]string
		require.NoError(t, evidence.Predicate(&predicate))
		assert.Equal(t, map[string]string{resultKey: "ok"}, predicate)
	})

	t.Run("reports a missing attestation", func(t *testing.T) {
		fake := oci.NewFakeClient(nil)
		_, err := Push(context.Background(), fake, signer, subject, "https://example.com/other/v1", statement, created)
		require.NoError(t, err)

		_, err = Find(context.Background(), fake, signer.TrustSet(), subject, predicateType)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("reports an attestation of an untrusted key", func(t *testing.T) {
		fake := oci.NewFakeClient(nil)
		_, err := Push(context.Background(), fake, signingtest.NewSigner(t), subject, predicateType, statement, created)
		require.NoError(t, err)

		_, err = Find(context.Background(), fake, signer.TrustSet(), subject, predicateType)
		require.ErrorIs(t, err, signing.ErrNotVerified)
	})

	t.Run("reports an unsigned referrer", func(t *testing.T) {
		fake := oci.NewFakeClient(nil)
		_, err := fake.PushReferrer(context.Background(), subject, oci.ReferrerArtifact{
			ArtifactType: oci.SigstoreBundleMediaType,
			MediaType:    oci.SigstoreBundleMediaType,
			Payload:      statement,
			Annotations:  map[string]string{oci.AnnotationBundlePredicateType: predicateType},
		})
		require.NoError(t, err)

		_, err = Find(context.Background(), fake, signer.TrustSet(), subject, predicateType)
		require.ErrorIs(t, err, signing.ErrNotVerified)
	})

	t.Run("skips a forged referrer for a verified one", func(t *testing.T) {
		fake := oci.NewFakeClient(nil)
		digest, err := Push(context.Background(), fake, signer, subject, predicateType, statement, created)
		require.NoError(t, err)
		_, err = Push(context.Background(), fake, signingtest.NewSigner(t), subject, predicateType, statement, created.Add(time.Hour))
		require.NoError(t, err)

		evidence, err := Find(context.Background(), fake, signer.TrustSet(), subject, predicateType)
		require.NoError(t, err)
		assert.Equal(t, digest, evidence.Digest)
	})
}
