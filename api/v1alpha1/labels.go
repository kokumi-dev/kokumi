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

package v1alpha1

// Label keys applied to kokumi-managed resources.
const (
	// LabelOrder is the name of the Order that produced this resource.
	LabelOrder = "delivery.kokumi.dev/order"

	// LabelVersion is the source version tag/digest used when the Preparation was built.
	LabelVersion = "delivery.kokumi.dev/version"

	// LabelServing is the name of the Serving that owns an Argo CD Application.
	LabelServing = "delivery.kokumi.dev/serving"

	// LabelServingNamespace is the namespace of the Serving that owns an
	// Argo CD Application. It is required to map events on the Application,
	// which lives in the argocd namespace, back to the Serving, which lives
	// in its own namespace.
	LabelServingNamespace = "delivery.kokumi.dev/serving-namespace"

	// LabelEnvironment groups the Orders of a Product that form one
	// environment of its promotion pipeline, such as the regional variants of
	// int or prod. Users set it; the UI draws an environment as one column of
	// the pipeline. It is metadata, not intent: changing it renders nothing.
	LabelEnvironment = "delivery.kokumi.dev/environment"

	// LabelInCluster marks a Cluster that connects in-cluster, so that
	// Environments, such as local, select the cluster Kokumi runs in by
	// label. Kokumi sets it on the Cluster in-cluster it keeps in its own
	// namespace when it creates it; other Clusters carry it when their
	// authors set it.
	LabelInCluster = "delivery.kokumi.dev/in-cluster"
)

// Annotation keys applied to kokumi-managed resources.
const (
	// AnnotationCommitMessage carries a user-supplied commit message that is
	// attached to the OCI artifact and then removed from the Order after consumption.
	AnnotationCommitMessage = "delivery.kokumi.dev/commit-message"

	// AnnotationAllowedOrder marks an Argo CD Application as opt-in to being
	// managed (updated) by a kokumi Order. The annotation value is the name of
	// the Order that is allowed to update the Application. Kokumi sets this
	// annotation when it creates an Application, and refuses to update any
	// pre-existing Application that does not carry a matching value.
	AnnotationAllowedOrder = "delivery.kokumi.dev/allowed-order"
)

// Paths of the selectable fields, for field selectors against the API server
// and for the field indexes of the controller cache.
const (
	// FieldOrderName selects Approvals and Preparations by spec.orderName.
	FieldOrderName = "spec.orderName"

	// FieldPreparationRefName selects Approvals by spec.preparationRef.name.
	FieldPreparationRefName = "spec.preparationRef.name"

	// FieldProductRefName selects Orders by spec.productRef.name.
	FieldProductRefName = "spec.productRef.name"

	// FieldProductRevision selects Preparations by spec.productRevision: the
	// Preparations every Order of a Product rendered from one Product change.
	FieldProductRevision = "spec.productRevision"

	// FieldKubeconfigSecretName indexes Clusters by the Secret that holds
	// their kubeconfig.
	FieldKubeconfigSecretName = "spec.kubeconfig.secretRef.name"
)

// Finalizer names registered on kokumi-managed resources.
const (
	// Finalizer is added to Orders, Preparations, Recipes, Servings,
	// Environments and Clusters. The Serving controller deletes the Serving's
	// Argo CD Application, which lives in another namespace, before it
	// removes it.
	Finalizer = "delivery.kokumi.dev/finalizer"

	// FinalizerProductProtection keeps a Product from being deleted while
	// Orders still reference it, like kubernetes.io/pvc-protection.
	FinalizerProductProtection = "delivery.kokumi.dev/product-protection"
)
