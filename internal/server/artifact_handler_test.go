package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

func TestArtifactGraph(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	registry := oci.NewFakeClient(nil)

	ref, err := oci.Parse("oci://registry.example/shop/podinfo:6.10.2")
	require.NoError(t, err)
	digest, err := registry.Push(ctx, ref, "/out", oci.PushOptions{ArtifactType: oci.PreparationArtifactType})
	require.NoError(t, err)
	subject := ref
	subject.Digest = digest

	signed, err := attestation.Sign(ctx, registry, signer, subject, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	forged, err := attestation.Sign(ctx, registry, signingtest.NewSigner(t), subject, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	sbom, err := registry.PushReferrer(ctx, subject, oci.ReferrerArtifact{
		ArtifactType: "application/spdx+json",
		MediaType:    "application/spdx+json",
		Payload:      []byte(`{"spdxVersion":"SPDX-2.3"}`),
		Annotations:  map[string]string{"org.opencontainers.image.created": "2026-10-12T11:00:00Z"},
	})
	require.NoError(t, err)
	sbomRef := subject
	sbomRef.Tag = ""
	sbomRef.Digest = sbom
	sbomSigned, err := attestation.Sign(ctx, registry, signer, sbomRef, time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	graph, err := artifactGraph(ctx, registry, signer.TrustSet(), subject.OCIString(), nil)
	require.NoError(t, err)
	assert.Equal(t, "oci://registry.example/shop/podinfo", graph.Repository)
	assert.Equal(t, "6.10.2", graph.Tag)
	require.NotNil(t, graph.Root)

	root := graph.Root
	assert.Equal(t, digest, root.Digest)
	assert.Equal(t, oci.PreparationArtifactType, root.ArtifactType)
	assert.Nil(t, root.Verification, "only Sigstore bundles are verified")
	require.Len(t, root.Referrers, 3)

	verified := root.Referrers[0]
	assert.Equal(t, signed, verified.Digest)
	assert.Equal(t, string(oci.LinkSubject), verified.Link)
	assert.Equal(t, attestation.SignaturePredicateType, verified.PredicateType)
	assert.Equal(t, &VerificationDTO{Verified: true, KeyID: signer.KeyID(), KeyName: signing.CurrentKeyName}, verified.Verification)
	var statement map[string]any
	require.NoError(t, json.Unmarshal(verified.Payload, &statement))
	assert.Equal(t, attestation.SignaturePredicateType, statement["predicateType"])

	untrusted := root.Referrers[1]
	assert.Equal(t, forged, untrusted.Digest)
	require.NotNil(t, untrusted.Verification)
	assert.False(t, untrusted.Verification.Verified)
	assert.Contains(t, untrusted.Verification.Error, "untrusted key")
	assert.Equal(t, reasonUntrustedSigner, untrusted.Verification.Reason)
	assert.Nil(t, untrusted.Payload, "an unverified statement is never shown")

	sbomNode := root.Referrers[2]
	assert.Equal(t, sbom, sbomNode.Digest)
	assert.Nil(t, sbomNode.Verification)
	assert.Nil(t, sbomNode.Payload)
	require.Len(t, sbomNode.Referrers, 1)
	sbomSignature := sbomNode.Referrers[0]
	assert.Equal(t, sbomSigned, sbomSignature.Digest)
	assert.Equal(t, &VerificationDTO{Verified: true, KeyID: signer.KeyID(), KeyName: signing.CurrentKeyName}, sbomSignature.Verification, "a signature verifies against the manifest it is attached to")

	assert.Empty(t, verified.Verification.Reason)

	t.Run("a signature does not verify for another subject", func(t *testing.T) {
		_, err := verifyBundle(ctx, registry, signer.TrustSet(), sbomRef, sbomSigned, digest)
		require.ErrorIs(t, err, signing.ErrNotVerified)
	})
}

func TestArtifactGraph_RecordedVerdicts(t *testing.T) {
	ctx := context.Background()
	kokumi := signingtest.NewSigner(t)
	registry := oci.NewFakeClient(nil)

	ref, err := oci.Parse("oci://ghcr.io/example/charts/app:1.0.0")
	require.NoError(t, err)
	digest, err := registry.Push(ctx, ref, "/out", oci.PushOptions{ArtifactType: "application/vnd.cncf.helm.config.v1+json"})
	require.NoError(t, err)
	subject := ref
	subject.Digest = digest

	keyless, err := registry.PushReferrer(ctx, subject, oci.ReferrerArtifact{ArtifactType: oci.SigstoreBundleMediaType, MediaType: oci.SigstoreBundleMediaType, Payload: []byte(`{"keyless":true}`)})
	require.NoError(t, err)
	notary, err := registry.PushReferrer(ctx, subject, oci.ReferrerArtifact{ArtifactType: "application/vnd.cncf.notary.signature", MediaType: "application/jose+json", Payload: []byte("{}")})
	require.NoError(t, err)
	later, err := registry.PushReferrer(ctx, subject, oci.ReferrerArtifact{ArtifactType: oci.SigstoreBundleMediaType, MediaType: oci.SigstoreBundleMediaType, Payload: []byte(`{"later":true}`)})
	require.NoError(t, err)
	byKokumi, err := attestation.Sign(ctx, registry, kokumi, subject, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	verdicts := map[string]deliveryv1alpha1.SignatureVerdict{
		keyless: {Digest: keyless, Subject: digest, Result: deliveryv1alpha1.SignatureVerified, Signer: &deliveryv1alpha1.SourceVerification{
			Provider: deliveryv1alpha1.VerificationProviderCosign, Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/app/release.yml@refs/tags/v1",
		}},
		notary:   {Digest: notary, Subject: digest, Result: deliveryv1alpha1.SignatureUnsupported, Message: "Notary signatures are not verified"},
		byKokumi: {Digest: byKokumi, Subject: digest, Result: deliveryv1alpha1.SignatureUntrustedSigner},
	}

	graph, err := artifactGraph(ctx, registry, kokumi.TrustSet(), subject.OCIString(), verdicts)
	require.NoError(t, err)

	got := map[string]*VerificationDTO{}
	for _, r := range graph.Root.Referrers {
		got[r.Digest] = r.Verification
	}
	assert.Equal(t, &VerificationDTO{Verified: true, Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/app/release.yml@refs/tags/v1"}, got[keyless],
		"a keyless signature the Product controller verified")
	assert.Equal(t, &VerificationDTO{Reason: reasonUnsupported, Error: "Notary signatures are not verified"}, got[notary])
	assert.Equal(t, &VerificationDTO{Reason: reasonNotChecked}, got[later], "attached after the last check")
	assert.Equal(t, &VerificationDTO{Verified: true, KeyID: kokumi.KeyID(), KeyName: signing.CurrentKeyName}, got[byKokumi],
		"a live check by a Kitchen key wins")
}
