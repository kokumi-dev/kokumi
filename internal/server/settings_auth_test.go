package server

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestAuthSettings(t *testing.T) {
	apply := func(t *testing.T, body string) *deliveryv1alpha1.KitchenAuth {
		t.Helper()
		var dto authDTO
		require.NoError(t, json.Unmarshal([]byte(body), &dto))
		auth := &deliveryv1alpha1.KitchenAuth{AdminUser: &deliveryv1alpha1.AdminUserConfig{Username: testUsername}}
		applyAuth(auth, &dto)
		return auth
	}

	t.Run("enabled turns the admin account on", func(t *testing.T) {
		auth := apply(t, `{"adminUser":{"enabled":true,"username":"root"}}`)
		require.NotNil(t, auth.AdminUser)
		assert.True(t, auth.AdminUser.Enabled)
		assert.Equal(t, testRootUser, auth.AdminUser.Username)
	})

	t.Run("a disabled admin account keeps its settings", func(t *testing.T) {
		auth := apply(t, `{"adminUser":{"enabled":false,"username":"root"}}`)
		require.NotNil(t, auth.AdminUser)
		assert.False(t, auth.AdminUser.Enabled)
		assert.Equal(t, testRootUser, auth.AdminUser.Username)
	})

	t.Run("settings without an admin account remove it", func(t *testing.T) {
		auth := apply(t, `{"tokenSigningKeySecretRef":{"name":"kokumi-server-tokens"}}`)
		assert.Nil(t, auth.AdminUser)
	})

	t.Run("the admin account reads as enabled, username and Secret", func(t *testing.T) {
		out, err := json.Marshal(authToDTO(&deliveryv1alpha1.KitchenAuth{AdminUser: &deliveryv1alpha1.AdminUserConfig{
			Enabled:   true,
			Username:  testUsername,
			SecretRef: &corev1.LocalObjectReference{Name: testSecret},
		}}))
		require.NoError(t, err)
		assert.JSONEq(t, `{"adminUser":{"enabled":true,"username":"admin","secretRef":{"name":"kokumi-server-auth"}}}`, string(out))
	})

	t.Run("providerSignOut round-trips", func(t *testing.T) {
		auth := apply(t, `{"oidc":{"issuerURL":"https://sso.example.com","clientID":"kokumi","providerSignOut":"Always"}}`)
		require.NotNil(t, auth.OIDC)
		assert.Equal(t, deliveryv1alpha1.ProviderSignOutAlways, auth.OIDC.ProviderSignOut)

		out, err := json.Marshal(authToDTO(auth).OIDC)
		require.NoError(t, err)
		assert.JSONEq(t, `{"issuerURL":"https://sso.example.com","clientID":"kokumi","providerSignOut":"Always"}`, string(out))
	})
}
