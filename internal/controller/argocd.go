/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/promotion"
)

// argoCD is the engine of Environments that deploy through Argo CD: an
// Application per target cluster in Argo CD's namespace. It implements
// servingDeployer and environmentAdmitter with the client calls;
// internal/argocd builds and reads the objects.
type argoCD struct {
	client.Client
	// namespace is Kokumi's own namespace. Its Environments may use any
	// AppProject.
	namespace string
}

// Admit reports whether Orders of env may deploy through Argo CD: Argo
// CD's CRDs are installed, the Environment's AppProject exists and,
// outside Kokumi's namespace, admits the Environment's namespace in its
// sourceNamespaces. Argo CD checks sourceNamespaces only for Applications
// outside its own namespace, and Kokumi writes them into it, so Kokumi
// checks it, every time before it deploys.
func (a *argoCD) Admit(ctx context.Context, env *deliveryv1alpha1.Environment) (admission, error) {
	name := ""
	if env.Spec.Deployer.ArgoCD != nil {
		name = env.Spec.Deployer.ArgoCD.Project
	}
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(argocd.AppProjectGroupVersionKind)
	err := a.Get(ctx, client.ObjectKey{Namespace: argocd.Namespace, Name: name}, project)
	switch {
	case meta.IsNoMatchError(err):
		return admission{deliveryv1alpha1.ReasonArgoCDNotInstalled, "Argo CD's CRDs are not installed; install Argo CD to deploy"}, nil
	case apierrors.IsNotFound(err):
		return admission{deliveryv1alpha1.ReasonProjectNotFound,
			fmt.Sprintf("The Argo CD AppProject %q does not exist in namespace %s", name, argocd.Namespace)}, nil
	case err != nil:
		return admission{}, fmt.Errorf("failed to get AppProject %q: %w", name, err)
	case env.Namespace != a.namespace && !argocd.ProjectAdmitsSourceNamespace(project, env.Namespace):
		return admission{deliveryv1alpha1.ReasonProjectNotPermitted,
			fmt.Sprintf("The Argo CD AppProject %q does not admit namespace %s in its sourceNamespaces", name, env.Namespace)}, nil
	}
	return admission{}, nil
}

// Apply points the Application that deploys the Order of serving to target
// at the artifact of deployed, creating it when it is missing, and returns
// what Argo CD reports there. An Application that already points there is
// left untouched, and drift is reverted. A pre-existing Application that is
// not opted in for the Serving's Order is never changed: the target is held
// with ReasonOptInRequired.
func (a *argoCD) Apply(
	ctx context.Context, env *deliveryv1alpha1.Environment, serving *deliveryv1alpha1.Serving, deployed *promotion.Entry, target deployTarget,
) (targetState, error) {
	logger := log.FromContext(ctx).WithValues("cluster", target.cluster, "preparation", deployed.Preparation)

	project := ""
	if env.Spec.Deployer.ArgoCD != nil {
		project = env.Spec.Deployer.ArgoCD.Project
	}
	desired, err := argocd.Application(serving, deployed.OCIRef, argocd.Target{
		Cluster: target.cluster, Server: target.server, Namespace: target.namespace, Project: project,
	})
	if err != nil {
		return targetState{}, err
	}

	existing := argocd.New()
	err = a.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	switch {
	case meta.IsNoMatchError(err):
		return targetState{held: deliveryv1alpha1.ReasonArgoCDNotInstalled,
			message: "Argo CD's Application CRD is not installed; install Argo CD to deploy"}, nil
	case apierrors.IsNotFound(err):
		if err := a.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// The cache has not seen it yet; its watch reconciles again.
				logger.V(1).Info("Argo CD Application exists, the cache has not seen it yet", "name", desired.GetName())
				return targetState{health: promotion.HealthProgressing}, nil
			}
			return targetState{}, fmt.Errorf("failed to create Application: %w", err)
		}
		logger.Info("Created Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace())
		return stateOf(desired), nil
	case err != nil:
		return targetState{}, fmt.Errorf("failed to get Application: %w", err)
	}

	if err := argocd.OptedIn(existing, serving.Namespace, serving.Spec.OrderName); err != nil {
		logger.Info("Could not update deployment, opt-in required", "error", err.Error())
		return targetState{held: deliveryv1alpha1.ReasonOptInRequired, message: err.Error()}, nil
	}
	if argocd.Matches(existing, desired) {
		return stateOf(existing), nil
	}

	desired.SetResourceVersion(existing.GetResourceVersion())
	if err := a.Update(ctx, desired); err != nil {
		return targetState{}, fmt.Errorf("failed to update Application: %w", err)
	}
	logger.Info("Updated Argo CD Application", "name", desired.GetName(), "namespace", desired.GetNamespace())
	return stateOf(desired), nil
}

// stateOf maps what Argo CD reports on app to the state of a target.
func stateOf(app *unstructured.Unstructured) targetState {
	state := argocd.StateOf(app)
	switch state.Phase {
	case argocd.PhaseHealthy:
		return targetState{health: promotion.HealthHealthy, revision: state.Revision, message: state.Message}
	case argocd.PhaseDegraded:
		return targetState{health: promotion.HealthDegraded, revision: state.Revision, message: state.Message}
	case argocd.PhaseRejected:
		return targetState{held: deliveryv1alpha1.ReasonRejected, message: "Argo CD refuses the deployment: " + state.Message}
	default:
		return targetState{health: promotion.HealthProgressing, revision: state.Revision, message: state.Message}
	}
}

// Remove deletes the Applications of serving on the clusters keep does not
// hold, all of them for nil, and returns those clusters. What runs there
// keeps running: Kokumi's Applications carry no resources-finalizer. One
// that is not opted in for the Serving's Order is kept. Each delete is
// conditional on the UID and resource version that passed the opt-in check,
// so an Application replaced or changed in between is never deleted.
func (a *argoCD) Remove(ctx context.Context, serving *deliveryv1alpha1.Serving, keep map[string]bool) ([]string, error) {
	logger := log.FromContext(ctx)

	apps := argocd.NewList()
	err := a.List(ctx, apps, client.InNamespace(argocd.Namespace), client.MatchingLabels(argocd.ServingLabels(serving)))
	switch {
	case meta.IsNoMatchError(err):
		// Without Argo CD there are no Applications.
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("failed to list Applications: %w", err)
	}
	var removed []string
	for i := range apps.Items {
		app := &apps.Items[i]
		if keep[argocd.ClusterOf(app)] {
			continue
		}
		if err := argocd.OptedIn(app, serving.Namespace, serving.Spec.OrderName); err != nil {
			logger.Info("Keeping Argo CD Application that is not opted in for the Order", "name", app.GetName(), "error", err.Error())
			continue
		}
		uid, resourceVersion := app.GetUID(), app.GetResourceVersion()
		if err := a.Delete(ctx, app, client.Preconditions{UID: &uid, ResourceVersion: &resourceVersion}); client.IgnoreNotFound(err) != nil {
			return removed, fmt.Errorf("failed to delete Application %q: %w", app.GetName(), err)
		}
		logger.Info("Deleted Argo CD Application", "name", app.GetName(), "cluster", argocd.ClusterOf(app))
		removed = append(removed, argocd.ClusterOf(app))
	}
	return removed, nil
}

// serverKey is an API server URL without a trailing slash, as Argo CD
// compares them.
func serverKey(server string) string {
	return strings.TrimSuffix(server, "/")
}

// servingOfApplication maps an Argo CD Application to the Serving its labels
// name.
func servingOfApplication(_ context.Context, obj client.Object) []ctrl.Request {
	key, ok := argocd.ServingOf(obj.GetLabels())
	if !ok {
		return nil
	}
	return []ctrl.Request{{NamespacedName: key}}
}

// applicationChanged passes the Application updates that change what the
// Serving status and the opt-in check depend on. Creates and deletes always
// pass.
var applicationChanged = predicate.Funcs{
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldApp, okOld := e.ObjectOld.(*unstructured.Unstructured)
		newApp, okNew := e.ObjectNew.(*unstructured.Unstructured)
		return okOld && okNew && argocd.Changed(oldApp, newApp)
	},
}

// newAppProject returns an empty Argo CD AppProject, to read or watch
// AppProjects into.
func newAppProject() *unstructured.Unstructured {
	project := &unstructured.Unstructured{}
	project.SetGroupVersionKind(argocd.AppProjectGroupVersionKind)
	return project
}

// crdCheckInterval is how often an optional watch checks whether the CRD of
// its kind is installed.
const crdCheckInterval = 30 * time.Second

// optionalWatch adds the watch of a kind from another project, such as Argo
// CD's Application or AppProject, to a controller once its CRD is installed
// and serves the version Kokumi reads, so that the manager starts and every
// controller runs without it. It checks at start and then every
// crdCheckInterval, and once the watch runs it sends an event on Started,
// so that the reconciles that found the kind missing run again. Reconciles
// never ask it whether the kind is installed: reads of a kind without its
// CRD fail with a no-match error, which tells them.
type optionalWatch struct {
	obj      client.Object
	gvk      schema.GroupVersionKind
	mapper   meta.RESTMapper
	interval time.Duration
	started  chan event.GenericEvent
}

// newOptionalWatch returns the optional watch of the kind of obj: a type of
// the manager's scheme, or an unstructured object with its kind set.
func newOptionalWatch(mgr ctrl.Manager, obj client.Object) (*optionalWatch, error) {
	gvk, err := apiutil.GVKForObject(obj, mgr.GetScheme())
	if err != nil {
		return nil, err
	}
	return &optionalWatch{obj: obj, gvk: gvk, mapper: mgr.GetRESTMapper(), interval: crdCheckInterval, started: make(chan event.GenericEvent, 1)}, nil
}

// Started returns the channel that receives an event once the watch runs.
func (w *optionalWatch) Started() <-chan event.GenericEvent {
	return w.started
}

// Add adds the runnable to mgr that starts the watch on c, with h and
// predicates, once the CRD is installed.
func (w *optionalWatch) Add(mgr ctrl.Manager, c controller.Controller, h handler.EventHandler, predicates ...predicate.Predicate) error {
	return mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		return w.wait(ctx, func() error {
			return c.Watch(source.Kind[client.Object](mgr.GetCache(), w.obj, h, predicates...))
		})
	}))
}

// wait calls start once the CRD of the kind is installed, then sends an
// event on Started. It returns when the watch started or ctx is done.
func (w *optionalWatch) wait(ctx context.Context, start func() error) error {
	logger := log.FromContext(ctx).WithValues("kind", w.gvk.Kind, "group", w.gvk.Group)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for logged := false; ; {
		_, err := w.mapper.RESTMapping(w.gvk.GroupKind(), w.gvk.Version)
		switch {
		case err == nil:
			if err := start(); err != nil {
				return fmt.Errorf("failed to watch %s: %w", w.gvk.Kind, err)
			}
			w.started <- event.GenericEvent{Object: w.obj}
			logger.Info("Watching optional kind, its CRD is installed")
			return nil
		case !meta.IsNoMatchError(err):
			logger.Error(err, "Failed to check whether the CRD of an optional kind is installed")
		case !logged:
			logger.Info("CRD of optional kind not installed, or not serving the version, checking again",
				"version", w.gvk.Version, "interval", w.interval)
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
