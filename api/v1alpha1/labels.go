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

	// LabelAutoDeploy indicates whether automatic deployment is enabled.
	LabelAutoDeploy = "delivery.kokumi.dev/auto-deploy"

	// LabelApproveDeploy is set to "true" on a Preparation to trigger a manual serving deployment.
	LabelApproveDeploy = "delivery.kokumi.dev/approve-deploy"

	// LabelServing is the name of the Serving that owns an Argo CD Application.
	LabelServing = "delivery.kokumi.dev/serving"
)

// Annotation keys applied to kokumi-managed resources.
const (
	// AnnotationCommitMessage carries a user-supplied commit message that is
	// attached to the OCI artifact and then removed from the Order after consumption.
	AnnotationCommitMessage = "delivery.kokumi.dev/commit-message"
)

// Finalizer names registered on kokumi-managed resources.
const (
	// FinalizerOrder is the finalizer added to every Order.
	Finalizer = "delivery.kokumi.dev/finalizer"
)
