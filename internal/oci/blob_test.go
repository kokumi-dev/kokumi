package oci

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/content/oci"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Platforms of the test image index.
const (
	testOS    = "linux"
	testARM64 = "arm64"
)

func TestFetchBlob(t *testing.T) {
	ctx := context.Background()
	client := NewClient(Options{PlainHTTP: true})
	host := newTestRegistry(t, true)

	ref, err := Parse("oci://" + host + "/shop/blob:1.0.0")
	require.NoError(t, err)
	root := pushTestArtifact(t, client, ref, "kind: ConfigMap\n")

	manifest, err := client.FetchManifest(ctx, root)
	require.NoError(t, err)
	require.NotNil(t, manifest.Config)
	layer := manifest.Layers[0]

	data, err := client.FetchBlob(ctx, root, layer, layer.Size)
	require.NoError(t, err)
	assert.Len(t, data, int(layer.Size))

	_, err = client.FetchBlob(ctx, root, layer, layer.Size-1)
	require.ErrorIs(t, err, ErrPayloadTooLarge)
}

func TestExtractLayers(t *testing.T) {
	ctx := context.Background()
	client := NewClient(Options{PlainHTTP: true})
	host := newTestRegistry(t, true)

	ref, err := Parse("oci://" + host + "/shop/db:2")
	require.NoError(t, err)
	root := pushTestArtifact(t, client, ref, "kind: ConfigMap\n")

	dir := filepath.Join(t.TempDir(), "db")
	desc, err := client.ExtractLayers(ctx, ref, dir)
	require.NoError(t, err)
	assert.Equal(t, root.Digest, desc.Digest, "the tag resolves to the pushed manifest")

	data, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "kind: ConfigMap\n", string(data))
}

func TestCopyToLayout(t *testing.T) {
	ctx := context.Background()
	client := NewClient(Options{PlainHTTP: true})
	host := newTestRegistry(t, true)

	amd64, err := random.Image(64, 1)
	require.NoError(t, err)
	arm64, err := random.Image(64, 1)
	require.NoError(t, err)
	index := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: amd64, Platform: &v1.Platform{OS: testOS, Architecture: "amd64"}},
		mutate.IndexAddendum{Add: arm64, Platform: &v1.Platform{OS: testOS, Architecture: testARM64}},
	)
	tag, err := name.NewTag(host+"/library/app:1.0", name.Insecure)
	require.NoError(t, err)
	require.NoError(t, remote.WriteIndex(tag, index))
	indexDigest, err := index.Digest()
	require.NoError(t, err)
	armDigest, err := arm64.Digest()
	require.NoError(t, err)

	ref, err := Parse("oci://" + host + "/library/app@" + indexDigest.String())
	require.NoError(t, err)

	t.Run("the index lists the platforms", func(t *testing.T) {
		manifest, err := client.FetchManifest(ctx, ref)
		require.NoError(t, err)
		require.Len(t, manifest.Manifests, 2)
		assert.Equal(t, testOS+"/amd64", manifest.Manifests[0].Platform)
		assert.Equal(t, testOS+"/"+testARM64, manifest.Manifests[1].Platform)
		assert.Equal(t, armDigest.String(), manifest.Manifests[1].Digest)
	})

	t.Run("one platform of an index", func(t *testing.T) {
		dir := t.TempDir()
		desc, err := client.CopyToLayout(ctx, ref, dir, "scan", &ocispec.Platform{OS: testOS, Architecture: testARM64})
		require.NoError(t, err)
		assert.Equal(t, armDigest.String(), desc.Digest)
		assert.NotEqual(t, ocispec.MediaTypeImageIndex, desc.MediaType, "the platform manifest, not the index")

		store, err := oci.NewWithContext(ctx, dir)
		require.NoError(t, err)
		tagged, err := store.Resolve(ctx, "scan")
		require.NoError(t, err)
		assert.Equal(t, armDigest.String(), tagged.Digest.String(), "the layout tags the platform manifest")
	})

	t.Run("a missing platform", func(t *testing.T) {
		_, err := client.CopyToLayout(ctx, ref, t.TempDir(), "scan", &ocispec.Platform{OS: testOS, Architecture: "s390x"})
		require.Error(t, err)
	})
}
