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
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/images"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
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
			Verified: &artifact.ConfigVerification{
				Provider: "cosign",
				Issuer:   "https://token.actions.githubusercontent.com",
				Subject:  "https://github.com/stefanprodan/podinfo/.github/workflows/release.yml@refs/tags/6.10.2",
			},
		},
		Destination: artifact.ConfigDestination{OCI: "oci://registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod"},
		Product:     &artifact.ConfigProduct{Namespace: "shop", Name: "podinfo", ConfigHash: "sha256:77e0"},
		Renderer:    artifact.ConfigRenderer{Type: "Helm", Version: "v4.3.0", Builder: "ghcr.io/kokumi-dev/kokumi@sha256:bb"},
		Images: []images.Image{
			{
				Ref:       "busybox@" + busyboxDigest,
				Name:      "docker.io/library/busybox",
				Digest:    busyboxDigest,
				MediaType: "application/vnd.oci.image.index.v1+json",
				Vendored:  "registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod@" + busyboxDigest,
			},
			{
				Ref:       "ghcr.io/stefanprodan/podinfo:6.10.2",
				Name:      "ghcr.io/stefanprodan/podinfo",
				Digest:    podinfoDigest,
				MediaType: "application/vnd.oci.image.index.v1+json",
				Vendored:  "registry.kokumi.svc.cluster.local:5000/shop/podinfo-prod@" + podinfoDigest,
			},
		},
	}
}

const (
	busyboxDigest = "sha256:d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1d1"
	podinfoDigest = "sha256:d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4"
)

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

func TestStatement_leavesOutUnresolvedImages(t *testing.T) {
	cfg := testConfig()
	cfg.Images = []images.Image{{Ref: "ghcr.io/example/missing:1", Name: "ghcr.io/example/missing", Error: "not found"}}

	statement, err := Statement(testSubject(t), cfg)
	require.NoError(t, err)

	var parsed struct {
		Predicate Predicate `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(statement, &parsed))
	require.Len(t, parsed.Predicate.BuildDefinition.ResolvedDependencies, 1, "only the source")
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

func TestStatement_unverifiedSource(t *testing.T) {
	cfg := testConfig()
	cfg.Source.Verified = nil

	statement, err := Statement(testSubject(t), cfg)
	require.NoError(t, err)

	var parsed struct {
		Predicate Predicate `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(statement, &parsed))
	require.Empty(t, parsed.Predicate.BuildDefinition.ResolvedDependencies[0].Annotations)
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
	signer := signingtest.NewSigner(t)

	data, err := json.Marshal(testConfig())
	require.NoError(t, err)

	client := &oci.FakeClient{Pushes: []oci.PushedArtifact{{
		Ref:     subject,
		Digest:  subjectDigest,
		Options: oci.PushOptions{Config: data, ConfigMediaType: oci.PreparationConfigMediaType},
	}}}

	cfg, err := ReadConfig(ctx, client, subject)
	require.NoError(t, err)

	first, err := Attach(ctx, client, signer, subject, cfg)
	require.NoError(t, err)
	require.Len(t, client.Referrers, 1)

	referrer := client.Referrers[0].Artifact
	require.Equal(t, oci.SigstoreBundleMediaType, referrer.ArtifactType)
	require.Equal(t, PredicateType, referrer.Annotations[oci.AnnotationBundlePredicateType])
	require.Equal(t, "2026-10-12T09:14:03Z", referrer.Annotations["org.opencontainers.image.created"])

	evidence, err := attestation.Find(ctx, client, signer.TrustSet(), subject, PredicateType)
	require.NoError(t, err)
	require.Equal(t, first, evidence.Digest)

	t.Run("attaching again reuses the referrer", func(t *testing.T) {
		second, err := Attach(ctx, client, signer, subject, cfg)
		require.NoError(t, err)
		require.Equal(t, first, second)
		require.Len(t, client.Referrers, 1)
	})

	t.Run("an artifact without a Kokumi config is rejected", func(t *testing.T) {
		plain := &oci.FakeClient{Pushes: []oci.PushedArtifact{{
			Ref:     subject,
			Digest:  subjectDigest,
			Options: oci.PushOptions{Config: []byte("{}"), ConfigMediaType: "application/vnd.oci.empty.v1+json"},
		}}}

		_, err := ReadConfig(ctx, plain, subject)
		require.ErrorIs(t, err, ErrInvalidConfig)
		require.Empty(t, plain.Referrers)
	})
}
