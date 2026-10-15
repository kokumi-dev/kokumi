package provenance

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

var update = flag.Bool("update", false, "update golden files")

const subjectDigest = "sha256:c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"

func testConfig() *artifact.Config {
	return &artifact.Config{
		SchemaVersion: artifact.ConfigSchemaVersion,
		Order:         &artifact.ConfigObject{Namespace: "shop", Name: "podinfo-prod", UID: "6f0c"},
		ConfigHash:    "sha256:9b1d",
		CommitMessage: "bump replicas to 3",
		CreatedTime:   "2026-10-12T09:14:03Z",
		Source: artifact.ConfigSource{
			OCI:     "oci://ghcr.io/stefanprodan/charts/podinfo",
			Version: "6.10.2",
			Digest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Destination: artifact.ConfigDestination{OCI: "oci://registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod"},
		Product:     &artifact.ConfigProduct{Namespace: "shop", Name: "podinfo", ConfigHash: "sha256:77e0"},
		Renderer:    artifact.ConfigRenderer{Type: "Helm", Version: "v4.3.0", Builder: "ghcr.io/kokumi-dev/kokumi@sha256:bb"},
	}
}

func testSubject(t *testing.T) oci.Reference {
	t.Helper()

	ref, err := oci.Parse("registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod@" + subjectDigest)
	require.NoError(t, err)

	return ref
}

// indent returns data with its whitespace normalized, so that a golden file
// an editor has reformatted still compares equal. Key order, numbers and
// string escapes are compared as written.
func indent(t *testing.T, data []byte) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, json.Indent(&out, bytes.TrimSpace(data), "", "  "))

	return out.String()
}

func TestStatement_golden(t *testing.T) {
	statement, err := Statement(testSubject(t), testConfig())
	require.NoError(t, err)

	golden := filepath.Join("testdata", "statement.golden.json")
	if *update {
		require.NoError(t, os.WriteFile(golden, []byte(indent(t, statement)+"\n"), 0600))
	}

	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, indent(t, want), indent(t, statement))
}

func TestStatement_product(t *testing.T) {
	cfg := testConfig()

	statement, err := Statement(testSubject(t), cfg)
	require.NoError(t, err)

	var decoded struct {
		Predicate Predicate `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(statement, &decoded))
	params := decoded.Predicate.BuildDefinition.ExternalParameters
	require.Equal(t, "shop/podinfo-prod", params.Order)
	require.Equal(t, "shop/podinfo", params.Product)
	require.Equal(t, cfg.ConfigHash, params.ConfigHash, "the hash of the stored intent, not the Product's own hash")
}

func TestStatement_rejectsUnresolvedSource(t *testing.T) {
	cfg := testConfig()
	cfg.Source.Digest = ""

	_, err := Statement(testSubject(t), cfg)
	require.Error(t, err)
}

func TestAttach(t *testing.T) {
	ctx := context.Background()
	subject := testSubject(t)

	data, err := json.Marshal(testConfig())
	require.NoError(t, err)

	client := &oci.FakeClient{Pushes: []oci.PushedArtifact{{
		Ref:     subject,
		Digest:  subjectDigest,
		Options: oci.PushOptions{Config: data, ConfigMediaType: oci.PreparationConfigMediaType},
	}}}

	first, err := Attach(ctx, client, subject)
	require.NoError(t, err)
	require.Len(t, client.Referrers, 1)

	referrer := client.Referrers[0].Artifact
	require.Equal(t, oci.ProvenanceArtifactType, referrer.ArtifactType)
	require.Equal(t, oci.InTotoStatementMediaType, referrer.MediaType)
	require.Equal(t, "2026-10-12T09:14:03Z", referrer.Annotations["org.opencontainers.image.created"])

	t.Run("attaching again yields the same referrer", func(t *testing.T) {
		second, err := Attach(ctx, client, subject)
		require.NoError(t, err)
		require.Equal(t, first, second)
		require.Equal(t, client.Referrers[0].Artifact, client.Referrers[1].Artifact)
	})

	t.Run("an artifact without a Kokumi config is rejected", func(t *testing.T) {
		plain := &oci.FakeClient{Pushes: []oci.PushedArtifact{{
			Ref:     subject,
			Digest:  subjectDigest,
			Options: oci.PushOptions{Config: []byte("{}"), ConfigMediaType: "application/vnd.oci.empty.v1+json"},
		}}}

		_, err := Attach(ctx, plain, subject)
		require.ErrorIs(t, err, ErrInvalidConfig)
		require.Empty(t, plain.Referrers)
	})
}
