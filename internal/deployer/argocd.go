package deployer

import (
	"context"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	argocdNamespace = "argocd"
	argocdGroup     = "argoproj.io"
	argocdVersion   = "v1alpha1"
	argocdKind      = "Application"
)

// ArgoCDDeployer deploys a Preparation's artifact by managing an Argo CD
// Application that points at the artifact's OCI reference.
type ArgoCDDeployer struct {
	client.Client
}

var _ Deployer = (*ArgoCDDeployer)(nil)

// NewArgoCD returns an ArgoCDDeployer using the given client.
func NewArgoCD(c client.Client) *ArgoCDDeployer {
	return &ArgoCDDeployer{Client: c}
}

func argoAppGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   argocdGroup,
		Version: argocdVersion,
		Kind:    argocdKind,
	}
}

// newArgoApp returns an empty unstructured Argo CD Application.
func newArgoApp() *unstructured.Unstructured {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(argoAppGVK())
	return app
}

// WatchObject returns an empty unstructured Argo CD Application, the resource
// whose state changes drive Serving re-reconciliation.
func (d *ArgoCDDeployer) WatchObject() client.Object {
	return newArgoApp()
}

// WatchPredicate filters Application update events to those where the
// health, sync status, or synced revision changed.
func (d *ArgoCDDeployer) WatchPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHealth, _, _ := unstructured.NestedString(e.ObjectOld.(*unstructured.Unstructured).Object, "status", "health", "status")
			newHealth, _, _ := unstructured.NestedString(e.ObjectNew.(*unstructured.Unstructured).Object, "status", "health", "status")
			oldSync, _, _ := unstructured.NestedString(e.ObjectOld.(*unstructured.Unstructured).Object, "status", "sync", "status")
			newSync, _, _ := unstructured.NestedString(e.ObjectNew.(*unstructured.Unstructured).Object, "status", "sync", "status")
			oldRevision, _, _ := unstructured.NestedString(e.ObjectOld.(*unstructured.Unstructured).Object, "status", "sync", "revision")
			newRevision, _, _ := unstructured.NestedString(e.ObjectNew.(*unstructured.Unstructured).Object, "status", "sync", "revision")
			return oldHealth != newHealth || oldSync != newSync || oldRevision != newRevision
		},
	}
}

// EnqueueRequests maps an Argo CD Application event to a reconcile request
// for the owning Serving, matched via the respective labels.
func (d *ArgoCDDeployer) EnqueueRequests(_ context.Context, obj client.Object) []reconcile.Request {
	servingName, ok := obj.GetLabels()[deliveryv1alpha1.LabelServing]
	if !ok || servingName == "" {
		return nil
	}
	servingNamespace := obj.GetLabels()[deliveryv1alpha1.LabelServingNamespace]
	if servingNamespace == "" {
		return nil
	}
	return []reconcile.Request{{
		Namespace: servingNamespace,
		Name:      servingName,
	}}
}

// VerifyOptIn looks up the Argo CD Application that this Serving would manage
// and verifies the opt-in annotation.
func (d *ArgoCDDeployer) VerifyOptIn(ctx context.Context, serving *deliveryv1alpha1.Serving) error {
	existing := newArgoApp()

	err := d.Get(ctx, client.ObjectKey{Namespace: argocdNamespace, Name: serving.Name}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get existing Application: %w", err)
	}

	return assertAllowedOrderAnnotation(existing, serving.Spec.OrderName)
}

// assertAllowedOrderAnnotation returns ErrOptInRequired (wrapped with a
// descriptive message) if the given Application is not annotated with the
// expected delivery.kokumi.dev/allowed-order value.
func assertAllowedOrderAnnotation(app *unstructured.Unstructured, expectedOrder string) error {
	actual := app.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder]
	if actual == expectedOrder {
		return nil
	}
	return fmt.Errorf(
		"%w: Argo CD Application %q must be annotated with %q=%q (current value: %q)",
		ErrOptInRequired,
		app.GetName(),
		deliveryv1alpha1.AnnotationAllowedOrder,
		expectedOrder,
		actual,
	)
}

// Deploy creates or updates the Argo CD Application for the Serving so that
// it points at the Preparation's artifact.
func (d *ArgoCDDeployer) Deploy(ctx context.Context, serving *deliveryv1alpha1.Serving, preparation *deliveryv1alpha1.Preparation) error {
	logger := log.FromContext(ctx)

	ociRef, err := oci.Parse(preparation.Spec.Artifact.OCIRef)
	if err != nil {
		return fmt.Errorf("failed to parse OCI Ref: %w", err)
	}

	targetRevision := ociRef.Digest
	appName := serving.Name

	app := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "argoproj.io/v1alpha1",
			"kind":       argocdKind,
			"metadata": map[string]any{
				"name":      appName,
				"namespace": argocdNamespace,
				"labels": map[string]any{
					deliveryv1alpha1.LabelOrder:            serving.Spec.OrderName,
					deliveryv1alpha1.LabelServing:          serving.Name,
					deliveryv1alpha1.LabelServingNamespace: serving.Namespace,
				},
				"annotations": map[string]any{
					deliveryv1alpha1.AnnotationAllowedOrder: serving.Spec.OrderName,
				},
			},
			"spec": map[string]any{
				"project": "default",
				"source": map[string]any{
					"repoURL":        ociRef.OCIRepositoryReference(),
					"targetRevision": targetRevision,
					"path":           ".",
				},
				"destination": map[string]any{
					"server":    "https://kubernetes.default.svc",
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
		},
	}

	existing := newArgoApp()
	err = d.Get(ctx, client.ObjectKey{Namespace: argocdNamespace, Name: appName}, existing)

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Creating Argo CD Application", "name", appName, "namespace", argocdNamespace, "revision", targetRevision)
			if err := d.Create(ctx, app); err != nil {
				return fmt.Errorf("failed to create Application: %w", err)
			}
			logger.Info("Created Argo CD Application", "name", appName)
		} else {
			return fmt.Errorf("failed to get existing Application: %w", err)
		}
	} else {
		if err := assertAllowedOrderAnnotation(existing, serving.Spec.OrderName); err != nil {
			logger.Info(
				"Cannot update Argo CD Application, opt-in annotation must exist",
				"name", appName,
				"namespace", argocdNamespace,
				"requiredAnnotation", deliveryv1alpha1.AnnotationAllowedOrder,
				"expectedValue", serving.Spec.OrderName,
				"actualValue", existing.GetAnnotations()[deliveryv1alpha1.AnnotationAllowedOrder],
			)
			return err
		}

		// Skip the update when the Application already matches the desired
		// state. Updating unconditionally would bump the generation on every
		// reconcile, which re-triggers the watch and hot-loops the controller.
		if applicationMatches(existing, app) {
			return nil
		}

		app.SetResourceVersion(existing.GetResourceVersion())
		logger.Info("Updating Argo CD Application", "name", appName, "namespace", argocdNamespace, "revision", targetRevision)
		if err := d.Update(ctx, app); err != nil {
			return fmt.Errorf("failed to update Application: %w", err)
		}
		logger.Info("Updated Argo CD Application", "name", appName)
	}

	return nil
}

// applicationMatches reports whether the existing Application's spec and
// metadata already equal the desired ones, i.e. an update would be a no-op.
func applicationMatches(existing, desired *unstructured.Unstructured) bool {
	existingSpec, found, err := unstructured.NestedMap(existing.Object, "spec")
	if !found || err != nil {
		return false
	}
	desiredSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	if !reflect.DeepEqual(existingSpec, desiredSpec) {
		return false
	}

	existingAnnotations := existing.GetAnnotations()
	desiredAnnotations := desired.GetAnnotations()
	if len(existingAnnotations) != len(desiredAnnotations) {
		return false
	}
	for k, v := range desiredAnnotations {
		if existingAnnotations[k] != v {
			return false
		}
	}

	existingLabels := existing.GetLabels()
	desiredLabels := desired.GetLabels()
	if len(existingLabels) != len(desiredLabels) {
		return false
	}
	for k, v := range desiredLabels {
		if existingLabels[k] != v {
			return false
		}
	}

	return true
}

// Status reports the current state of the Argo CD Application belonging to
// the Serving.
func (d *ArgoCDDeployer) Status(ctx context.Context, serving *deliveryv1alpha1.Serving) (DeploymentStatus, error) {
	app := newArgoApp()

	err := d.Get(ctx, client.ObjectKey{Namespace: argocdNamespace, Name: serving.Name}, app)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return DeploymentStatus{Phase: PhaseMissing}, nil
		}
		return DeploymentStatus{}, fmt.Errorf("failed to get Application: %w", err)
	}

	syncStatus, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
	revision, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
	healthStatus, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
	healthMessage, _, _ := unstructured.NestedString(app.Object, "status", "health", "message")

	status := DeploymentStatus{
		Phase:    PhaseProgressing,
		Revision: revision,
		Message:  healthMessage,
	}

	switch {
	case healthStatus == "Degraded":
		status.Phase = PhaseDegraded
	case syncStatus == "Synced" && healthStatus == "Healthy":
		status.Phase = PhaseHealthy
	}

	return status, nil
}

// Remove deletes the Argo CD Application for the Serving. Deleting a missing
// Application is not an error.
func (d *ArgoCDDeployer) Remove(ctx context.Context, serving *deliveryv1alpha1.Serving) error {
	logger := log.FromContext(ctx)

	app := newArgoApp()
	app.SetNamespace(argocdNamespace)
	app.SetName(serving.Name)

	if err := d.Delete(ctx, app); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "Failed to delete Argo CD Application")
			return err
		}
		logger.Info("Argo CD Application already deleted")
		return nil
	}

	logger.Info("Deleted Argo CD Application", "name", serving.Name)
	return nil
}
