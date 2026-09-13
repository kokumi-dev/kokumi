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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/service"
)

var _ = Describe("Menu Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Namespace: testNamespace,
			Name:      resourceName,
		}
		menu := &deliveryv1alpha1.Menu{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Menu")
			err := k8sClient.Get(ctx, typeNamespacedName, menu)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Menu{
					Namespace: testNamespace,
					Name:      resourceName,
					Spec: deliveryv1alpha1.MenuSpec{
						Source: deliveryv1alpha1.OCISource{
							OCI:     testOCIRef,
							Version: testVersion,
						},
						Overrides: deliveryv1alpha1.OverridePolicy{
							Values:  deliveryv1alpha1.ValueOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyAll},
							Patches: deliveryv1alpha1.PatchOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyNone},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Menu{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Menu")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &MenuReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Service:        service.NewMenuService(nil),
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Publishing status.source")
			updated := &deliveryv1alpha1.Menu{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updated)).To(Succeed())
			Expect(updated.Status.Source).NotTo(BeNil())
			Expect(updated.Status.Source.OCI).To(Equal(testOCIRef))
			Expect(updated.Status.Source.Version).To(Equal(testVersion))
			Expect(updated.Status.Source.PantryRef).To(BeNil())
			// Digest is best-effort; the fake registry does not exist in envtest.
			Expect(updated.Status.Source.Digest).To(BeEmpty())
		})

		It("should vendor the source when spec.vendor is set", func() {
			// Own resource name: the shared test-resource is left terminating
			// (finalizer) by the first test's AfterEach, since no controller
			// runs to remove it.
			vendorName := types.NamespacedName{Namespace: testNamespace, Name: "test-resource-vendor"}
			By("Creating the resource with spec.vendor")
			resource := &deliveryv1alpha1.Menu{
				Namespace: testNamespace,
				Name:      vendorName.Name,
				Spec: deliveryv1alpha1.MenuSpec{
					Source: deliveryv1alpha1.OCISource{
						OCI:     testOCIRef,
						Version: testVersion,
					},
					Vendor: &deliveryv1alpha1.VendorSpec{
						Destination: deliveryv1alpha1.VendorDestination{
							OCI: "oci://registry.kokumi.svc.cluster.local:5000/vendor/test-resource",
						},
					},
					Overrides: deliveryv1alpha1.OverridePolicy{
						Values:  deliveryv1alpha1.ValueOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyAll},
						Patches: deliveryv1alpha1.PatchOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyNone},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, resource)
			})

			By("Reconciling with a fake OCI client")
			controllerReconciler := &MenuReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Service:        service.NewMenuService(oci.NewFakeClient(afero.NewMemMapFs())),
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: vendorName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Advertising the vendored ref")
			vendored := &deliveryv1alpha1.Menu{}
			Expect(k8sClient.Get(ctx, vendorName, vendored)).To(Succeed())
			Expect(vendored.Status.Source).NotTo(BeNil())
			Expect(vendored.Status.Source.OCI).To(Equal("oci://registry.kokumi.svc.cluster.local:5000/vendor/test-resource"))
			Expect(vendored.Status.Source.Version).To(Equal(testVersion))
			Expect(vendored.Status.Source.Digest).NotTo(BeEmpty())
		})
	})
})
