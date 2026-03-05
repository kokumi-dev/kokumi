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
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/service"
)

var _ = Describe("Recipe Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "recipe"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		recipe := &deliveryv1alpha1.Recipe{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Recipe")
			err := k8sClient.Get(ctx, typeNamespacedName, recipe)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Recipe{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: deliveryv1alpha1.RecipeSpec{
						AutoDeploy: false,
						Source: deliveryv1alpha1.OCISource{
							OCI:     "oci://registry.kokumi.svc.cluster.local:5000/recipe/test-resource",
							Version: "0.1.0",
						},
						Destination: deliveryv1alpha1.OCIDestination{
							OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Recipe{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Recipe")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			fs := afero.NewMemMapFs()
			controllerReconciler := &RecipeReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Service: *service.NewRecipeService(
					oci.NewFakeClient(fs),
					fs,
					"",
				),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			preparationList := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, preparationList,
				client.InNamespace("default"),
				client.MatchingLabels{"delivery.kokumi.dev/recipe": resourceName},
			)).To(Succeed())
			Expect(preparationList.Items).To(HaveLen(1))
		})
	})
})
