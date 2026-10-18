package argocd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

const (
	testOrderName   = "my-order"
	testServingName = "my-serving"
	testNamespace   = "my-namespace"
	testDigest      = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	statusField     = "status"
)

// withStatus returns an Application with the status the Argo CD
// Application controller writes for the given health and sync status at
// revision.
func withStatus(health, sync, revision string) *unstructured.Unstructured {
	app := New()
	app.SetNamespace(Namespace)
	app.SetName(testServingName)
	app.Object[statusField] = map[string]any{
		"sync":   map[string]any{statusField: sync, "revision": revision},
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

func testPreparation() *deliveryv1alpha1.Preparation {
	return &deliveryv1alpha1.Preparation{Spec: deliveryv1alpha1.PreparationSpec{
		Artifact: deliveryv1alpha1.Artifact{OCIRef: "oci://registry.example/order/app@" + testDigest, Digest: testDigest},
	}}
}

func TestApplication(t *testing.T) {
	app, err := Application(testServing(), testPreparation())
	require.NoError(t, err)

	assert.Equal(t, GroupVersionKind, app.GroupVersionKind())
	assert.Equal(t, types.NamespacedName{Namespace: Namespace, Name: testServingName}, types.NamespacedName{Namespace: app.GetNamespace(), Name: app.GetName()})
	assert.Equal(t, testOrderName, app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder], "an Application Kokumi creates is opted in")
	serving, ok := ServingOf(app.GetLabels())
	assert.True(t, ok)
	assert.Equal(t, types.NamespacedName{Namespace: testNamespace, Name: testServingName}, serving)

	repoURL, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
	revision, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
	assert.Equal(t, "oci://registry.example/order/app", repoURL)
	assert.Equal(t, testDigest, revision)

	_, err = Application(testServing(), &deliveryv1alpha1.Preparation{})
	assert.Error(t, err)
}

func TestMatches(t *testing.T) {
	desired, err := Application(testServing(), testPreparation())
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
	annotated.SetAnnotations(map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName, "extra": "x"})
	assert.False(t, Matches(annotated, desired))

	assert.False(t, Matches(New(), desired), "an Application without spec")
}

func TestStateOf(t *testing.T) {
	tests := []struct {
		name string
		app  *unstructured.Unstructured
		want Phase
	}{
		{name: "missing", want: PhaseMissing},
		{name: "synced and healthy", app: withStatus("Healthy", "Synced", testDigest), want: PhaseHealthy},
		{name: "healthy but out of sync", app: withStatus("Healthy", "OutOfSync", testDigest), want: PhaseProgressing},
		{name: "degraded", app: withStatus("Degraded", "Synced", testDigest), want: PhaseDegraded},
		{name: "progressing", app: withStatus("Progressing", "Synced", testDigest), want: PhaseProgressing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := StateOf(tt.app)
			assert.Equal(t, tt.want, state.Phase)
			if tt.app != nil {
				assert.Equal(t, testDigest, state.Revision)
				assert.Equal(t, "health message", state.Message)
			}
		})
	}
}

func TestOptedIn(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{name: "matching annotation", annotations: map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}},
		{name: "missing annotation", wantErr: true},
		{name: "annotation of another Order", annotations: map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: "other-order"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New()
			app.SetAnnotations(tt.annotations)
			err := OptedIn(app, testOrderName)
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
	optIn := map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: testOrderName}

	tests := []struct {
		name     string
		old, new *unstructured.Unstructured
		want     bool
	}{
		{name: "synced revision", old: withStatus("Healthy", "Synced", "sha256:old"), new: withStatus("Healthy", "Synced", "sha256:new"), want: true},
		{name: "health", old: withStatus("Progressing", "Synced", testDigest), new: withStatus("Healthy", "Synced", testDigest), want: true},
		{name: "opt-in added", old: withStatus("Healthy", "Synced", testDigest),
			new: annotated(withStatus("Healthy", "Synced", testDigest), optIn), want: true},
		{name: "opt-in for another Order", old: annotated(withStatus("Healthy", "Synced", testDigest),
			map[string]string{deliveryv1alpha1.AnnotationAllowedOrder: "other-order"}),
			new: annotated(withStatus("Healthy", "Synced", testDigest), optIn), want: true},
		{name: "opt-in removed", old: annotated(withStatus("Healthy", "Synced", testDigest), optIn),
			new: withStatus("Healthy", "Synced", testDigest), want: true},
		{name: "unrelated annotation", old: withStatus("Healthy", "Synced", testDigest),
			new: annotated(withStatus("Healthy", "Synced", testDigest), map[string]string{"argocd.argoproj.io/refresh": "normal"})},
		{name: "nothing relevant", old: withStatus("Healthy", "Synced", testDigest), new: withStatus("Healthy", "Synced", testDigest)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Changed(tt.old, tt.new))
		})
	}
}
