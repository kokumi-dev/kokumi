package scan

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/sbom"
	"github.com/kokumi-dev/kokumi/internal/scanner"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

const (
	generatedSBOM = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[{"name":"openssl"}]}`
	arm64         = "linux/arm64"
)

// fakeScanner answers scan requests with report, or with err.
type fakeScanner struct {
	report   json.RawMessage
	err      error
	failRef  string
	requests []scanner.Request
}

func (f *fakeScanner) Scan(_ context.Context, req scanner.Request) (*scanner.Response, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	resp := &scanner.Response{
		Scanner:  scanner.ScannerInfo{Name: "trivy", Version: "0.71.0"},
		Database: scanner.DatabaseInfo{Ref: req.Database.Ref, UpdatedAt: t0.Add(-time.Hour)},
	}
	// Like Trivy's, each report records when it was scanned.
	var report map[string]any
	if err := json.Unmarshal(f.report, &report); err != nil {
		return nil, err
	}
	report["metadata"] = map[string]any{"scanStartedOn": t0.Add(time.Duration(len(f.requests)) * time.Minute)}
	stamped, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	for _, img := range req.Images {
		result := scanner.Result{Ref: img.Ref, Report: stamped}
		if img.Ref == f.failRef {
			result = scanner.Result{Ref: img.Ref, Error: "no space left on device"}
		}
		if len(img.SBOM) == 0 {
			result.SBOM = json.RawMessage(generatedSBOM)
		}
		resp.Images = append(resp.Images, result)
	}
	return resp, nil
}

// testImage returns a client that serves a two-platform image index and the
// index by digest.
func testImage(t *testing.T) (*oci.FakeClient, oci.Reference) {
	t.Helper()
	index := digestOf(10)
	client := oci.NewFakeClient(nil)
	client.Manifests = map[string]oci.Manifest{
		index: {
			MediaType: ocispec.MediaTypeImageIndex,
			Digest:    index,
			Manifests: []oci.Descriptor{
				{MediaType: ocispec.MediaTypeImageManifest, Digest: digestOf(11), Platform: "linux/amd64"},
				{MediaType: ocispec.MediaTypeImageManifest, Digest: digestOf(12), Platform: arm64},
			},
		},
	}
	ref, err := oci.Parse("oci://registry.internal/shop/vendor/app/images/nginx@" + index)
	require.NoError(t, err)
	return client, ref
}

func testReport(t *testing.T) json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "vulnerability", "testdata", "report.json"))
	require.NoError(t, err)
	return data
}

func TestPipeline_Run(t *testing.T) {
	ctx := context.Background()
	signer := signingtest.NewSigner(t)
	client, image := testImage(t)
	scans := &fakeScanner{report: testReport(t)}
	pipeline := Pipeline{Scanner: scans, Platform: arm64, Signer: signer, Trust: signer.TrustSet(), Timeout: time.Minute}
	dbs := Databases{DB: scanner.Database{Ref: "oci://mirror.gcr.io/aquasec/trivy-db@" + digestOf(1)}}
	target := Target{Client: client, Connection: scanner.Connection{Host: "registry.internal"}, Images: []oci.Reference{image}}

	result, err := pipeline.Run(ctx, target, dbs, t0)
	require.NoError(t, err)
	require.Empty(t, result.Failures)
	require.Len(t, result.Records, 1)
	assert.Equal(t, "trivy", result.Scanner.Name)
	rec := result.Records[0]
	assert.Equal(t, image.Digest, rec.Digest)
	assert.Equal(t, "oci://registry.internal/shop/vendor/app/images/nginx", rec.Repository)
	assert.Equal(t, arm64, rec.Platform, "the preferred platform of the index")
	assert.Equal(t, t0, rec.ScannedAt)
	assert.Equal(t, sbom.OriginGenerated, rec.SBOM.Origin)
	assert.Equal(t, vulnerability.Summary{Critical: 1, High: 2, Medium: 1, Unknown: 1, Fixable: 3}, rec.Summary)

	require.Len(t, scans.requests, 1)
	assert.Equal(t, []scanner.Image{{Ref: image.OCIString(), Platform: arm64}}, scans.requests[0].Images, "no SBOM to reuse yet")

	verified, err := attestation.VerifyReferrer(ctx, client, signer.TrustSet(), image, rec.Report, vulnerability.PredicateType)
	require.NoError(t, err, "the report is signed and attached")
	var report vulnerability.Report
	require.NoError(t, verified.Predicate(&report))
	assert.NotContains(t, string(report.Scanner.Result), "PrimaryURL", "the report is trimmed")
	assert.Equal(t, "oci://mirror.gcr.io/aquasec/trivy-db@"+digestOf(1), report.Scanner.DB.URI)
	params, err := json.Marshal(report.Invocation.Parameters)
	require.NoError(t, err)
	assert.JSONEq(t, `{"sbom":{"digest":"`+rec.SBOM.Digest+`","origin":"Generated"},"platform":"linux/arm64"}`, string(params))

	t.Run("a second scan reuses Kokumi's SBOM", func(t *testing.T) {
		again, err := pipeline.Run(ctx, target, dbs, t0.Add(24*time.Hour))
		require.NoError(t, err)
		require.Len(t, again.Records, 1)
		assert.Equal(t, rec.SBOM, again.Records[0].SBOM, "the same SBOM")
		assert.JSONEq(t, generatedSBOM, string(scans.requests[1].Images[0].SBOM))
		assert.NotEqual(t, rec.Report, again.Records[0].Report, "a new report")
	})

	t.Run("an image that fails", func(t *testing.T) {
		failing := &fakeScanner{report: testReport(t), failRef: image.OCIString()}
		result, err := Pipeline{Scanner: failing, Platform: arm64, Signer: signer, Trust: signer.TrustSet(), Timeout: time.Minute}.Run(ctx, target, dbs, t0)
		require.NoError(t, err)
		assert.Empty(t, result.Records)
		require.Len(t, result.Failures, 1)
		assert.Equal(t, image.Digest+": no space left on device", result.Failures[0].String())
	})

	t.Run("a scanner that cannot be called", func(t *testing.T) {
		busy := &scanner.UnavailableError{RetryAfter: 30 * time.Second, Err: errors.New("busy")}
		_, err := Pipeline{Scanner: &fakeScanner{err: busy}, Signer: signer, Trust: signer.TrustSet(), Timeout: time.Minute}.Run(ctx, target, dbs, t0)
		require.ErrorAs(t, err, &busy)
	})

	t.Run("an image that cannot be read is no call", func(t *testing.T) {
		missing, err := oci.Parse("oci://registry.internal/shop/vendor/app/images/nginx@" + digestOf(99))
		require.NoError(t, err)
		calls := len(scans.requests)
		result, err := pipeline.Run(ctx, Target{Client: client, Images: []oci.Reference{missing}}, dbs, t0)
		require.NoError(t, err)
		require.Len(t, result.Failures, 1)
		assert.Len(t, scans.requests, calls)
	})
}

func TestResolveDatabases(t *testing.T) {
	ctx := context.Background()
	client := oci.NewFakeClient(nil)
	connect := func(string) (oci.Client, scanner.Connection, error) { return client, scanner.Connection{}, nil }

	dbs, javaErr, err := ResolveDatabases(ctx, connect, "oci://mirror.gcr.io/aquasec/trivy-db", "oci://mirror.gcr.io/aquasec/trivy-java-db@"+digestOf(2))
	require.NoError(t, err)
	require.NoError(t, javaErr)
	assert.Equal(t, "oci://mirror.gcr.io/aquasec/trivy-db@"+oci.FakeDigest, dbs.DB.Ref, "the schema tag, resolved")
	require.NotNil(t, dbs.JavaDB)
	assert.Equal(t, "oci://mirror.gcr.io/aquasec/trivy-java-db@"+digestOf(2), dbs.JavaDB.Ref, "a digest is kept")
	require.Len(t, client.Resolves, 1)
	assert.Equal(t, DBTag, client.Resolves[0].Tag)

	java, err := oci.Parse("oci://mirror.gcr.io/aquasec/trivy-java-db:1")
	require.NoError(t, err)
	client.ResolveErrs = map[string]error{java.String(): errors.New("unauthorized")}
	dbs, javaErr, err = ResolveDatabases(ctx, connect, "oci://mirror.gcr.io/aquasec/trivy-db", "oci://mirror.gcr.io/aquasec/trivy-java-db")
	require.NoError(t, err, "images without Java archives scan without it")
	require.ErrorContains(t, javaErr, "unauthorized")
	assert.Nil(t, dbs.JavaDB)

	failing := func(string) (oci.Client, scanner.Connection, error) {
		return nil, scanner.Connection{}, errors.New("registry \"mirror\" not found")
	}
	_, _, err = ResolveDatabases(ctx, failing, "oci://mirror.gcr.io/aquasec/trivy-db", "")
	require.ErrorContains(t, err, "vulnerability database")
}
