package source

import (
	"context"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

// pushArtifact pushes a small artifact to the registry at host and returns
// its reference by digest.
func pushArtifact(t *testing.T, host, repository string) oci.Reference {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("kind: ConfigMap\n"), 0600))

	ref, err := oci.Parse("oci://" + host + "/" + repository + ":1.0.0")
	require.NoError(t, err)

	digest, err := oci.NewORASClient().Push(context.Background(), ref, dir, oci.PushOptions{})
	require.NoError(t, err)

	ref.Tag = ""
	ref.Digest = digest
	return ref
}

func TestVerify(t *testing.T) {
	server := httptest.NewServer(registry.New(registry.WithReferrersSupport(true), registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")

	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	verifier := NewVerifier()

	signed := pushArtifact(t, host, "charts/signed")
	_, err := attestation.Sign(ctx, oci.NewORASClient(), signer, signed, time.Now())
	require.NoError(t, err)

	t.Run("verifies a bundle signed with a listed key", func(t *testing.T) {
		got, err := verifier.Verify(ctx, signed, nil, Policy{Keys: []Key{
			{Name: "other.pub", PEM: signingtest.NewSigner(t).PublicKeyPEM()},
			{Name: "cosign.pub", PEM: signer.PublicKeyPEM()},
		}})
		require.NoError(t, err)
		require.Equal(t, signer.KeyID(), got.KeyID)
	})

	t.Run("does not verify a bundle signed with another key", func(t *testing.T) {
		_, err := verifier.Verify(ctx, signed, nil, Policy{Keys: []Key{{Name: "other.pub", PEM: signingtest.NewSigner(t).PublicKeyPEM()}}})
		require.ErrorIs(t, err, ErrNotVerified)
	})

	t.Run("does not verify an unsigned artifact", func(t *testing.T) {
		unsigned := pushArtifact(t, host, "charts/unsigned")

		_, err := verifier.Verify(ctx, unsigned, nil, Policy{Keys: []Key{{Name: "cosign.pub", PEM: signer.PublicKeyPEM()}}})
		require.ErrorIs(t, err, ErrNotVerified)
	})

	t.Run("requires a key or an identity", func(t *testing.T) {
		_, err := verifier.Verify(ctx, signed, nil, Policy{})
		require.ErrorIs(t, err, ErrNotVerified)
	})
}
