// Package argocd builds what Argo CD needs to deploy the artifact of the
// Preparation a Serving serves: one Application per target cluster, and a
// cluster Secret per cluster it deploys to beyond its own. It reads the
// state Argo CD reports on an Application. Kokumi never applies manifests
// itself: Argo CD syncs them from the artifact.
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
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// Namespace holds the Applications of all Servings and the cluster Secrets
// Kokumi manages.
const Namespace = "argocd"

// DefaultProject is Argo CD's own AppProject, which the Environment Kokumi
// ensures in its namespace deploys through.
const DefaultProject = "default"

// GroupVersionKind is the kind of Argo CD Applications.
var GroupVersionKind = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}

// AppProjectGroupVersionKind is the kind of Argo CD AppProjects.
var AppProjectGroupVersionKind = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "AppProject"}

// ErrOptInRequired reports an Application that exists but is not opted in
// for management by the Serving's Order. It holds until the Application or
// the Serving changes.
var ErrOptInRequired = errors.New("existing deployment not opted in for this order")

// maxNameLength is the limit of an Application name: Argo CD's label-based
// resource tracking writes it into a label value.
const maxNameLength = 63

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

	// PhaseRejected means Argo CD refuses to deploy the Application as it
	// is, for example because its AppProject does not permit its
	// destination.
	PhaseRejected Phase = "Rejected"

	// PhaseMissing means the Serving has no Application.
	PhaseMissing Phase = "Missing"
)

// conditionInvalidSpec is the condition Argo CD sets on an Application it
// refuses to deploy as it is.
const conditionInvalidSpec = "InvalidSpecError"

// State is what Argo CD reports on an Application of a Serving.
type State struct {
	Phase Phase
	// Revision is the artifact digest the Application is synced to, empty
	// when unknown.
	Revision string
	// Message is the health message Argo CD reports, or why it refuses the
	// Application.
	Message string
}

// Target is a cluster and namespace an Application deploys to.
type Target struct {
	// Cluster names the Cluster, in the Serving's namespace.
	Cluster string
	// Server is the URL of the cluster's API server.
	Server string
	// Namespace is the namespace on the cluster the Preparation deploys
	// into: its target namespace.
	Namespace string
	// Project is the AppProject of the Application.
	Project string
}

// New returns an empty Application, to read or watch Applications into.
func New() *unstructured.Unstructured {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(GroupVersionKind)
	return app
}

// NewList returns an empty list of Applications, to list them into.
func NewList() *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(GroupVersionKind.GroupVersion().WithKind(GroupVersionKind.Kind + "List"))
	return list
}

// Key returns the key of the Application that deploys the Order of serving
// to cluster: kokumi-<namespace>-<order>-<hash>, where the hash is the first
// 8 hex digits of the SHA-256 of <namespace>/<order>/<cluster>. The hash
// keeps names apart that hyphens would join, such as a-b/c and a/b-c, and
// <namespace>-<order> is cut to fit 63 characters.
func Key(serving *deliveryv1alpha1.Serving, cluster string) types.NamespacedName {
	order := serving.Spec.OrderName
	return types.NamespacedName{
		Namespace: Namespace,
		Name:      hashedName("kokumi-"+serving.Namespace+"-"+order, serving.Namespace+"/"+order+"/"+cluster, maxNameLength),
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

// ServingLabels returns the labels that mark the Applications of serving,
// to select them by.
func ServingLabels(serving *deliveryv1alpha1.Serving) map[string]string {
	return map[string]string{
		deliveryv1alpha1.LabelServing:          serving.Name,
		deliveryv1alpha1.LabelServingNamespace: serving.Namespace,
	}
}

// allowedOrder is the value of the opt-in annotation for the Order name in
// namespace.
func allowedOrder(namespace, order string) string {
	return namespace + "/" + order
}

// Application returns the Application that deploys the artifact at ociRef,
// with its digest, for serving to target, into the target's namespace. It
// is opted in for the Serving's Order and labeled with the Serving and the
// cluster, so that its events map back to the Serving.
func Application(serving *deliveryv1alpha1.Serving, ociRef string, target Target) (*unstructured.Unstructured, error) {
	ref, err := oci.Parse(ociRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse OCI Ref: %w", err)
	}

	key := Key(serving, target.Cluster)
	labels := map[string]any{
		deliveryv1alpha1.LabelOrder:   serving.Spec.OrderName,
		deliveryv1alpha1.LabelCluster: target.Cluster,
	}
	for k, v := range ServingLabels(serving) {
		labels[k] = v
	}
	app := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":      key.Name,
			"namespace": key.Namespace,
			"labels":    labels,
			"annotations": map[string]any{
				deliveryv1alpha1.AnnotationAllowedOrder: allowedOrder(serving.Namespace, serving.Spec.OrderName),
			},
		},
		"spec": map[string]any{
			"project": target.Project,
			"source": map[string]any{
				"repoURL":        ref.OCIRepositoryReference(),
				"targetRevision": ref.Digest,
				"path":           ".",
			},
			"destination": map[string]any{
				"server":    target.Server,
				"namespace": target.Namespace,
			},
			"syncPolicy": map[string]any{
				"automated": map[string]any{
					"prune":    true,
					"selfHeal": true,
				},
				"syncOptions": []any{
					"ServerSideApply=true",
					// The target namespace need not exist on the cluster.
					"CreateNamespace=true",
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

// ClusterOf returns the cluster an Application of a Serving deploys to, as
// its label names it.
func ClusterOf(app *unstructured.Unstructured) string {
	return app.GetLabels()[deliveryv1alpha1.LabelCluster]
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
// It is healthy at its revision only when Argo CD compared the
// Application's current source, synced it to that revision, finished the
// sync, and reports the result healthy: a report on an earlier comparison or
// on a sync that is still running says nothing about the revision. An
// Application Argo CD refuses, such as one whose AppProject does not permit
// its destination, is rejected, with Argo CD's message.
func StateOf(app *unstructured.Unstructured) State {
	if app == nil {
		return State{Phase: PhaseMissing}
	}
	if msg, rejected := invalidSpec(app); rejected {
		return State{Phase: PhaseRejected, Message: msg}
	}

	syncStatus, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
	healthStatus, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
	healthMessage, _, _ := unstructured.NestedString(app.Object, "status", "health", "message")

	state := State{Phase: PhaseProgressing, Revision: revision, Message: healthMessage}
	switch {
	case healthStatus == "Degraded":
		state.Phase = PhaseDegraded
	case syncStatus == "Synced" && healthStatus == "Healthy" && comparedCurrentSource(app) && syncFinished(app, revision):
		state.Phase = PhaseHealthy
	}
	return state
}

// invalidSpec returns the message of the InvalidSpecError condition of app,
// and whether it has one.
func invalidSpec(app *unstructured.Unstructured) (string, bool) {
	conditions, _, _ := unstructured.NestedSlice(app.Object, "status", statusConditions)
	for _, c := range conditions {
		if condition, ok := c.(map[string]any); ok && condition["type"] == conditionInvalidSpec {
			msg, _ := condition["message"].(string)
			return msg, true
		}
	}
	return "", false
}

// comparedCurrentSource reports whether Argo CD's last comparison of app was
// against its current source.
func comparedCurrentSource(app *unstructured.Unstructured) bool {
	compared, found, _ := unstructured.NestedMap(app.Object, "status", "sync", "comparedTo", "source")
	if !found {
		return false
	}
	source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	for _, field := range []string{"repoURL", "targetRevision", "path"} {
		if compared[field] != source[field] {
			return false
		}
	}
	return true
}

// syncFinished reports whether app has no sync operation, or a last one that
// succeeded at revision.
func syncFinished(app *unstructured.Unstructured, revision string) bool {
	phase, found, _ := unstructured.NestedString(app.Object, "status", "operationState", "phase")
	if !found {
		return true
	}
	synced, _, _ := unstructured.NestedString(app.Object, "status", "operationState", "syncResult", "revision")
	return phase == "Succeeded" && synced == revision
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
// its health, sync status, synced revision, the source it was compared
// against, its sync operation, or its opt-in annotation. The annotation is
// part of it, so that opting a pre-existing Application in resumes the
// Serving that was refused it.
func Changed(old, updated *unstructured.Unstructured) bool {
	return watchedState(old) != watchedState(updated)
}

func watchedState(app *unstructured.Unstructured) [8]string {
	field := func(path ...string) string {
		value, _, _ := unstructured.NestedString(app.Object, path...)
		return value
	}
	rejected, _ := invalidSpec(app)
	return [8]string{
		field("status", "health", "status"),
		field("status", "sync", "status"),
		field("status", "sync", "revision"),
		field("status", "sync", "comparedTo", "source", "targetRevision"),
		field("status", "operationState", "phase"),
		field("status", "operationState", "syncResult", "revision"),
		rejected,
		app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder],
	}
}

// statusSync, statusHealth, statusOperation and statusConditions are the
// parts of an Application's status that Kokumi reads.
const (
	statusSync       = "sync"
	statusHealth     = "health"
	statusOperation  = "operationState"
	statusConditions = "conditions"
)

// trimmedStatus are the fields of an Application's status that Kokumi reads.
var trimmedStatus = [][]string{
	{statusSync, "status"}, {statusSync, "revision"}, {statusSync, "comparedTo", "source"},
	{statusHealth, "status"}, {statusHealth, "message"},
	{statusOperation, "phase"}, {statusOperation, "syncResult", "revision"},
	{statusConditions},
}

// TrimStatus keeps only the part of an Application's status that Kokumi
// reads: its sync, health, sync operation and conditions. It drops the
// resources, history and summaries Argo CD records there, which grow with
// what the Application deploys, so that the Applications Kokumi caches stay
// small.
func TrimStatus(app *unstructured.Unstructured) {
	status, found, _ := unstructured.NestedMap(app.Object, "status")
	if !found {
		return
	}
	kept := map[string]any{}
	for _, path := range trimmedStatus {
		if value, ok, _ := unstructured.NestedFieldNoCopy(status, path...); ok {
			_ = unstructured.SetNestedField(kept, value, path...)
		}
	}
	app.Object["status"] = kept
}
