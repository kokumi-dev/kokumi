package signing

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sigstore/cosign/v3/pkg/cosign"
	"github.com/stretchr/testify/require"

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

// fakeInputs returns its Inputs and err to a Provider and counts the reads.
type fakeInputs struct {
	Inputs
	err   error
	reads atomic.Int32
}

func (f *fakeInputs) read(context.Context) (Inputs, error) {
	f.reads.Add(1)
	return f.Inputs, f.err
}

func TestProvider(t *testing.T) {
	data := generateSecretData(t, "secret")
	rotated := testSigner(t)

	kitchen := &deliveryv1alpha1.Kitchen{
		Spec: deliveryv1alpha1.KitchenSpec{Signing: &deliveryv1alpha1.KitchenSigning{
			TrustedKeys:               []deliveryv1alpha1.TrustedKey{{Name: "before-rotation", PublicKey: string(rotated.PublicKeyPEM())}},
			RequireSourceVerification: true,
		}},
	}

	t.Run("is not ready without a key", func(t *testing.T) {
		in := &fakeInputs{err: fmt.Errorf("%w: Secret not found", ErrKeyNotReady)}
		provider := NewProvider(in.read)

		_, err := provider.State(context.Background())
		require.ErrorIs(t, err, ErrKeyNotReady)
	})

	t.Run("is not ready with an invalid key", func(t *testing.T) {
		in := &fakeInputs{KeyData: map[string][]byte{SecretPrivateKey: []byte("invalid")}}
		provider := NewProvider(in.read)

		_, err := provider.State(context.Background())
		require.ErrorIs(t, err, ErrKeyNotReady)
		require.ErrorIs(t, err, ErrInvalidKey)
	})

	t.Run("loads the key and the trusted keys", func(t *testing.T) {
		in := &fakeInputs{KeyData: data, Kitchen: kitchen}
		provider := NewProvider(in.read)
		changes := 0
		provider.OnChange(func() { changes++ })

		state, err := provider.State(context.Background())
		require.NoError(t, err)
		require.True(t, state.RequireSourceVerification)
		require.Len(t, state.Trust.KeyIDs(), 2)
		require.Equal(t, "current", state.Trust.KeyName(state.Signer.KeyID()))
		require.Equal(t, 1, changes)

		key := provider.key
		_, err = provider.Load(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, changes, "an unchanged state notifies nobody")
		require.Same(t, key, provider.key, "an unchanged Secret is not decrypted again")
	})

	t.Run("loads once for concurrent first uses", func(t *testing.T) {
		in := &fakeInputs{KeyData: data, Kitchen: kitchen}
		provider := NewProvider(in.read)

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
		require.Equal(t, int32(1), in.reads.Load())
	})

	t.Run("picks up a rotated key", func(t *testing.T) {
		in := &fakeInputs{KeyData: data}
		provider := NewProvider(in.read)
		changes := 0
		provider.OnChange(func() { changes++ })

		before, err := provider.State(context.Background())
		require.NoError(t, err)
		require.Equal(t, 1, changes)

		in.KeyData = generateSecretData(t, "other")

		after, err := provider.Load(context.Background())
		require.NoError(t, err)
		require.NotEqual(t, before.Signer.KeyID(), after.Signer.KeyID())
		require.Equal(t, 2, changes, "a rotated key notifies the listeners")
	})

	t.Run("keeps the last good state on a transient read error", func(t *testing.T) {
		in := &fakeInputs{KeyData: data}
		provider := NewProvider(in.read)

		before, err := provider.State(context.Background())
		require.NoError(t, err)

		in.err = errors.New("connection refused")
		after, err := provider.Load(context.Background())
		require.NoError(t, err)
		require.Equal(t, before.ID(), after.ID())
	})
}
