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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

var _ = Describe("Preparation Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "preparation"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		preparation := &deliveryv1alpha1.Preparation{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Preparation")
			err := k8sClient.Get(ctx, typeNamespacedName, preparation)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Preparation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: deliveryv1alpha1.PreparationSpec{
						Recipe:     "recipe",
						ConfigHash: "sha256:448093f1b28dc7147740d8e400946e9b228650aa31a54b0ed734ca9ab0ae5b6b",
						Renderer: deliveryv1alpha1.Renderer{
							Version:    "0.1.0",
							Digest:     "sha256:fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc",
							RenderType: deliveryv1alpha1.RenderTypeManifest,
						},
						Source: deliveryv1alpha1.RecipeSource{
							OCI:        "oci://registry.kokumi.svc.cluster.local:5000/recipe/test-resource",
							BaseDigest: "sha256:6c2069fa6684d3659d93538331711b09a33cb42ae305802195d6a4d58847b345",
						},
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@sha256:fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc",
							Digest: "sha256:fdf90e00e7605d65cdf4a5d3a404c9823ee2e473f7468f68c29694f1b909e2bc",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Preparation{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Preparation")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &PreparationReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
