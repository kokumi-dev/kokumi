package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

const attestedDigest = "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"

func TestListAttestations(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	prep := &deliveryv1alpha1.Preparation{Spec: deliveryv1alpha1.PreparationSpec{Artifact: deliveryv1alpha1.Artifact{
		OCIRef: "oci://registry.example/shop/podinfo@" + attestedDigest,
		Digest: attestedDigest,
	}}}
	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	require.NoError(t, err)

	registry := oci.NewFakeClient(nil)
	signed, err := attestation.Sign(ctx, registry, signer, subject, time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	forged, err := attestation.Sign(ctx, registry, signingtest.NewSigner(t), subject, time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	_, err = registry.PushReferrer(ctx, subject, oci.ReferrerArtifact{
		ArtifactType: "application/vnd.example.sbom+json",
		MediaType:    "application/json",
		Payload:      []byte(`{"secret":"not shown"}`),
		Annotations:  map[string]string{"org.opencontainers.image.created": "2026-10-12T11:00:00Z"},
	})
	require.NoError(t, err)

	attestations, err := listAttestations(ctx, registry, signer.TrustSet(), prep, true)
	require.NoError(t, err)
	require.Len(t, attestations, 3)

	verified := attestations[0]
	assert.Equal(t, signed, verified.Digest)
	assert.Equal(t, attestation.SignaturePredicateType, verified.PredicateType)
	assert.Equal(t, AttestationVerificationDTO{Verified: true, KeyID: signer.KeyID()}, verified.Verification)
	var statement map[string]any
	require.NoError(t, json.Unmarshal(verified.Payload, &statement))
	assert.Equal(t, attestation.SignaturePredicateType, statement["predicateType"])

	untrusted := attestations[1]
	assert.Equal(t, forged, untrusted.Digest)
	assert.False(t, untrusted.Verification.Verified)
	assert.Contains(t, untrusted.Verification.Error, "untrusted key")
	assert.Nil(t, untrusted.Payload, "an unverified statement is never shown")

	other := attestations[2]
	assert.False(t, other.Verification.Verified)
	assert.Equal(t, "not a Sigstore bundle", other.Verification.Error)
	assert.Nil(t, other.Payload)
}

func TestValidateSigningDTO(t *testing.T) {
	publicKey := string(signingtest.NewSigner(t).PublicKeyPEM())

	tests := []struct {
		name    string
		dto     signingSettingsDTO
		wantErr string
	}{
		{name: "empty", dto: signingSettingsDTO{}},
		{name: "valid key", dto: signingSettingsDTO{TrustedKeys: []trustedKeyDTO{{Name: "before-2026-10", PublicKey: publicKey}}}},
		{name: "invalid name", dto: signingSettingsDTO{TrustedKeys: []trustedKeyDTO{{Name: "Before Rotation", PublicKey: publicKey}}}, wantErr: "DNS label"},
		{name: "duplicate name", dto: signingSettingsDTO{TrustedKeys: []trustedKeyDTO{
			{Name: "a", PublicKey: publicKey}, {Name: "a", PublicKey: publicKey},
		}}, wantErr: "used twice"},
		{name: "not a public key", dto: signingSettingsDTO{TrustedKeys: []trustedKeyDTO{{Name: "a", PublicKey: "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"}}}, wantErr: "invalid trusted key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSigningDTO(&tt.dto)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	t.Run("settings that trust and require nothing clear spec.signing", func(t *testing.T) {
		assert.Nil(t, signingFromDTO(&signingSettingsDTO{}))
		assert.Equal(t, signingSettingsDTO{TrustedKeys: []trustedKeyDTO{}}, signingToDTO(nil))
	})
}

func TestSigningFromKitchen(t *testing.T) {
	current, rotated := signingtest.NewSigner(t), signingtest.NewSigner(t)
	kitchen := &deliveryv1alpha1.Kitchen{
		Spec: deliveryv1alpha1.KitchenSpec{Signing: &deliveryv1alpha1.KitchenSigning{
			TrustedKeys:               []deliveryv1alpha1.TrustedKey{{Name: "before-rotation", PublicKey: string(rotated.PublicKeyPEM())}},
			RequireSourceVerification: true,
		}},
		Status: deliveryv1alpha1.KitchenStatus{
			Signing: &deliveryv1alpha1.KitchenSigningStatus{KeyID: current.KeyID(), PublicKey: string(current.PublicKeyPEM())},
			Conditions: []metav1.Condition{{
				Type: deliveryv1alpha1.ConditionTypeSigningKeyReady, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonKeyReady,
			}},
		},
	}

	got := signingFromKitchen(kitchen)
	assert.True(t, got.Ready)
	assert.True(t, got.RequireSourceVerification)
	require.NotNil(t, got.Current)
	assert.Equal(t, current.KeyID(), got.Current.KeyID)
	assert.Equal(t, []signingKeyDTO{{Name: "before-rotation", KeyID: rotated.KeyID(), PublicKey: string(rotated.PublicKeyPEM())}}, got.TrustedKeys)

	trust, err := signing.TrustSetForKitchen(kitchen)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{current.KeyID(), rotated.KeyID()}, trust.KeyIDs())
}
