package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

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
