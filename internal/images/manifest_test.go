package images

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kokumi-dev/kokumi/internal/renderer"
)

const workloads = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      initContainers:
        - name: migrate
          image: ghcr.io/example/migrate:2.0
      containers:
        - name: web
          image: nginx:1.27
        - name: sidecar
          image: nginx:1.27
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: backup
spec:
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: backup
              image: registry.example.com/tools/backup@sha256:1111111111111111111111111111111111111111111111111111111111111111
---
apiVersion: argoproj.io/v1alpha1
kind: Rollout
metadata:
  name: canary
spec:
  template:
    spec:
      containers:
        - name: app
          image: ghcr.io/example/app
---
apiVersion: v1
kind: Pod
metadata:
  name: debug
spec:
  containers:
    - name: main
      image: busybox
      imagePullPolicy: Always
  ephemeralContainers:
    - name: shell
      image: alpine:3.20
  volumes:
    - name: data
      image:
        reference: ghcr.io/example/data:1
    - name: scratch
      emptyDir: {}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
data:
  image: not-an-image-field
`

func TestParseManifest_findsDefaultImageFields(t *testing.T) {
	manifest, err := ParseManifest([]byte(workloads), nil)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"ghcr.io/example/migrate:2.0",
		nginxRef,
		"registry.example.com/tools/backup@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		appName,
		"busybox",
		"alpine:3.20",
		"ghcr.io/example/data:1",
	}, manifest.References())
}

func TestParseManifest_skipsCustomResourceDefinitionsAndEmptyFields(t *testing.T) {
	content := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: things.example.com
spec:
  versions:
    - name: v1
      schema:
        openAPIV3Schema:
          properties:
            containers:
              - image: schema-example
---
apiVersion: v1
kind: Pod
metadata:
  name: unset
spec:
  containers:
    - name: a
      image: null
    - name: b
      image: ""
---
# A comment-only document
`
	manifest, err := ParseManifest([]byte(content), nil)
	require.NoError(t, err)
	assert.Empty(t, manifest.References())
}

func TestParseManifest_fieldSpecs(t *testing.T) {
	content := `apiVersion: monitoring.coreos.com/v1
kind: Prometheus
metadata:
  name: main
spec:
  image: quay.io/prometheus/prometheus:v3.0.0
  sidecars:
    - image: quay.io/thanos/thanos:v0.36.0
---
apiVersion: other.example.com/v1
kind: Prometheus
metadata:
  name: lookalike
spec:
  image: should-not-match
`
	specs := []FieldSpec{
		{Group: monitoringGroup, Kind: prometheusKind, Path: "spec/image"},
		{Group: monitoringGroup, Kind: prometheusKind, Path: "spec/sidecars[]/image"},
		{Group: monitoringGroup, Kind: prometheusKind, Path: "spec/missing/image"},
	}

	manifest, err := ParseManifest([]byte(content), specs)
	require.NoError(t, err)
	assert.Equal(t, []string{"quay.io/prometheus/prometheus:v3.0.0", "quay.io/thanos/thanos:v0.36.0"}, manifest.References())
}

func TestParseManifest_fieldSpecOnObjectFails(t *testing.T) {
	content := `apiVersion: v1
kind: Pod
metadata:
  name: web
spec:
  containers:
    - name: web
      image: nginx
`
	_, err := ParseManifest([]byte(content), []FieldSpec{{Kind: "Pod", Path: "spec"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not locate a string")
}

func TestManifest_rewriteKeepsTheFormattingOfTheRenderPipeline(t *testing.T) {
	normalized, err := renderer.NormalizeYAML([]byte(workloads))
	require.NoError(t, err)

	manifest, err := ParseManifest(normalized, nil)
	require.NoError(t, err)

	unchanged, err := manifest.Encode()
	require.NoError(t, err)
	assert.Equal(t, string(normalized), string(unchanged), "encoding without rewrites must not change the manifest")

	assert.True(t, manifest.Rewrite(map[string]string{"nginx:1.27": "nginx:1.27@sha256:2222222222222222222222222222222222222222222222222222222222222222"}))
	assert.False(t, manifest.Rewrite(map[string]string{"nginx:1.27": "nginx:1.27@sha256:2222222222222222222222222222222222222222222222222222222222222222"}))

	rewritten, err := manifest.Encode()
	require.NoError(t, err)
	assert.Contains(t, string(rewritten), "image: nginx:1.27@sha256:2222222222222222222222222222222222222222222222222222222222222222")
	assert.NotContains(t, string(rewritten), "image: nginx:1.27\n")
	assert.Contains(t, string(rewritten), "image: busybox\n")
}

func TestParseManifest_invalidYAML(t *testing.T) {
	_, err := ParseManifest([]byte("kind: [unclosed"), nil)
	require.Error(t, err)
}
