package sbom

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

func digestOf(name string) string {
	return fmt.Sprintf("sha256:%064x", []byte(name))[:71]
}

var (
	indexDigest       = digestOf("index")
	amd64Digest       = digestOf("amd64")
	arm64Digest       = digestOf("arm64")
	attestationDigest = digestOf("attestations")
	layerDigest       = digestOf("spdx-layer")
	singleDigest      = digestOf("single")
	// variantIndexDigest lists linux/arm64 with its variant, as alpine does.
	variantIndexDigest = digestOf("variant-index")
)

const buildKitSPDX = `{"spdxVersion":"SPDX-2.3","name":"buildkit"}`

// fakeImage returns a client that serves a two-platform image index, with a
// BuildKit SBOM attestation for linux/amd64, and its reference.
func fakeImage(t *testing.T) (*oci.FakeClient, oci.Reference) {
	t.Helper()

	ref, err := oci.Parse("oci://registry.internal/shop/vendor/app/images/docker.io/library/nginx@" + indexDigest)
	require.NoError(t, err)

	statement := `{"_type":"https://in-toto.io/Statement/v0.1","predicateType":"https://spdx.dev/Document","predicate":` + buildKitSPDX + `}`
	client := &oci.FakeClient{
		Manifests: map[string]oci.Manifest{
			indexDigest: {
				Descriptor: oci.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: indexDigest},
				Manifests: []oci.Descriptor{
					{MediaType: ocispec.MediaTypeImageManifest, Digest: amd64Digest, Platform: "linux/amd64"},
					{MediaType: ocispec.MediaTypeImageManifest, Digest: arm64Digest, Platform: "linux/arm64"},
					{MediaType: ocispec.MediaTypeImageManifest, Digest: attestationDigest, Platform: "unknown/unknown", Annotations: map[string]string{
						annotationReferenceType: referenceTypeAttestation, annotationReferenceDigest: amd64Digest,
					}},
				},
			},
			attestationDigest: {
				Descriptor: oci.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: attestationDigest},
				Layers: []oci.Descriptor{
					{MediaType: "application/vnd.in-toto+json", Digest: digestOf("provenance"), Annotations: map[string]string{
						annotationLayerPredicate: "https://slsa.dev/provenance/v0.2",
					}},
					{MediaType: "application/vnd.in-toto+json", Digest: layerDigest, Size: int64(len(statement)), Annotations: map[string]string{
						annotationLayerPredicate: "https://spdx.dev/Document",
					}},
				},
			},
			singleDigest: {Descriptor: oci.Descriptor{MediaType: ocispec.MediaTypeImageManifest, Digest: singleDigest}},
			variantIndexDigest: {
				Descriptor: oci.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: variantIndexDigest},
				Manifests: []oci.Descriptor{
					{MediaType: ocispec.MediaTypeImageManifest, Digest: amd64Digest, Platform: "linux/amd64"},
					{MediaType: ocispec.MediaTypeImageManifest, Digest: arm64Digest, Platform: "linux/arm64/v8"},
				},
			},
		},
		Blobs: map[string][]byte{layerDigest: []byte(statement)},
	}
	return client, ref
}

// attachSBOM attaches document as a signed SBOM of ref, under predicateType.
func attachSBOM(t *testing.T, client oci.Client, signer *signing.Signer, ref oci.Reference, predicateType, document, platform string) string {
	t.Helper()
	statement, err := attestation.NewStatement(ref.RepositoryReference(), ref.Digest, predicateType, json.RawMessage(document))
	require.NoError(t, err)
	annotations := map[string]string{}
	if platform != "" {
		annotations[AnnotationPlatform] = platform
	}
	digest, err := attestation.PushAnnotated(context.Background(), client, signer, ref, predicateType, statement, time.Now(), annotations)
	require.NoError(t, err)
	return digest
}

func TestSelect(t *testing.T) {
	ctx := context.Background()
	client, ref := fakeImage(t)

	target, err := Select(ctx, client, ref, "linux/arm64")
	require.NoError(t, err)
	assert.Equal(t, "linux/arm64", target.Platform)
	assert.Equal(t, arm64Digest, target.Manifest)
	require.NotNil(t, target.Index)

	variant := ref
	variant.Digest = variantIndexDigest
	target, err = Select(ctx, client, variant, "linux/arm64")
	require.NoError(t, err)
	assert.Equal(t, "linux/arm64/v8", target.Platform, "a preference without a variant matches any")
	target, err = Select(ctx, client, variant, "linux/arm64/v9")
	require.NoError(t, err)
	assert.Equal(t, "linux/amd64", target.Platform, "another variant does not match")

	target, err = Select(ctx, client, ref, "linux/s390x")
	require.NoError(t, err)
	assert.Equal(t, "linux/amd64", target.Platform, "the first platform, not the attestations")

	single := ref
	single.Digest = singleDigest
	target, err = Select(ctx, client, single, "linux/arm64")
	require.NoError(t, err)
	assert.Empty(t, target.Platform)
	assert.Equal(t, singleDigest, target.Manifest)
	assert.Nil(t, target.Index)
}

func TestFind(t *testing.T) {
	ctx := context.Background()
	kokumi := signingtest.NewSigner(t)
	trust := kokumi.TrustSet()

	t.Run("the BuildKit SBOM of the scanned platform", func(t *testing.T) {
		client, ref := fakeImage(t)
		target, err := Select(ctx, client, ref, "linux/amd64")
		require.NoError(t, err)

		found, err := Find(ctx, client, trust, target)
		require.NoError(t, err)
		require.NotNil(t, found)
		assert.Equal(t, OriginImage, found.Origin)
		assert.Equal(t, attestationDigest, found.Digest)
		assert.JSONEq(t, buildKitSPDX, string(found.Document))

		arm64, err := Select(ctx, client, ref, "linux/arm64")
		require.NoError(t, err)
		found, err = Find(ctx, client, trust, arm64)
		require.NoError(t, err)
		assert.Nil(t, found, "BuildKit attested only linux/amd64")
	})

	t.Run("Kokumi's own SBOM first, for its platform only", func(t *testing.T) {
		client, ref := fakeImage(t)
		own := attachSBOM(t, client, kokumi, ref, PredicateType, `{"bomFormat":"CycloneDX"}`, "linux/arm64")

		arm64, err := Select(ctx, client, ref, "linux/arm64")
		require.NoError(t, err)
		found, err := Find(ctx, client, trust, arm64)
		require.NoError(t, err)
		require.NotNil(t, found)
		assert.Equal(t, OriginGenerated, found.Origin)
		assert.Equal(t, own, found.Digest)
		assert.JSONEq(t, `{"bomFormat":"CycloneDX"}`, string(found.Document))

		amd64, err := Select(ctx, client, ref, "linux/amd64")
		require.NoError(t, err)
		found, err = Find(ctx, client, trust, amd64)
		require.NoError(t, err)
		assert.Equal(t, OriginImage, found.Origin, "Kokumi's SBOM describes another platform")
	})

	t.Run("an SBOM signed by a trusted key", func(t *testing.T) {
		client, ref := fakeImage(t)
		signed := attachSBOM(t, client, kokumi, ref, "https://spdx.dev/Document/v2.3", `{"spdxVersion":"SPDX-2.3","name":"signed"}`, "")

		arm64, err := Select(ctx, client, ref, "linux/arm64")
		require.NoError(t, err)
		found, err := Find(ctx, client, trust, arm64)
		require.NoError(t, err)
		require.NotNil(t, found)
		assert.Equal(t, OriginSigned, found.Origin)
		assert.Equal(t, signed, found.Digest)
	})

	t.Run("never an SBOM nobody trusted signed", func(t *testing.T) {
		client, ref := fakeImage(t)
		attachSBOM(t, client, signingtest.NewSigner(t), ref, PredicateType, `{"bomFormat":"CycloneDX","components":[]}`, "linux/arm64")
		_, err := client.PushReferrer(ctx, ref, oci.ReferrerArtifact{
			ArtifactType: "application/vnd.cyclonedx+json",
			MediaType:    "application/vnd.cyclonedx+json",
			Payload:      []byte(`{"bomFormat":"CycloneDX","components":[]}`),
		})
		require.NoError(t, err)

		arm64, err := Select(ctx, client, ref, "linux/arm64")
		require.NoError(t, err)
		found, err := Find(ctx, client, trust, arm64)
		require.NoError(t, err)
		assert.Nil(t, found, "an unsigned or untrusted SBOM listing no packages would scan clean")
	})
}

func TestLoad(t *testing.T) {
	ctx := context.Background()
	kokumi := signingtest.NewSigner(t)
	trust := kokumi.TrustSet()

	t.Run("the BuildKit SBOM the index lists", func(t *testing.T) {
		client, ref := fakeImage(t)
		document, err := Load(ctx, client, trust, ref, attestationDigest, OriginImage)
		require.NoError(t, err)
		assert.JSONEq(t, buildKitSPDX, string(document))

		_, err = Load(ctx, client, trust, ref, amd64Digest, OriginImage)
		require.ErrorIs(t, err, ErrNotFound, "a platform manifest is no attestation manifest")
	})

	t.Run("a signed SBOM referrer", func(t *testing.T) {
		client, ref := fakeImage(t)
		own := attachSBOM(t, client, kokumi, ref, PredicateType, `{"bomFormat":"CycloneDX"}`, "linux/arm64")

		document, err := Load(ctx, client, trust, ref, own, OriginGenerated)
		require.NoError(t, err)
		assert.JSONEq(t, `{"bomFormat":"CycloneDX"}`, string(document))

		_, err = Load(ctx, client, signingtest.NewSigner(t).TrustSet(), ref, own, OriginGenerated)
		require.ErrorIs(t, err, signing.ErrNotVerified, "the key is no longer trusted")

		_, err = Load(ctx, client, trust, ref, digestOf("gone"), OriginSigned)
		require.ErrorIs(t, err, ErrNotFound)
	})
}
