package argocd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
)

const (
	testOrderName   = "my-order"
	testServingName = "my-order"
	testNamespace   = "my-namespace"
	testDigest      = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	statusField     = "status"
	testOptIn       = testNamespace + "/" + testOrderName
)

const testOCIRef = "oci://registry.example/order/app@" + testDigest

// testCluster is the cluster the tests deploy to.
const testCluster = "member"

// keyRevision is the field Argo CD reports the synced revision in.
const keyRevision = "revision"

var testTarget = Target{Cluster: testCluster, Server: "https://member.example:6443", Namespace: "webshop", Project: "prod"}

// withStatus returns the Application of testServing with the status the
// Argo CD Application controller writes once it synced revision without an
// operation in flight and found it in health.
func withStatus(health, sync, revision string) *unstructured.Unstructured {
	app, err := Application(testServing(), "oci://registry.example/order/app@"+revision, testTarget)
	if err != nil {
		app = New()
	}
	app.Object[statusField] = map[string]any{
		"sync": map[string]any{statusField: sync, keyRevision: revision, "comparedTo": map[string]any{
			"source": map[string]any{"repoURL": "oci://registry.example/order/app", "targetRevision": revision, "path": "."},
		}},
		"health": map[string]any{statusField: health, "message": "health message"},
	}
	return app
}

func testServing() *deliveryv1alpha1.Serving {
	return &deliveryv1alpha1.Serving{
		Name:      testServingName,
		Namespace: testNamespace,
		Spec:      deliveryv1alpha1.ServingSpec{OrderName: testOrderName},
	}
}

func servingIn(namespace, order string) *deliveryv1alpha1.Serving {
	return &deliveryv1alpha1.Serving{Name: order, Namespace: namespace, Spec: deliveryv1alpha1.ServingSpec{OrderName: order}}
}

func TestKey(t *testing.T) {
	key := Key(testServing(), cluster.InCluster)
	assert.Equal(t, Namespace, key.Namespace)
	assert.Regexp(t, `^kokumi-my-namespace-my-order-[0-9a-f]{8}$`, key.Name)
	assert.Equal(t, key, Key(testServing(), cluster.InCluster), "the name is stable")

	assert.NotEqual(t, Key(servingIn("a-b", "c"), cluster.InCluster), Key(servingIn("a", "b-c"), cluster.InCluster),
		"Orders that hyphens would join get names of their own")
	assert.NotEqual(t, Key(testServing(), cluster.InCluster), Key(testServing(), testCluster),
		"each cluster gets an Application of its own")

	long := Key(servingIn(strings.Repeat("n", 63), strings.Repeat("o.", 126)+"o"), "member")
	assert.LessOrEqual(t, len(long.Name), 63)
	assert.Empty(t, validation.IsValidLabelValue(long.Name), "Argo CD writes the name into a label")
	assert.Empty(t, validation.IsDNS1123Subdomain(long.Name))
}

func TestApplication(t *testing.T) {
	app, err := Application(testServing(), testOCIRef, testTarget)
	require.NoError(t, err)

	assert.Equal(t, GroupVersionKind, app.GroupVersionKind())
	assert.Equal(t, Key(testServing(), testCluster), types.NamespacedName{Namespace: app.GetNamespace(), Name: app.GetName()})
	assert.Equal(t, testOptIn, app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder], "an Application Kokumi creates is opted in")
	serving, ok := ServingOf(app.GetLabels())
	assert.True(t, ok)
	assert.Equal(t, types.NamespacedName{Namespace: testNamespace, Name: testServingName}, serving)
	assert.Equal(t, testCluster, ClusterOf(app))

	field := func(path ...string) string {
		value, _, _ := unstructured.NestedString(app.Object, path...)
		return value
	}
	assert.Equal(t, "oci://registry.example/order/app", field("spec", "source", "repoURL"))
	assert.Equal(t, testDigest, field("spec", "source", "targetRevision"))
	assert.Equal(t, "prod", field("spec", "project"))
	assert.Equal(t, "https://member.example:6443", field("spec", "destination", "server"))
	assert.Equal(t, "webshop", field("spec", "destination", "namespace"), "every cluster runs the Preparation in its target namespace")

	_, err = Application(testServing(), "", testTarget)
	assert.Error(t, err)
}

func TestMatches(t *testing.T) {
	desired, err := Application(testServing(), testOCIRef, testTarget)
	require.NoError(t, err)

	same := desired.DeepCopy()
	same.Object[statusField] = map[string]any{"sync": map[string]any{statusField: "Synced"}}
	assert.True(t, Matches(same, desired), "the status Argo CD writes is no difference")

	moved := desired.DeepCopy()
	require.NoError(t, unstructured.SetNestedField(moved.Object, "sha256:other", "spec", "source", "targetRevision"))
	assert.False(t, Matches(moved, desired))

	relabeled := desired.DeepCopy()
	relabeled.SetLabels(map[string]string{"team": "a"})
	assert.False(t, Matches(relabeled, desired))

	annotated := desired.DeepCopy()
	annotated.SetAnnotations(map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOptIn, "extra": "x"})
	assert.False(t, Matches(annotated, desired))

	assert.False(t, Matches(New(), desired), "an Application without spec")
}

func TestStateOf(t *testing.T) {
	stale := withStatus("Healthy", "Synced", testDigest)
	require.NoError(t, unstructured.SetNestedField(stale.Object, "sha256:old", "status", "sync", "comparedTo", "source", "targetRevision"))
	uncompared := withStatus("Healthy", "Synced", testDigest)
	unstructured.RemoveNestedField(uncompared.Object, "status", "sync", "comparedTo")
	syncing := withStatus("Healthy", "Synced", testDigest)
	require.NoError(t, unstructured.SetNestedField(syncing.Object, "Running", "status", "operationState", "phase"))
	synced := withStatus("Healthy", "Synced", testDigest)
	require.NoError(t, unstructured.SetNestedMap(synced.Object, map[string]any{
		"phase": "Succeeded", "syncResult": map[string]any{"revision": testDigest},
	}, "status", "operationState"))
	earlierSync := withStatus("Healthy", "Synced", testDigest)
	require.NoError(t, unstructured.SetNestedMap(earlierSync.Object, map[string]any{
		"phase": "Succeeded", "syncResult": map[string]any{"revision": "sha256:old"},
	}, "status", "operationState"))

	tests := []struct {
		name string
		app  *unstructured.Unstructured
		want Phase
	}{
		{name: "missing", want: PhaseMissing},
		{name: "rejected", app: rejected(withStatus("Healthy", "Synced", testDigest)), want: PhaseRejected},
		{name: "synced and healthy", app: withStatus("Healthy", "Synced", testDigest), want: PhaseHealthy},
		{name: "synced and healthy after a sync at the revision", app: synced, want: PhaseHealthy},
		{name: "healthy but out of sync", app: withStatus("Healthy", "OutOfSync", testDigest), want: PhaseProgressing},
		{name: "degraded", app: withStatus("Degraded", "Synced", testDigest), want: PhaseDegraded},
		{name: "progressing", app: withStatus("Progressing", "Synced", testDigest), want: PhaseProgressing},
		{name: "healthy on a comparison of an earlier source", app: stale, want: PhaseProgressing},
		{name: "healthy without a comparison", app: uncompared, want: PhaseProgressing},
		{name: "healthy while a sync runs", app: syncing, want: PhaseProgressing},
		{name: "healthy after a sync of an earlier revision", app: earlierSync, want: PhaseProgressing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := StateOf(tt.app)
			assert.Equal(t, tt.want, state.Phase)
			if tt.want == PhaseRejected {
				assert.Equal(t, testRejection, state.Message, "Argo CD says why")
				return
			}
			if tt.app != nil {
				assert.Equal(t, testDigest, state.Revision)
				assert.Equal(t, "health message", state.Message)
			}
		})
	}
}

// testRejection is the message of an Argo CD InvalidSpecError.
const testRejection = "application destination is not permitted in project"

// rejected adds the condition Argo CD sets on an Application it refuses to
// app.
func rejected(app *unstructured.Unstructured) *unstructured.Unstructured {
	app.Object[statusField].(map[string]any)["conditions"] = []any{
		map[string]any{"type": "InvalidSpecError", "message": testRejection},
	}
	return app
}

func TestOptedIn(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{name: "matching annotation", annotations: map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOptIn}},
		{name: "missing annotation", wantErr: true},
		{name: "the bare Order name", annotations: map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}, wantErr: true},
		{name: "the Order of another namespace", annotations: map[string]string{
			deliveryv1alpha1.AnnotationAllowedOrder: "other/" + testOrderName,
		}, wantErr: true},
		{name: "annotation of another Order", annotations: map[string]string{
			deliveryv1alpha1.AnnotationAllowedOrder: testNamespace + "/other-order",
		}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New()
			app.SetAnnotations(tt.annotations)
			err := OptedIn(app, testNamespace, testOrderName)
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrOptInRequired)
		})
	}
}

func TestServingOf(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{name: "both labels", labels: map[string]string{
			deliveryv1alpha1.LabelServing: testServingName, deliveryv1alpha1.LabelServingNamespace: testNamespace,
		}, want: true},
		{name: "without the serving label", labels: map[string]string{deliveryv1alpha1.LabelServingNamespace: testNamespace}},
		{name: "without the serving-namespace label", labels: map[string]string{deliveryv1alpha1.LabelServing: testServingName}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serving, ok := ServingOf(tt.labels)
			assert.Equal(t, tt.want, ok)
			if tt.want {
				assert.Equal(t, types.NamespacedName{Namespace: testNamespace, Name: testServingName}, serving,
					"the Serving's namespace, not the argocd namespace")
			}
		})
	}
}

func TestChanged(t *testing.T) {
	annotated := func(app *unstructured.Unstructured, annotations map[string]string) *unstructured.Unstructured {
		app.SetAnnotations(annotations)
		return app
	}
	with := func(app *unstructured.Unstructured, value string, path ...string) *unstructured.Unstructured {
		require.NoError(t, unstructured.SetNestedField(app.Object, value, path...))
		return app
	}
	optIn := map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOptIn}

	tests := []struct {
		name     string
		old, new *unstructured.Unstructured
		want     bool
	}{
		{name: "synced revision", old: withStatus("Healthy", "Synced", "sha256:old"), new: withStatus("Healthy", "Synced", "sha256:new"), want: true},
		{name: "health", old: withStatus("Progressing", "Synced", testDigest), new: withStatus("Healthy", "Synced", testDigest), want: true},
		{name: "compared source", old: with(withStatus("Healthy", "Synced", testDigest), "sha256:old", "status", "sync", "comparedTo", "source", "targetRevision"),
			new: withStatus("Healthy", "Synced", testDigest), want: true},
		{name: "operation finished", old: with(withStatus("Healthy", "Synced", testDigest), "Running", "status", "operationState", "phase"),
			new: with(withStatus("Healthy", "Synced", testDigest), "Succeeded", "status", "operationState", "phase"), want: true},
		{name: "opt-in added", old: annotated(withStatus("Healthy", "Synced", testDigest), nil),
			new: annotated(withStatus("Healthy", "Synced", testDigest), optIn), want: true},
		{name: "opt-in for another Order", old: annotated(withStatus("Healthy", "Synced", testDigest),
			map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: "other/other-order"}),
			new: annotated(withStatus("Healthy", "Synced", testDigest), optIn), want: true},
		{name: "opt-in removed", old: annotated(withStatus("Healthy", "Synced", testDigest), optIn),
			new: annotated(withStatus("Healthy", "Synced", testDigest), nil), want: true},
		{name: "unrelated annotation", old: annotated(withStatus("Healthy", "Synced", testDigest), optIn),
			new: annotated(withStatus("Healthy", "Synced", testDigest), map[string]string{
				deliveryv1alpha1.AnnotationAllowedOrder: testOptIn, "argocd.argoproj.io/refresh": "normal",
			})},
		{name: "rejected", old: withStatus("Unknown", "Unknown", testDigest), new: rejected(withStatus("Unknown", "Unknown", testDigest)), want: true},
		{name: "nothing relevant", old: withStatus("Healthy", "Synced", testDigest), new: withStatus("Healthy", "Synced", testDigest)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Changed(tt.old, tt.new))
		})
	}
}

func TestTrimStatus(t *testing.T) {
	app := withStatus("Healthy", "Synced", testDigest)
	status := app.Object[statusField].(map[string]any)
	status["resources"] = []any{map[string]any{"kind": "Deployment"}}
	status["history"] = []any{map[string]any{"id": int64(1)}}
	before := StateOf(app)

	TrimStatus(app)

	assert.Equal(t, before, StateOf(app), "what Kokumi reads stays")
	assert.NotContains(t, app.Object[statusField], "resources")
	assert.NotContains(t, app.Object[statusField], "history")

	refused := rejected(withStatus("Unknown", "Unknown", testDigest))
	TrimStatus(refused)
	assert.Equal(t, PhaseRejected, StateOf(refused).Phase, "the conditions stay")
}
