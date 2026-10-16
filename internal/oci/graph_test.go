package oci

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry starts an in-memory registry and returns its host.
func newTestRegistry(t *testing.T, referrersAPI bool) string {
	t.Helper()

	server := httptest.NewServer(ggcrregistry.New(ggcrregistry.WithReferrersSupport(referrersAPI), ggcrregistry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

// pushTestArtifact pushes a one-file artifact to ref and returns ref by digest.
func pushTestArtifact(t *testing.T, client Client, ref Reference, content string) Reference {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(content), 0600))

	digest, err := client.Push(context.Background(), ref, dir, PushOptions{ArtifactType: PreparationArtifactType})
	require.NoError(t, err)

	ref.Digest = digest
	return ref
}

// attach pushes a referrer of subject and returns it by digest.
func attach(t *testing.T, client Client, subject Reference, artifactType, created string) Reference {
	t.Helper()

	digest, err := client.PushReferrer(context.Background(), subject, ReferrerArtifact{
		ArtifactType: artifactType,
		MediaType:    "application/json",
		Payload:      fmt.Appendf(nil, `{"type":%q,"created":%q}`, artifactType, created),
		Annotations:  map[string]string{ocispec.AnnotationCreated: created},
	})
	require.NoError(t, err)

	ref := subject
	ref.Tag = ""
	ref.Digest = digest
	return ref
}

func TestWalk(t *testing.T) {
	ctx := context.Background()
	client := NewORASClient()
	host := newTestRegistry(t, true)

	ref, err := Parse("oci://" + host + "/shop/podinfo:1.0.0")
	require.NoError(t, err)

	t.Run("referrers of referrers", func(t *testing.T) {
		root := pushTestArtifact(t, client, ref, "kind: ConfigMap\n")
		signature := attach(t, client, root, SigstoreBundleMediaType, "2026-10-12T09:00:00Z")
		sbom := attach(t, client, root, "application/spdx+json", "2026-10-12T10:00:00Z")
		sbomSignature := attach(t, client, sbom, SigstoreBundleMediaType, "2026-10-12T11:00:00Z")

		graph, err := Walk(ctx, client, root)
		require.NoError(t, err)

		assert.Equal(t, "oci://"+host+"/shop/podinfo", graph.Repository)
		assert.Equal(t, "1.0.0", graph.Tag)
		assert.Equal(t, root.Digest, graph.TagDigest)
		assert.Equal(t, root.Digest, graph.Root.Digest)
		assert.Equal(t, PreparationArtifactType, graph.Root.ArtifactType)
		require.Len(t, graph.Root.Layers, 1)
		assert.NoError(t, graph.Root.Err)

		require.Len(t, graph.Root.Referrers, 2)
		assert.Equal(t, signature.Digest, graph.Root.Referrers[0].Digest, "referrers are ordered oldest first")
		assert.Equal(t, LinkSubject, graph.Root.Referrers[0].Link)
		require.NotNil(t, graph.Root.Referrers[0].Subject)
		assert.Equal(t, root.Digest, graph.Root.Referrers[0].Subject.Digest)

		sbomNode := graph.Root.Referrers[1]
		assert.Equal(t, sbom.Digest, sbomNode.Digest)
		assert.Equal(t, "application/spdx+json", sbomNode.ArtifactType)
		require.Len(t, sbomNode.Referrers, 1)
		assert.Equal(t, sbomSignature.Digest, sbomNode.Referrers[0].Digest)
		assert.Equal(t, sbom.Digest, sbomNode.Referrers[0].Subject.Digest)
		assert.False(t, graph.Root.Truncated)
	})

	t.Run("cosign legacy tags", func(t *testing.T) {
		legacyRef := ref
		legacyRef.Repository = "charts/legacy"
		root := pushTestArtifact(t, client, legacyRef, "kind: Secret\n")

		sigRef := legacyRef
		sigRef.Tag = "sha256-" + strings.TrimPrefix(root.Digest, "sha256:") + ".sig"
		signature := pushTestArtifact(t, client, sigRef, "signature")

		graph, err := Walk(ctx, client, root)
		require.NoError(t, err)

		require.Len(t, graph.Root.Referrers, 1)
		node := graph.Root.Referrers[0]
		assert.Equal(t, signature.Digest, node.Digest)
		assert.Equal(t, LinkTag, node.Link)
		assert.Equal(t, sigRef.Tag, node.Tag)
	})

	t.Run("index children", func(t *testing.T) {
		indexRef := ref
		indexRef.Repository = "images/multi"
		amd64Ref := indexRef
		amd64Ref.Tag = "amd64"
		amd64 := pushTestArtifact(t, client, amd64Ref, "amd64")
		arm64Ref := indexRef
		arm64Ref.Tag = "arm64"
		arm64 := pushTestArtifact(t, client, arm64Ref, "arm64")
		signature := attach(t, client, arm64, SigstoreBundleMediaType, "2026-10-12T09:00:00Z")

		indexRef.Tag = "1.0.0"
		_, err := client.PushIndex(ctx, indexRef, "", []Descriptor{{Digest: amd64.Digest}, {Digest: arm64.Digest}}, nil)
		require.NoError(t, err)

		graph, err := Walk(ctx, client, indexRef)
		require.NoError(t, err)

		assert.Equal(t, ocispec.MediaTypeImageIndex, graph.Root.MediaType)
		assert.Equal(t, graph.Root.Digest, graph.TagDigest)
		require.Len(t, graph.Root.Children, 2)
		assert.Equal(t, amd64.Digest, graph.Root.Children[0].Digest)
		assert.Equal(t, LinkIndex, graph.Root.Children[0].Link)
		require.Len(t, graph.Root.Children[1].Referrers, 1)
		assert.Equal(t, signature.Digest, graph.Root.Children[1].Referrers[0].Digest)
	})

	t.Run("moved tag", func(t *testing.T) {
		movedRef := ref
		movedRef.Repository = "shop/moved"
		first := pushTestArtifact(t, client, movedRef, "first")
		second := pushTestArtifact(t, client, movedRef, "second")

		graph, err := Walk(ctx, client, first)
		require.NoError(t, err)

		assert.Equal(t, first.Digest, graph.Root.Digest)
		assert.Equal(t, second.Digest, graph.TagDigest)
	})

	t.Run("depth limit", func(t *testing.T) {
		deepRef := ref
		deepRef.Repository = "shop/deep"
		subject := pushTestArtifact(t, client, deepRef, "deep")
		for range maxGraphDepth + 1 {
			subject = attach(t, client, subject, SigstoreBundleMediaType, "2026-10-12T09:00:00Z")
		}

		graph, err := Walk(ctx, client, pushedRoot(deepRef))
		require.NoError(t, err)

		node := graph.Root
		for range maxGraphDepth {
			require.Len(t, node.Referrers, 1)
			assert.False(t, node.Truncated)
			node = node.Referrers[0]
		}
		assert.Empty(t, node.Referrers)
		assert.True(t, node.Truncated)
	})

	t.Run("node limit", func(t *testing.T) {
		wideRef := ref
		wideRef.Repository = "shop/wide"
		root := pushTestArtifact(t, client, wideRef, "wide")
		for i := range maxGraphNodes {
			attach(t, client, root, SigstoreBundleMediaType, fmt.Sprintf("2026-10-12T09:%02d:%02dZ", i/60, i%60))
		}

		graph, err := Walk(ctx, client, root)
		require.NoError(t, err)

		assert.Len(t, graph.Root.Referrers, maxGraphNodes-1)
		assert.True(t, graph.Root.Truncated)
	})
}

// pushedRoot returns the reference by tag of an artifact pushed to ref.
func pushedRoot(ref Reference) Reference {
	ref.Digest = ""
	return ref
}

func TestWalk_ReferrersTagSchema(t *testing.T) {
	ctx := context.Background()
	client := NewORASClient()
	host := newTestRegistry(t, false)

	ref, err := Parse("oci://" + host + "/shop/podinfo:1.0.0")
	require.NoError(t, err)
	root := pushTestArtifact(t, client, ref, "kind: ConfigMap\n")
	signature := attach(t, client, root, SigstoreBundleMediaType, "2026-10-12T09:00:00Z")

	graph, err := Walk(ctx, client, root)
	require.NoError(t, err)

	// The referrers index under the sha256-<hex> tag is how the referrers
	// are found, not a node of its own.
	require.Len(t, graph.Root.Referrers, 1)
	assert.Equal(t, signature.Digest, graph.Root.Referrers[0].Digest)
	assert.Equal(t, LinkSubject, graph.Root.Referrers[0].Link)
}
