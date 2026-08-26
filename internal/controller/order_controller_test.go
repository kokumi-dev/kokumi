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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/service"
)

var _ = Describe("Order Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "order"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testNamespace,
		}
		order := &deliveryv1alpha1.Order{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Order")
			err := k8sClient.Get(ctx, typeNamespacedName, order)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Order{
					Name:      resourceName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.OrderSpec{
						AutoDeploy: deliveryv1alpha1.AutoDeployDisabled,
						Source: &deliveryv1alpha1.OCISource{
							OCI:     testOCIRef,
							Version: testVersion,
						},
						Destination: &deliveryv1alpha1.OCIDestination{
							OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Order{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Order")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			fs := afero.NewMemMapFs()
			controllerReconciler := &OrderReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Service: *service.NewOrderService(
					oci.NewFakeClient(fs),
					fs,
					"",
				),
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			preparationList := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, preparationList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: resourceName},
			)).To(Succeed())
			Expect(preparationList.Items).To(HaveLen(1))
		})
	})

	Context("When Order uses pantryRef", func() {
		const ns = testNamespace

		ctx := context.Background()

		newReconciler := func() *OrderReconciler {
			fs := afero.NewMemMapFs()
			return &OrderReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Service: *service.NewOrderService(
					oci.NewFakeClient(fs),
					fs,
					"",
				),
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}
		}

		listPreparations := func(orderName string) *deliveryv1alpha1.PreparationList {
			list := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, list,
				client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName},
			)).To(Succeed())
			return list
		}

		reconcileOrder := func(name string) error {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{
				Name: name, Namespace: ns,
			})
			return err
		}

		getOrder := func(name string) *deliveryv1alpha1.Order {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, order)).To(Succeed())
			return order
		}

		createPantry := func(name, url string, secretRef *corev1.LocalObjectReference) {
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Pantry{
				Name: name, Namespace: ns,
				Spec: deliveryv1alpha1.PantrySpec{
					URL:       url,
					SecretRef: secretRef,
				},
			})).To(Succeed())
		}

		createOrder := func(name string, source *deliveryv1alpha1.OCISource, dest *deliveryv1alpha1.OCIDestination) {
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: name, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					AutoDeploy:  deliveryv1alpha1.AutoDeployDisabled,
					Source:      source,
					Destination: dest,
				},
			})).To(Succeed())
		}

		cleanup := func(orderName, pantryName string, extra ...client.Object) {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
				client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName},
			)
			if orderName != "" {
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{
					Name: orderName, Namespace: ns,
				})
			}
			if pantryName != "" {
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Pantry{
					Name: pantryName, Namespace: ns,
				})
			}
			for _, obj := range extra {
				_ = k8sClient.Delete(ctx, obj)
			}
		}

		It("creates a Preparation from the live Pantry URL", func() {
			const (
				orderName  = "order-src-pantry"
				pantryName = "src-pantry"
				pantryURL  = "oci://registry.kokumi.svc.cluster.local:5000/charts/app"
			)
			defer cleanup(orderName, pantryName)

			createPantry(pantryName, pantryURL, nil)
			createOrder(orderName, &deliveryv1alpha1.OCISource{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
				Version:   testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/app",
			})

			Expect(reconcileOrder(orderName)).To(Succeed())

			order := getOrder(orderName)
			Expect(order.Status.LatestConfigHash).NotTo(BeEmpty())
			ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))

			preps := listPreparations(orderName)
			Expect(preps.Items).To(HaveLen(1))
			Expect(preps.Items[0].Spec.Source.OCI).To(Equal(pantryURL))
		})

		It("creates a new Preparation when the source Pantry URL changes", func() {
			const (
				orderName  = "order-src-url-change"
				pantryName = "src-pantry-change"
				firstURL   = "oci://registry.kokumi.svc.cluster.local:5000/charts/app"
				secondURL  = "oci://registry.kokumi.svc.cluster.local:5000/charts/app-v2"
			)
			defer cleanup(orderName, pantryName)

			createPantry(pantryName, firstURL, nil)
			createOrder(orderName, &deliveryv1alpha1.OCISource{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
				Version:   testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/app-change",
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			first := getOrder(orderName)
			firstHash := first.Status.LatestConfigHash
			Expect(firstHash).NotTo(BeEmpty())
			Expect(listPreparations(orderName).Items).To(HaveLen(1))

			pantry := &deliveryv1alpha1.Pantry{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pantryName, Namespace: ns}, pantry)).To(Succeed())
			pantry.Spec.URL = secondURL
			Expect(k8sClient.Update(ctx, pantry)).To(Succeed())

			Expect(reconcileOrder(orderName)).To(Succeed())
			second := getOrder(orderName)
			Expect(second.Status.LatestConfigHash).NotTo(Equal(firstHash))

			preps := listPreparations(orderName)
			Expect(preps.Items).To(HaveLen(2))
			urls := []string{preps.Items[0].Spec.Source.OCI, preps.Items[1].Spec.Source.OCI}
			Expect(urls).To(ConsistOf(firstURL, secondURL))
		})

		It("skips rebuild when the Pantry URL is unchanged", func() {
			const (
				orderName  = "order-src-unchanged"
				pantryName = "src-pantry-unchanged"
				pantryURL  = "oci://registry.kokumi.svc.cluster.local:5000/charts/stable"
			)
			defer cleanup(orderName, pantryName)

			createPantry(pantryName, pantryURL, nil)
			createOrder(orderName, &deliveryv1alpha1.OCISource{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
				Version:   testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/stable",
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			hash := getOrder(orderName).Status.LatestConfigHash

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(getOrder(orderName).Status.LatestConfigHash).To(Equal(hash))
			Expect(listPreparations(orderName).Items).To(HaveLen(1))
		})

		It("creates a new Preparation when the destination Pantry URL changes", func() {
			const (
				orderName  = "order-dest-url-change"
				pantryName = "dest-pantry-change"
				firstURL   = "oci://registry.kokumi.svc.cluster.local:5000/dest/app"
				secondURL  = "oci://registry.kokumi.svc.cluster.local:5000/dest/app-v2"
			)
			defer cleanup(orderName, pantryName)

			createPantry(pantryName, firstURL, nil)
			createOrder(orderName, &deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.kokumi.svc.cluster.local:5000/order/app",
				Version: "0.1.0",
			}, &deliveryv1alpha1.OCIDestination{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			firstHash := getOrder(orderName).Status.LatestConfigHash
			Expect(listPreparations(orderName).Items).To(HaveLen(1))
			Expect(listPreparations(orderName).Items[0].Spec.Artifact.OCIRef).To(HavePrefix(firstURL + "@"))

			pantry := &deliveryv1alpha1.Pantry{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pantryName, Namespace: ns}, pantry)).To(Succeed())
			pantry.Spec.URL = secondURL
			Expect(k8sClient.Update(ctx, pantry)).To(Succeed())

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(getOrder(orderName).Status.LatestConfigHash).NotTo(Equal(firstHash))

			preps := listPreparations(orderName)
			Expect(preps.Items).To(HaveLen(2))
			refs := []string{preps.Items[0].Spec.Artifact.OCIRef, preps.Items[1].Spec.Artifact.OCIRef}
			Expect(refs).To(ContainElement(HavePrefix(firstURL + "@")))
			Expect(refs).To(ContainElement(HavePrefix(secondURL + "@")))
		})

		It("does not rebuild when only Pantry credentials change", func() {
			const (
				orderName  = "order-secret-only"
				pantryName = "src-pantry-secret"
				secretName = "src-pantry-creds"
				pantryURL  = "oci://registry.kokumi.svc.cluster.local:5000/charts/private"
			)
			secret := &corev1.Secret{
				Name: secretName, Namespace: ns,
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{"placeholder": []byte("x")},
			}
			defer cleanup(orderName, pantryName, secret)

			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			createPantry(pantryName, pantryURL, &corev1.LocalObjectReference{Name: secretName})
			createOrder(orderName, &deliveryv1alpha1.OCISource{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
				Version:   testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/private",
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			hash := getOrder(orderName).Status.LatestConfigHash
			Expect(listPreparations(orderName).Items).To(HaveLen(1))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, secret)).To(Succeed())
			secret.Data = map[string][]byte{
				corev1.DockerConfigJsonKey: []byte(`{"auths":{"registry.kokumi.svc.cluster.local:5000":{"auth":"bmV3OnRva2Vu"}}}`),
			}
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(getOrder(orderName).Status.LatestConfigHash).To(Equal(hash))
			Expect(listPreparations(orderName).Items).To(HaveLen(1))
		})

		It("fails when the referenced Pantry is missing", func() {
			const orderName = "order-missing-pantry"
			defer cleanup(orderName, "")

			createOrder(orderName, &deliveryv1alpha1.OCISource{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: "does-not-exist"},
				Version:   testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/missing",
			})

			Expect(reconcileOrder(orderName)).NotTo(Succeed())

			order := getOrder(orderName)
			Expect(order.Status.LatestConfigHash).To(BeEmpty())
			ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal("ProcessingFailed"))
		})
	})

	Context("When Order configuration returns to a previous state", func() {
		const (
			ns        = testNamespace
			orderName = "order-edit-revert"
		)

		ctx := context.Background()

		newReconciler := func() *OrderReconciler {
			fs := afero.NewMemMapFs()
			return &OrderReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Service: *service.NewOrderService(
					oci.NewFakeClient(fs),
					fs,
					"",
				),
				PantryResolver: credential.NewKubeResolver(k8sClient),
			}
		}

		It("creates a new Preparation when edits are reverted to a previous configuration", func() {
			defer func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(ns),
					client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName},
				)
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{
					Name: orderName, Namespace: ns,
				})
			}()

			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					AutoDeploy: deliveryv1alpha1.AutoDeployDisabled,
					Source: &deliveryv1alpha1.OCISource{
						OCI:     "oci://registry.kokumi.svc.cluster.local:5000/order/edit-revert",
						Version: "0.1.0",
					},
					Destination: &deliveryv1alpha1.OCIDestination{
						OCI: "oci://registry.kokumi.svc.cluster.local:5000/preparation/edit-revert",
					},
				},
			})).To(Succeed())

			reconcile := func() {
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{
					Name: orderName, Namespace: ns,
				})
				Expect(err).NotTo(HaveOccurred())
			}

			listPreps := func() []deliveryv1alpha1.Preparation {
				list := &deliveryv1alpha1.PreparationList{}
				Expect(k8sClient.List(ctx, list,
					client.InNamespace(ns),
					client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName},
				)).To(Succeed())
				return list.Items
			}

			getOrder := func() *deliveryv1alpha1.Order {
				order := &deliveryv1alpha1.Order{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: orderName, Namespace: ns}, order)).To(Succeed())
				return order
			}

			reconcile()
			first := getOrder()
			firstHash := first.Status.LatestConfigHash
			firstDigest := first.Status.LatestArtifactDigest
			firstName := first.Status.LatestPreparationName
			Expect(listPreps()).To(HaveLen(1))

			order := getOrder()
			order.Spec.Edits = []deliveryv1alpha1.Patch{{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "app"},
				Set:    map[string]string{".spec.replicas": "3"},
			}}
			Expect(k8sClient.Update(ctx, order)).To(Succeed())

			reconcile()
			second := getOrder()
			Expect(second.Status.LatestConfigHash).NotTo(Equal(firstHash))
			Expect(second.Status.LatestPreparationName).NotTo(Equal(firstName))
			Expect(listPreps()).To(HaveLen(2))
			secondDigest := second.Status.LatestArtifactDigest
			secondName := second.Status.LatestPreparationName

			order = getOrder()
			order.Spec.Edits = nil
			Expect(k8sClient.Update(ctx, order)).To(Succeed())

			reconcile()
			third := getOrder()
			Expect(third.Status.LatestConfigHash).To(Equal(firstHash))
			Expect(third.Status.LatestPreparationName).NotTo(Equal(firstName))
			Expect(third.Status.LatestPreparationName).NotTo(Equal(secondName))
			Expect(third.Status.LatestArtifactDigest).NotTo(Equal(firstDigest))
			Expect(third.Status.LatestArtifactDigest).NotTo(Equal(secondDigest))

			preps := listPreps()
			Expect(preps).To(HaveLen(3))

			var latest deliveryv1alpha1.Preparation
			for _, p := range preps {
				if p.Name == third.Status.LatestPreparationName {
					latest = p
				}
			}
			Expect(latest.Name).To(Equal(third.Status.LatestPreparationName))
			Expect(latest.Spec.ConfigHash).To(Equal(firstHash))
			Expect(latest.Spec.ParentDigest).To(Equal(secondDigest))

			reconcile()
			Expect(listPreps()).To(HaveLen(3))
		})
	})
})
