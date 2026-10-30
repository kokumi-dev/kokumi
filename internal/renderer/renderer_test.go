package renderer_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"runtime/metrics"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/kokumi-dev/kokumi/internal/renderer"
)

const (
	kindDeployment = "Deployment"
	webName        = "web"
	imageValue     = "nginx:1.28"
	replicasPath   = ".spec.replicas"
)

const deployments = `apiVersion: apps/v1
kind: Deployment
metadata:
    name: web
    namespace: shop
spec:
    replicas: 1
    template:
        spec:
            containers:
                - name: web
                  image: nginx:1.27
---
# The worker scales on its own.
apiVersion: apps/v1
kind: Deployment
metadata:
    name: worker
    namespace: shop
spec:
    replicas: 1
`

const normalizedDeployments = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
---
# The worker scales on its own.
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker
  namespace: shop
spec:
  replicas: 1
`

func TestProcess_normalizesWithoutPatches(t *testing.T) {
	out, err := renderer.NewPatcher(nil).Process(t.Context(), []byte(deployments))
	require.NoError(t, err)
	assert.Equal(t, normalizedDeployments, string(out))

	again, err := renderer.NewPatcher(nil).Process(t.Context(), out)
	require.NoError(t, err)
	assert.Equal(t, string(out), string(again), "normalized output stays as it is")
}

// TestProcess_matchesOneEncoderForTheWholeStream pins that encoding each
// document on its own, joined by "---", writes the same bytes as one encoder
// for all documents, so artifact digests do not depend on how Process
// encodes.
func TestProcess_matchesOneEncoderForTheWholeStream(t *testing.T) {
	tests := map[string]string{
		"deployments":           deployments,
		"keep chomping":         "a: |+\n  text\n\n\n---\nb: |+\n  text\n\n",
		"comments at the seams": "a: 1\n# foot of a\n---\n# head of b\nb: 2\n# end of stream\n",
		"comment-only document": "a: 1\n---\n# only a comment\n---\nb: 2\n",
		"empty documents":       "---\n---\na: 1\n---\n---\n",
		"end markers":           "a: 1\n...\n---\nb: 2\n...\n",
		"root scalars":          "plain\n---\n\"quoted\"\n---\n- a\n- b\n",
		"flow style":            "{a: 1, b: [1, 2]}\n---\n[x, y]\n",
		"helm output":           "---\n# Source: chart/templates/a.yaml\napiVersion: v1\nkind: ConfigMap\ndata:\n  script: |\n    echo hi\n\n---\n# Source: chart/templates/empty.yaml\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			var want bytes.Buffer
			encoder := yaml.NewEncoder(&want)
			encoder.SetIndent(2)
			decoder := yaml.NewDecoder(strings.NewReader(content))
			for {
				var doc yaml.Node
				err := decoder.Decode(&doc)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				require.NoError(t, encoder.Encode(&doc))
			}
			require.NoError(t, encoder.Close())

			got, err := renderer.NewPatcher(nil).Process(t.Context(), []byte(content))
			require.NoError(t, err)
			assert.Equal(t, want.String(), string(got))
		})
	}
}

func TestProcess_recordsTargetsThatMatchNothing(t *testing.T) {
	missing := renderer.PatchTarget{Kind: kindDeployment, Name: "missing"}
	otherNamespace := renderer.PatchTarget{Kind: kindDeployment, Name: webName, Namespace: "other"}
	patcher := renderer.NewPatcher([]renderer.Patch{
		{Target: missing, Set: map[string]string{replicasPath: "3"}},
		{Target: renderer.PatchTarget{Kind: kindDeployment, Name: "worker"}, Set: map[string]string{replicasPath: "3"}},
		{Target: otherNamespace, Set: map[string]string{replicasPath: "3"}},
	})

	out, err := patcher.Process(t.Context(), []byte(normalizedDeployments))
	require.NoError(t, err, "a target that matches nothing in one manifest may match in the next")
	assert.Contains(t, string(out), "name: worker\n  namespace: shop\nspec:\n  replicas: 3\n")

	unmatched := patcher.Unmatched()
	require.Len(t, unmatched, 2)
	assert.Equal(t, missing, unmatched[0].Target)
	assert.EqualError(t, unmatched[0], "target Deployment/missing matches no resource")
	assert.EqualError(t, unmatched[1], "target other/Deployment/web matches no resource")

	_, err = patcher.Process(t.Context(), []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: missing\n"))
	require.NoError(t, err)
	assert.Len(t, patcher.Unmatched(), 1, "matches add up across manifests")
}

func TestProcess_appliesPatchesToTheirTargets(t *testing.T) {
	patcher := renderer.NewPatcher([]renderer.Patch{
		{
			Target: renderer.PatchTarget{Kind: kindDeployment, Name: webName, Namespace: "shop"},
			Set: map[string]string{
				replicasPath: "3",
				".spec.template.spec.containers[0].image":  imageValue,
				".spec.template.spec.nodeSelector.example": "true",
			},
		},
	})
	out, err := patcher.Process(t.Context(), []byte(deployments))
	require.NoError(t, err)
	assert.Empty(t, patcher.Unmatched())

	web, worker, found := strings.Cut(string(out), "---\n")
	require.True(t, found)
	assert.Contains(t, web, "  replicas: 3\n")
	assert.Contains(t, web, "image: "+imageValue+"\n")
	assert.Contains(t, web, "nodeSelector:\n        example: true\n")
	assert.Contains(t, worker, "  replicas: 1\n", "patches change only their target")
}

func TestProcess_failsOnAPathThatCannotBeSet(t *testing.T) {
	tests := map[string]struct {
		set  map[string]string
		path string
		want string
	}{
		"index out of bounds": {
			set:  map[string]string{".spec.template.spec.containers[5].image": imageValue},
			path: ".spec.template.spec.containers[5].image",
			want: "index 5 is out of bounds for containers, which has 1 items",
		},
		"through a scalar": {
			set:  map[string]string{".spec.replicas.count": "1"},
			path: ".spec.replicas.count",
			want: `cannot set "count" in a scalar, only in a mapping`,
		},
		"through a list": {
			set:  map[string]string{".spec.template.spec.containers.image": imageValue},
			path: ".spec.template.spec.containers.image",
			want: `cannot set "image" in a list, only in a mapping`,
		},
		"index into a mapping": {
			set:  map[string]string{".spec.template[0]": "x"},
			path: ".spec.template[0]",
			want: "field template is a mapping, not a list",
		},
		"index into a missing list": {
			set:  map[string]string{".spec.template.spec.volumes[0].name": "data"},
			path: ".spec.template.spec.volumes[0].name",
			want: "list volumes not found",
		},
		"malformed index": {
			set:  map[string]string{".spec.template.spec.containers[first].image": imageValue},
			path: ".spec.template.spec.containers[first].image",
			want: `non-integer array index in "containers[first]"`,
		},
		"conflicting paths, in sorted order": {
			set:  map[string]string{".spec.replicas.max": "2", replicasPath: "1"},
			path: ".spec.replicas.max",
			want: `cannot set "max" in a scalar, only in a mapping`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			target := renderer.PatchTarget{Kind: kindDeployment, Name: webName}

			out, err := renderer.NewPatcher([]renderer.Patch{{Target: target, Set: tt.set}}).Process(t.Context(), []byte(deployments))
			require.Error(t, err)
			assert.Nil(t, out, "a manifest whose patch does not apply is never returned")

			patchErr, ok := errors.AsType[*renderer.PatchError](err)
			require.True(t, ok, "%v is a PatchError", err)
			assert.Equal(t, target, patchErr.Target)
			assert.Equal(t, 1, patchErr.Document)
			assert.Equal(t, tt.path, patchErr.Path)
			assert.Contains(t, err.Error(), fmt.Sprintf("document 1 (Deployment/web): path %q: %s", tt.path, tt.want))
		})
	}
}

func TestProcess_failsOnASyntaxErrorWithTheDocumentIndex(t *testing.T) {
	content := deployments + "---\nkind: [unclosed\n---\napiVersion: v1\nkind: ConfigMap\n"

	out, err := renderer.NewPatcher(nil).Process(t.Context(), []byte(content))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "document 3:")
	assert.Nil(t, out, "a manifest that does not parse is never returned truncated")
}

func TestProcess_withoutDocuments(t *testing.T) {
	for _, content := range []string{"", "# Only a comment\n"} {
		out, err := renderer.NewPatcher(nil).Process(t.Context(), []byte(content))
		require.NoError(t, err)
		assert.Empty(t, out)
	}
}

// operatorManifest returns a multi-document manifest like a rendered
// operator chart: one large CustomResourceDefinition of about 600 KiB, a
// dozen smaller ones and small objects around them. The large one is about a
// third of the whole, as in the external-secrets chart.
func operatorManifest() []byte {
	var b strings.Builder

	for i := range 20 {
		fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: config-%d\ndata:\n  key: value-%d\n---\n", i, i)
	}

	writeCRD(&b, "large", 1500)
	for i := range 12 {
		writeCRD(&b, fmt.Sprintf("medium%d", i), 250)
	}

	for i := range 20 {
		fmt.Fprintf(&b, "---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: app-%d\nspec:\n  replicas: 1\n  template:\n    spec:\n      containers:\n        - name: app\n          image: ghcr.io/example/app:%d\n", i, i)
	}

	return []byte(b.String())
}

// writeCRD writes a CustomResourceDefinition with fields schema properties,
// about 400 bytes each.
func writeCRD(b *strings.Builder, name string, fields int) {
	fmt.Fprintf(b, `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: %[1]ss.example.com
spec:
  group: example.com
  names:
    kind: %[1]s
    plural: %[1]ss
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
`, name)
	for i := range fields {
		fmt.Fprintf(b, `            field%d:
              description: Field %d configures one aspect of the thing and is described at length so that the schema grows like a real one.
              type: object
              properties:
                name:
                  type: string
                  maxLength: 253
                values:
                  type: array
                  items:
                    type: string
`, i, i)
	}
	b.WriteString("---\n")
}

// trackPeakHeap samples the heap in use until the returned function is
// called, which reports the most it saw above the starting point as
// peak-heap-B. Sampling every 100µs can miss short spikes.
func trackPeakHeap(b *testing.B) func() {
	b.Helper()

	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	heap := func() uint64 {
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}

	runtime.GC()
	base := heap()

	var peak uint64
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				peak = max(peak, heap())
			}
		}
	}()

	return func() {
		close(stop)
		<-done
		b.ReportMetric(float64(peak-min(peak, base)), "peak-heap-B")
	}
}

func BenchmarkProcess(b *testing.B) {
	content := operatorManifest()
	patches := []renderer.Patch{
		{Target: renderer.PatchTarget{Kind: kindDeployment, Name: "app-3"}, Set: map[string]string{replicasPath: "3"}},
	}

	b.SetBytes(int64(len(content)))
	b.ReportAllocs()
	report := trackPeakHeap(b)

	for b.Loop() {
		if _, err := renderer.NewPatcher(patches).Process(b.Context(), content); err != nil {
			b.Fatal(err)
		}
	}

	report()
}
