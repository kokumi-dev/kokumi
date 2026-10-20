package promotion

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

var update = flag.Bool("update", false, "update golden files")

// assertGolden compares statement with the indented golden file name.
func assertGolden(t *testing.T, name string, statement []byte) {
	t.Helper()

	var indented bytes.Buffer
	require.NoError(t, json.Indent(&indented, statement, "", "  "))
	indented.WriteString("\n")

	golden := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(golden, indented.Bytes(), 0o600))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), indented.String())
}

func TestPromotionStatement(t *testing.T) {
	previous := healthyEntry("podinfo-prod-9f", revision1, t0)
	pending := History{previous}.Open(candidate("podinfo-prod-c3", revision2), t0.Add(48*time.Hour+700*time.Millisecond))[0]

	gates := Gates{
		Approvals: &ApprovalsEvidence{Attestation: record1},
		After: StageEvidenceOf("shop", []Stage{{Order: "podinfo-staging", SoakSeconds: 86400}}, Result{
			Outcome:   OutcomeSatisfied,
			Qualified: []Qualified{{Entry: healthyEntry("podinfo-staging-77", revision2, t0.Add(24*time.Hour))}},
		}),
		Checks: &ChecksEvidence{Vulnerabilities: &VulnerabilitiesEvidence{
			MaxSeverity: "High",
			Images:      []ImageEvidence{{Name: "ghcr.io/stefanprodan/podinfo", Digest: revision1, Report: record2}},
		}},
	}

	statement, err := PromotionStatement("shop/podinfo-prod", "Automatic", pending, &previous, gates)
	require.NoError(t, err)
	assertGolden(t, "promotion.golden.json", statement)

	again, err := PromotionStatement("shop/podinfo-prod", "Automatic", pending, &previous, gates)
	require.NoError(t, err)
	assert.Equal(t, statement, again, "a retry builds the same bytes")

	first, err := PromotionStatement("shop/podinfo-prod", "Manual", pending, nil, Gates{})
	require.NoError(t, err)
	assert.NotContains(t, string(first), `"previous"`)
	assert.NotContains(t, string(first), `"approvals"`)
}

func TestDeploymentStatement(t *testing.T) {
	healthy := healthyEntry("podinfo-staging-77", revision2, t0.Add(24*time.Hour))
	healthy.DeploymentRecord = ""
	targets := []Target{{Server: "https://kubernetes.default.svc", Namespace: "shop"}}

	statement, err := DeploymentStatement(stagingKey, healthy, targets)
	require.NoError(t, err)
	assertGolden(t, "deployment.golden.json", statement)

	_, err = DeploymentStatement(stagingKey, candidate("podinfo-staging-77", revision2), targets)
	assert.Error(t, err, "a deployment that was never healthy has no record")
}

func TestVerifyDeployment(t *testing.T) {
	healthy := healthyEntry("podinfo-staging-77", revision2, t0)
	statement, err := DeploymentStatement(stagingKey, healthy, nil)
	require.NoError(t, err)
	var decoded struct {
		Predicate DeploymentPredicate `json:"predicate"`
	}
	require.NoError(t, json.Unmarshal(statement, &decoded))

	require.NoError(t, VerifyDeployment(decoded.Predicate, stagingKey, healthy))

	mismatches := map[string]func(e *Entry) string{
		"order":    func(*Entry) string { return "shop/podinfo-qa" },
		"digest":   func(e *Entry) string { e.Digest = revision1; return stagingKey },
		"revision": func(e *Entry) string { e.ProductRevision = revision1; return stagingKey },
		"healthy":  func(e *Entry) string { e.Healthy = e.Healthy.Add(time.Second); return stagingKey },
	}
	for name, mismatch := range mismatches {
		t.Run(name, func(t *testing.T) {
			e := healthy
			order := mismatch(&e)
			assert.ErrorIs(t, VerifyDeployment(decoded.Predicate, order, e), ErrRecordMismatch)
		})
	}
}

func TestVulnerabilitiesEvidenceOf(t *testing.T) {
	const image, report = "sha256:5555", "sha256:7777"
	scanned := vulnerability.Assessment{
		Name: "nginx", Digest: image, Vendored: true,
		Scan: &vulnerability.Scan{Repository: "registry.example/shop/nginx", Digest: image, Report: report}}
	unscanned := vulnerability.Assessment{Name: "busybox", Digest: "sha256:6666"}

	got := VulnerabilitiesEvidenceOf("High", []vulnerability.Assessment{scanned, unscanned})
	assert.Equal(t, &VulnerabilitiesEvidence{
		MaxSeverity: "High",
		Images:      []ImageEvidence{{Name: "nginx", Digest: image, Report: report}},
	}, got)

	assert.Equal(t, []ImageEvidence{}, VulnerabilitiesEvidenceOf("High", nil).Images, "no images record an empty list, not null")
}
