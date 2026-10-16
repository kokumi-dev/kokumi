package signing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	subjectDigest   = "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	otherDigest     = "sha256:d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"
	testPredicate   = "https://kokumi.dev/attestations/test/v1"
	statementFormat = `{"_type":"https://in-toto.io/Statement/v1","subject":[{"digest":{"sha256":"%s"}}],"predicateType":"%s","predicate":{}}`
)

// generateSecretData returns signing key Secret data as cosign generates it.
func generateSecretData(t *testing.T, password string) map[string][]byte {
	t.Helper()

	keys, err := cosign.GenerateKeyPair(func(bool) ([]byte, error) { return []byte(password), nil })
	require.NoError(t, err)

	return map[string][]byte{
		SecretPrivateKey: keys.PrivateBytes,
		SecretPassword:   []byte(password),
		SecretPublicKey:  keys.PublicBytes,
	}
}

func testSigner(t *testing.T) *Signer {
	t.Helper()

	key, err := LoadKey(generateSecretData(t, "secret"))
	require.NoError(t, err)

	signer, err := NewSigner(key)
	require.NoError(t, err)

	return signer
}

func statement(digest, predicateType string) []byte {
	return fmt.Appendf(nil, statementFormat, digest[len("sha256:"):], predicateType)
}

func TestLoadKey(t *testing.T) {
	t.Run("loads a key cosign generated", func(t *testing.T) {
		data := generateSecretData(t, "secret")

		key, err := LoadKey(data)
		require.NoError(t, err)
		require.Equal(t, string(data[SecretPublicKey]), string(key.PublicKeyPEM()))
		require.NotEmpty(t, key.ID())
	})

	t.Run("loads a key with an empty password", func(t *testing.T) {
		_, err := LoadKey(generateSecretData(t, ""))
		require.NoError(t, err)
	})

	t.Run("refuses a wrong password", func(t *testing.T) {
		data := generateSecretData(t, "secret")
		data[SecretPassword] = []byte("other")

		_, err := LoadKey(data)
		require.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("refuses a missing private key", func(t *testing.T) {
		_, err := LoadKey(map[string][]byte{SecretPassword: []byte("secret")})
		require.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("refuses a public key of another key", func(t *testing.T) {
		data := generateSecretData(t, "secret")
		data[SecretPublicKey] = generateSecretData(t, "secret")[SecretPublicKey]

		_, err := LoadKey(data)
		require.ErrorIs(t, err, ErrInvalidKey)
	})
}

func TestSign(t *testing.T) {
	signer := testSigner(t)
	payload := statement(subjectDigest, testPredicate)

	t.Run("is deterministic", func(t *testing.T) {
		first, err := signer.Sign(payload)
		require.NoError(t, err)
		second, err := signer.Sign(payload)
		require.NoError(t, err)

		require.Equal(t, string(first), string(second))
	})

	t.Run("verifies against the signing key", func(t *testing.T) {
		bundle, err := signer.Sign(payload)
		require.NoError(t, err)

		verified, err := signer.TrustSet().Verify(bundle, subjectDigest, testPredicate)
		require.NoError(t, err)
		require.Equal(t, signer.KeyID(), verified.KeyID)
		require.JSONEq(t, string(payload), string(verified.Statement))
	})

	t.Run("does not verify against another key", func(t *testing.T) {
		bundle, err := signer.Sign(payload)
		require.NoError(t, err)

		_, err = testSigner(t).TrustSet().Verify(bundle, subjectDigest, testPredicate)
		require.ErrorIs(t, err, ErrNotVerified)
		var untrusted *UntrustedSignerError
		require.ErrorAs(t, err, &untrusted)
		require.Equal(t, signer.KeyID(), untrusted.KeyID)
	})

	t.Run("does not verify for another subject", func(t *testing.T) {
		bundle, err := signer.Sign(payload)
		require.NoError(t, err)

		_, err = signer.TrustSet().Verify(bundle, otherDigest, testPredicate)
		require.ErrorIs(t, err, ErrNotVerified)
		var untrusted *UntrustedSignerError
		require.NotErrorAs(t, err, &untrusted, "a trusted key with a wrong subject is invalid, not untrusted")
	})

	t.Run("does not verify for another predicate type", func(t *testing.T) {
		bundle, err := signer.Sign(payload)
		require.NoError(t, err)

		_, err = signer.TrustSet().Verify(bundle, subjectDigest, "https://slsa.dev/provenance/v1")
		require.ErrorIs(t, err, ErrNotVerified)
	})

	t.Run("does not verify a tampered statement", func(t *testing.T) {
		bundle, err := signer.Sign(payload)
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(bundle, &decoded))
		envelope := decoded["dsseEnvelope"].(map[string]any)
		envelope["payload"] = base64.StdEncoding.EncodeToString(statement(otherDigest, testPredicate))
		tampered, err := json.Marshal(decoded)
		require.NoError(t, err)

		_, err = signer.TrustSet().Verify(tampered, otherDigest, testPredicate)
		require.ErrorIs(t, err, ErrNotVerified)
	})

	t.Run("does not verify garbage", func(t *testing.T) {
		_, err := signer.TrustSet().Verify([]byte(`{"mediaType":"x"}`), subjectDigest, testPredicate)
		require.ErrorIs(t, err, ErrNotVerified)
	})
}

func TestTrustSet(t *testing.T) {
	current, rotated := testSigner(t), testSigner(t)

	trust, err := NewTrustSet(
		TrustedPublicKey{Name: "current", PEM: current.PublicKeyPEM()},
		TrustedPublicKey{Name: "before-rotation", PEM: rotated.PublicKeyPEM()},
	)
	require.NoError(t, err)

	t.Run("verifies bundles of every trusted key", func(t *testing.T) {
		for _, signer := range []*Signer{current, rotated} {
			bundle, err := signer.Sign(statement(subjectDigest, testPredicate))
			require.NoError(t, err)

			verified, err := trust.Verify(bundle, subjectDigest, testPredicate)
			require.NoError(t, err)
			require.Equal(t, signer.KeyID(), verified.KeyID)
		}
		require.Equal(t, "before-rotation", trust.KeyName(rotated.KeyID()))
	})

	t.Run("identifies its content", func(t *testing.T) {
		reordered, err := NewTrustSet(
			TrustedPublicKey{Name: "b", PEM: rotated.PublicKeyPEM()},
			TrustedPublicKey{Name: "a", PEM: current.PublicKeyPEM()},
		)
		require.NoError(t, err)
		require.Equal(t, trust.ID(), reordered.ID())
		require.NotEqual(t, trust.ID(), current.TrustSet().ID())
	})

	t.Run("refuses a malformed key", func(t *testing.T) {
		_, err := NewTrustSet(TrustedPublicKey{Name: "bad", PEM: []byte("-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")})
		require.ErrorIs(t, err, ErrInvalidTrustedKey)
	})

	t.Run("names a bundle without a public key", func(t *testing.T) {
		bundle, err := current.Sign(statement(subjectDigest, "https://slsa.dev/provenance/v1"))
		require.NoError(t, err)

		var decoded map[string]any
		require.NoError(t, json.Unmarshal(bundle, &decoded))
		// A keyless bundle carries a certificate instead of a key hint.
		material := decoded["verificationMaterial"].(map[string]any)
		delete(material, "publicKey")
		material["certificate"] = map[string]any{"rawBytes": base64.StdEncoding.EncodeToString([]byte("certificate"))}
		keyless, err := json.Marshal(decoded)
		require.NoError(t, err)

		_, err = trust.Verify(keyless, subjectDigest, "")
		require.ErrorIs(t, err, ErrNotVerified)
		var untrusted *UntrustedSignerError
		require.ErrorAs(t, err, &untrusted)
		require.Empty(t, untrusted.KeyID)
		require.ErrorContains(t, err, "not signed with a public key")
	})
}

func TestProvider(t *testing.T) {
	const (
		namespace    = "kokumi"
		keyNamespace = deliveryv1alpha1.SigningKeyNamespace
	)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, deliveryv1alpha1.AddToScheme(scheme))

	data := generateSecretData(t, "secret")
	rotated := testSigner(t)

	secret := &corev1.Secret{Namespace: keyNamespace, Name: deliveryv1alpha1.SigningKeySecretName, Data: data}
	kitchen := &deliveryv1alpha1.Kitchen{
		Namespace: namespace, Name: deliveryv1alpha1.DefaultKitchenName,
		Spec: deliveryv1alpha1.KitchenSpec{Signing: &deliveryv1alpha1.KitchenSigning{
			TrustedKeys:               []deliveryv1alpha1.TrustedKey{{Name: "before-rotation", PublicKey: string(rotated.PublicKeyPEM())}},
			RequireSourceVerification: true,
		}},
	}

	t.Run("is not ready without a key", func(t *testing.T) {
		provider := NewProvider(fake.NewClientBuilder().WithScheme(scheme).Build(), namespace, keyNamespace)

		_, err := provider.State(context.Background())
		require.ErrorIs(t, err, ErrKeyNotReady)
	})

	t.Run("loads the key and the trusted keys", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, kitchen).Build()
		provider := NewProvider(reader, namespace, keyNamespace)
		changes := provider.Changes()

		state, err := provider.State(context.Background())
		require.NoError(t, err)
		require.True(t, state.RequireSourceVerification)
		require.Len(t, state.Trust.KeyIDs(), 2)
		require.Equal(t, "current", state.Trust.KeyName(state.Signer.KeyID()))
		require.Len(t, changes, 1)
		<-changes

		key := provider.key
		_, err = provider.Load(context.Background())
		require.NoError(t, err)
		require.Empty(t, changes, "an unchanged state notifies nobody")
		require.Same(t, key, provider.key, "an unchanged Secret is not decrypted again")
	})

	t.Run("loads once for concurrent first uses", func(t *testing.T) {
		provider := NewProvider(fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret, kitchen).Build(), namespace, keyNamespace)

		var wg sync.WaitGroup
		ids := make(chan string, 4)
		for range 4 {
			wg.Go(func() {
				state, err := provider.State(context.Background())
				if err == nil {
					ids <- state.Signer.KeyID()
				}
			})
		}
		wg.Wait()
		close(ids)

		var got []string
		for id := range ids {
			got = append(got, id)
		}
		require.Len(t, got, 4)
		require.Equal(t, []string{got[0], got[0], got[0], got[0]}, got)
	})

	t.Run("picks up a rotated key", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret.DeepCopy()).Build()
		provider := NewProvider(reader, namespace, keyNamespace)
		changes := provider.Changes()

		before, err := provider.State(context.Background())
		require.NoError(t, err)
		<-changes

		rotated := &corev1.Secret{}
		require.NoError(t, reader.Get(context.Background(), client.ObjectKeyFromObject(secret), rotated))
		rotated.Data = generateSecretData(t, "other")
		require.NoError(t, reader.Update(context.Background(), rotated))

		after, err := provider.Load(context.Background())
		require.NoError(t, err)
		require.NotEqual(t, before.Signer.KeyID(), after.Signer.KeyID())
		require.Len(t, changes, 1, "a rotated key notifies the subscribers")
	})
}
