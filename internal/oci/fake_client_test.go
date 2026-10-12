package oci

import (
	"context"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeClient_PushDigest(t *testing.T) {
	ctx := context.Background()
	client := NewFakeClient(afero.NewMemMapFs())

	ref, err := Parse("registry.example/a")
	require.NoError(t, err)

	ref.Tag = "1.0.0"

	a, err := client.Push(ctx, ref, "/out", PushOptions{})
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, a)

	b, err := client.Push(ctx, ref, "/out", PushOptions{})
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[a-f0-9]{64}$`, b)
	require.NotEqual(t, a, b)
}

func TestFakeClient_FetchConfig(t *testing.T) {
	ctx := context.Background()
	client := &FakeClient{}

	ref, err := Parse("registry.example/a:1.0.0")
	require.NoError(t, err)

	digest, err := client.Push(ctx, ref, "/out", PushOptions{
		ArtifactType:    "application/vnd.example.v1",
		Config:          []byte(`{"a":1}`),
		ConfigMediaType: "application/vnd.example.config.v1+json",
	})
	require.NoError(t, err)

	t.Run("by tag", func(t *testing.T) {
		mediaType, data, err := client.FetchConfig(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, "application/vnd.example.config.v1+json", mediaType)
		assert.JSONEq(t, `{"a":1}`, string(data))
	})

	t.Run("by digest", func(t *testing.T) {
		byDigest := ref
		byDigest.Tag = ""
		byDigest.Digest = digest

		_, data, err := client.FetchConfig(ctx, byDigest)
		require.NoError(t, err)
		assert.JSONEq(t, `{"a":1}`, string(data))
	})

	t.Run("unknown reference", func(t *testing.T) {
		other, err := Parse("registry.example/b:1.0.0")
		require.NoError(t, err)

		_, _, err = client.FetchConfig(ctx, other)
		require.Error(t, err)
	})
}

func TestFakeClient_Referrers(t *testing.T) {
	ctx := context.Background()
	client := &FakeClient{}

	subject, err := Parse("registry.example/a@sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f")
	require.NoError(t, err)

	first, err := client.PushReferrer(ctx, subject, ReferrerArtifact{ArtifactType: "application/vnd.example.one", Payload: []byte("one")})
	require.NoError(t, err)
	_, err = client.PushReferrer(ctx, subject, ReferrerArtifact{ArtifactType: "application/vnd.example.two", Payload: []byte("two")})
	require.NoError(t, err)

	all, err := client.ListReferrers(ctx, subject, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)

	filtered, err := client.ListReferrers(ctx, subject, "application/vnd.example.one")
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, first, filtered[0].Digest)

	other, err := Parse("registry.example/b@sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f")
	require.NoError(t, err)
	none, err := client.ListReferrers(ctx, other, "")
	require.NoError(t, err)
	assert.Empty(t, none)

	referrerRef := subject
	referrerRef.Digest = first
	artifact, err := client.FetchReferrer(ctx, referrerRef)
	require.NoError(t, err)
	assert.Equal(t, []byte("one"), artifact.Payload)
}

func TestFakeClient_Index(t *testing.T) {
	ctx := context.Background()
	client := &FakeClient{}

	ref, err := Parse("registry.example/a:bundle")
	require.NoError(t, err)

	_, err = client.FetchIndex(ctx, ref)
	require.ErrorIs(t, err, ErrNotAnIndex)

	entries := []IndexEntry{{Digest: "sha256:aa"}, {Digest: "sha256:bb", Annotations: map[string]string{"k": "v"}}}
	_, err = client.PushIndex(ctx, ref, "application/vnd.example.bundle", entries, nil)
	require.NoError(t, err)

	got, err := client.FetchIndex(ctx, ref)
	require.NoError(t, err)
	assert.Equal(t, entries, got)
}
