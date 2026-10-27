// Package argocd builds the Argo CD Application that deploys the artifact of
// the Preparation a Serving serves, and reads the state Argo CD reports on
// it. Kokumi never applies manifests itself: Argo CD syncs them from the
// artifact.
package argocd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Namespace holds the Applications of all Servings.
const Namespace = "argocd"

// InClusterServer is the API server Applications deploy to: the cluster
// Kokumi runs in.
const InClusterServer = "https://kubernetes.default.svc"

// maxNameLength is the limit of an Application name: Argo CD's label-based
// resource tracking writes it into a label value.
const maxNameLength = 63

// GroupVersionKind is the kind of Argo CD Applications.
var GroupVersionKind = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}

// ErrOptInRequired reports an Application that exists but is not opted in
// for management by the Serving's Order. It holds until the Application or
// the Serving changes.
var ErrOptInRequired = errors.New("existing deployment not opted in for this order")

// Phase is the coarse state of an Application.
type Phase string

const (
	// PhaseProgressing means the Application exists but has not reached the
	// desired revision or is not healthy yet.
	PhaseProgressing Phase = "Progressing"

	// PhaseHealthy means the Application is synced and healthy.
	PhaseHealthy Phase = "Healthy"

	// PhaseDegraded means the Application reports an unhealthy state.
	PhaseDegraded Phase = "Degraded"

	// PhaseMissing means the Serving has no Application.
	PhaseMissing Phase = "Missing"
)

// State is what Argo CD reports on the Application of a Serving.
type State struct {
	Phase Phase
	// Revision is the artifact digest the Application is synced to, empty
	// when unknown.
	Revision string
	// Message is the health message Argo CD reports.
	Message string
}

// New returns an empty Application, to read or watch Applications into.
func New() *unstructured.Unstructured {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(GroupVersionKind)
	return app
}

// Key returns the key of the Application that deploys the Order of serving
// to the cluster clusterName: kokumi-<namespace>-<order>-<hash>, where the
// hash is the first 8 hex digits of the SHA-256 of
// <namespace>/<order>/<cluster>. The hash keeps names apart that hyphens
// would join, such as a-b/c and a/b-c, and <namespace>-<order> is cut to fit
// 63 characters.
func Key(serving *deliveryv1alpha1.Serving, clusterName string) types.NamespacedName {
	order := serving.Spec.OrderName
	return types.NamespacedName{
		Namespace: Namespace,
		Name:      hashedName("kokumi-"+serving.Namespace+"-"+order, serving.Namespace+"/"+order+"/"+clusterName, maxNameLength),
	}
}

// hashedName returns base, cut to fit limit, followed by a hyphen and the
// first 8 hex digits of the SHA-256 of unique.
func hashedName(base, unique string, limit int) string {
	sum := sha256.Sum256([]byte(unique))
	hash := hex.EncodeToString(sum[:4])
	base = base[:min(len(base), limit-len(hash)-1)]
	return strings.TrimRight(base, "-.") + "-" + hash
}

// allowedOrder is the value of the opt-in annotation for the Order name in
// namespace.
func allowedOrder(namespace, order string) string {
	return namespace + "/" + order
}

// Application returns the Application that deploys the artifact at ociRef,
// with its digest, for serving. It is opted in for the Serving's Order and
// labeled with the Serving, so that its events map back to it.
func Application(serving *deliveryv1alpha1.Serving, ociRef string) (*unstructured.Unstructured, error) {
	ref, err := oci.Parse(ociRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}

	key := Key(serving, cluster.InCluster)
	app := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":      key.Name,
			"namespace": key.Namespace,
			"labels": map[string]any{
				deliveryv1alpha1.LabelOrder:            serving.Spec.OrderName,
				deliveryv1alpha1.LabelServing:          serving.Name,
				deliveryv1alpha1.LabelServingNamespace: serving.Namespace,
			},
			"annotations": map[string]any{
				deliveryv1alpha1.AnnotationAllowedOrder: allowedOrder(serving.Namespace, serving.Spec.OrderName),
			},
		},
		"spec": map[string]any{
			"project": "default",
			"source": map[string]any{
				"repoURL":        ref.OCIRepositoryReference(),
				"targetRevision": ref.Digest,
				"path":           ".",
			},
			"destination": map[string]any{
				"server":    InClusterServer,
				"namespace": serving.Namespace,
			},
			"syncPolicy": map[string]any{
				"automated": map[string]any{
					"prune":    true,
					"selfHeal": true,
				},
				"syncOptions": []any{
					"ServerSideApply=true",
				},
			},
		},
	}}
	app.SetGroupVersionKind(GroupVersionKind)
	return app, nil
}

// OptedIn returns an error wrapping ErrOptInRequired unless app carries the
// allowed-order annotation for the Order name in namespace.
func OptedIn(app *unstructured.Unstructured, namespace, order string) error {
	actual := app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder]
	expected := allowedOrder(namespace, order)
	if actual == expected {
		return nil
	}
	return fmt.Errorf("%w: Argo CD Application %q must be annotated with %q=%q (current value: %q)",
		ErrOptInRequired, app.GetName(), deliveryv1alpha1.AnnotationAllowedOrder, expected, actual)
}

// Matches reports whether existing already has the spec, labels and
// annotations of desired, so that an update would change nothing. Updating
// regardless would bump the generation on every reconcile, which triggers
// the watch again.
func Matches(existing, desired *unstructured.Unstructured) bool {
	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if !found || err != nil {
		return false
	}
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	return reflect.DeepEqual(existingSpec, desiredSpec) &&
		maps.Equal(existing.GetAnnotations(), desired.GetAnnotations()) &&
		maps.Equal(existing.GetLabels(), desired.GetLabels())
}

// StateOf returns what Argo CD reports on app, PhaseMissing when app is nil.
func StateOf(app *unstructured.Unstructured) State {
	if app == nil {
		return State{Phase: PhaseMissing}
	}

	syncStatus, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
	healthStatus, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
	healthMessage, _, _ := unstructured.NestedString(app.Object, "status", "health", "message")

	state := State{Phase: PhaseProgressing, Revision: revision, Message: healthMessage}
	switch {
	case healthStatus == "Degraded":
		state.Phase = PhaseDegraded
	case syncStatus == "Synced" && healthStatus == "Healthy":
		state.Phase = PhaseHealthy
	}
	return state
}

// ServingOf returns the Serving an Application with labels belongs to, and
// false when the labels name none. The serving-namespace label is required:
// the Application lives in the argocd namespace, not in the Serving's.
func ServingOf(labels map[string]string) (types.NamespacedName, bool) {
	name, namespace := labels[deliveryv1alpha1.LabelServing], labels[deliveryv1alpha1.LabelServingNamespace]
	if name == "" || namespace == "" {
		return types.NamespacedName{}, false
	}
	return types.NamespacedName{Namespace: namespace, Name: name}, true
}

// Changed reports whether the part of an Application that the Serving
// status and the opt-in check depend on differs between old and updated:
// its health, sync status, synced revision, or opt-in annotation. The
// annotation is part of it, so that opting a pre-existing Application in
// resumes the Serving that was refused it.
func Changed(old, updated *unstructured.Unstructured) bool {
	return watchedState(old) != watchedState(updated)
}

func watchedState(app *unstructured.Unstructured) [4]string {
	health, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
	sync, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
	return [4]string{health, sync, revision, app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder]}
}
