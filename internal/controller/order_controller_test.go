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
	"encoding/json"
	stderrors "errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/oras-project/oras-go/v3/registry/remote/credentials"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/version"
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
			createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})

			By("creating the custom resource for the Kind Order")
			err := k8sClient.Get(ctx, typeNamespacedName, order)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Order{
					Name:      resourceName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.OrderSpec{
						EnvironmentRef: localEnvironment,
						ProductRef:     deliveryv1alpha1.ProductRef{Name: testProductName},
						Promotion: &deliveryv1alpha1.PromotionSpec{
							Mode: deliveryv1alpha1.PromotionModeManual,
						},
						Destination: &deliveryv1alpha1.OCIDestination{
							URL: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource",
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
			fake := oci.NewFakeClient(fs)
			controllerReconciler := &OrderReconciler{
				Client:           k8sClient,
				APIReader:        k8sClient,
				Scheme:           k8sClient.Scheme(),
				Pipeline:         artifact.NewPipeline(artifact.NewStore(fake, fs, "")),
				Registries:       fakeRegistries(fake),
				Recorder:         events.NewFakeRecorder(100),
				InstallNamespace: testNamespace,
				BuilderID:        testBuilderID,
			}

			err := settleOrder(ctx, controllerReconciler, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			preparationList := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, preparationList,
				client.InNamespace(testNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: resourceName},
			)).To(Succeed())
			Expect(preparationList.Items).To(HaveLen(1))
		})
	})

	Context("When the Order or its Product lists Registries", func() {
		const (
			ns          = testNamespace
			destination = "oci://ghcr.io/kokumi-test/rendered"
			source      = "oci://ghcr.io/kokumi-test/charts/app"
		)

		ctx := context.Background()

		// private serves the registries: a client with credentials resolves
		// everything, an anonymous one nothing, as for a private registry.
		var private *oci.FakeClient
		denied := &oci.FakeClient{Resolved: map[string]oci.Descriptor{}}

		newReconciler := func() *OrderReconciler {
			return &OrderReconciler{
				Client:    k8sClient,
				APIReader: k8sClient,
				Scheme:    k8sClient.Scheme(),
				Pipeline:  artifact.NewPipeline(artifact.NewStore(denied, afero.NewMemMapFs(), "")),
				Registries: testRegistries(func(opts oci.Options) oci.Client {
					if opts.Credential == (credentials.Credential{}) {
						return denied
					}
					return private
				}),
				Recorder:         events.NewFakeRecorder(100),
				InstallNamespace: testNamespace,
				BuilderID:        testBuilderID,
			}
		}

		BeforeEach(func() {
			private = oci.NewFakeClient(afero.NewMemMapFs())
		})

		listPreparations := func(orderName string) []deliveryv1alpha1.Preparation {
			list := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, list,
				client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName},
			)).To(Succeed())
			return list.Items
		}

		reconcileOrder := func(name string) {
			err := settleOrder(ctx, newReconciler(), types.NamespacedName{Namespace: ns, Name: name})
			Expect(err).NotTo(HaveOccurred())
		}

		getOrder := func(name string) *deliveryv1alpha1.Order {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, order)).To(Succeed())
			return order
		}

		readyOf := func(name string) *metav1.Condition {
			return meta.FindStatusCondition(getOrder(name).Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		}

		// createRegistry creates a Registry for url whose credentials are in
		// a Secret of its own.
		createRegistry := func(name, url string) {
			secret := testPullSecret(name, "ghcr.io")
			reg := &deliveryv1alpha1.Registry{
				Name: name, Namespace: ns,
				Spec: deliveryv1alpha1.RegistrySpec{URL: url, SecretRef: &corev1.LocalObjectReference{Name: name}},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			Expect(k8sClient.Create(ctx, reg)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, reg)
				_ = k8sClient.Delete(ctx, secret)
			})
		}

		// createOrder creates an Order of its own Product, named after the
		// Order, that renders source. The Product connects through
		// productRefs, the Order through orderRefs.
		createOrder := func(name string, productRefs, orderRefs []corev1.LocalObjectReference, dest *deliveryv1alpha1.OCIDestination) {
			createTestProduct(ctx, name, deliveryv1alpha1.OCISource{URL: source, Version: testVersion})
			product := getTestProduct(ctx, name)
			product.Spec.RegistryRefs = productRefs
			Expect(k8sClient.Update(ctx, product)).To(Succeed())
			resolveTestProduct(ctx, name, source, testVersion)

			order := &deliveryv1alpha1.Order{
				Name: name, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: name},
					Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
					Destination:    dest,
					RegistryRefs:   orderRefs,
				},
			}
			Expect(k8sClient.Create(ctx, order)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(ns), client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})
				_ = k8sClient.Delete(ctx, order)
				deleteTestProduct(ctx, name)
			})
		}

		refs := func(names ...string) []corev1.LocalObjectReference {
			out := make([]corev1.LocalObjectReference, 0, len(names))
			for _, name := range names {
				out = append(out, corev1.LocalObjectReference{Name: name})
			}
			return out
		}

		It("records the Registry that authenticated the push and what each client needs", func() {
			const orderName = "order-registries"
			createRegistry("order-registries-src", "oci://ghcr.io/kokumi-test/charts")
			createRegistry("order-registries-dest", destination)
			createOrder(orderName, refs("order-registries-src"), refs("order-registries-dest"),
				&deliveryv1alpha1.OCIDestination{URL: destination})

			reconcileOrder(orderName)

			Expect(readyOf(orderName).Status).To(Equal(metav1.ConditionTrue))
			preps := listPreparations(orderName)
			Expect(preps).To(HaveLen(1))
			Expect(preps[0].Spec.Artifact.RegistryRef).To(Equal(&corev1.LocalObjectReference{Name: "order-registries-dest"}))
			Expect(private.Pushes).To(HaveLen(1), "the push goes through the destination's Registry")

			Expect(getOrder(orderName).Status.RegistryAccess).To(ConsistOf(
				deliveryv1alpha1.RegistryAccess{URL: source, Operation: deliveryv1alpha1.RegistryOperationPull,
					Client: deliveryv1alpha1.RegistryClientKokumi, Registry: "order-registries-src"},
				deliveryv1alpha1.RegistryAccess{URL: destination, Operation: deliveryv1alpha1.RegistryOperationPush,
					Client: deliveryv1alpha1.RegistryClientKokumi, Registry: "order-registries-dest"},
				deliveryv1alpha1.RegistryAccess{URL: destination, Operation: deliveryv1alpha1.RegistryOperationPull,
					Client: deliveryv1alpha1.RegistryClientArgoCD},
			))
		})

		It("does not render again when only its Registries or their credentials change", func() {
			const orderName = "order-registries-change"
			createRegistry("order-registries-change-src", "oci://ghcr.io/kokumi-test/charts")
			createRegistry("order-registries-change-dest", destination)
			createOrder(orderName, refs("order-registries-change-src"), refs("order-registries-change-dest"),
				&deliveryv1alpha1.OCIDestination{URL: destination})

			reconcileOrder(orderName)
			hash := getOrder(orderName).Status.LatestConfigHash
			Expect(listPreparations(orderName)).To(HaveLen(1))

			By("rotating the credentials")
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "order-registries-change-dest"}, secret)).To(Succeed())
			secret.Data[corev1.DockerConfigJsonKey] = []byte(`{"auths":{"ghcr.io":{"username":"bot","password":"rotated"}}}`)
			Expect(k8sClient.Update(ctx, secret)).To(Succeed())
			reconcileOrder(orderName)

			By("listing another Registry")
			createRegistry("order-registries-change-other", "oci://quay.io/kokumi-test")
			order := getOrder(orderName)
			order.Spec.RegistryRefs = refs("order-registries-change-dest", "order-registries-change-other")
			Expect(k8sClient.Update(ctx, order)).To(Succeed())
			reconcileOrder(orderName)

			Expect(getOrder(orderName).Status.LatestConfigHash).To(Equal(hash))
			Expect(getOrder(orderName).Status.ObservedGeneration).To(Equal(getOrder(orderName).Generation))
			Expect(listPreparations(orderName)).To(HaveLen(1))
		})

		It("waits while a Registry it lists is missing", func() {
			const orderName = "order-registry-missing"
			createRegistry("order-registry-missing-src", "oci://ghcr.io/kokumi-test/charts")
			createOrder(orderName, refs("order-registry-missing-src"), refs("does-not-exist"),
				&deliveryv1alpha1.OCIDestination{URL: destination})

			reconcileOrder(orderName)

			ready := readyOf(orderName)
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonRegistryNotFound))
			Expect(getOrder(orderName).Status.LatestConfigHash).To(BeEmpty())
			Expect(listPreparations(orderName)).To(BeEmpty())
		})

		It("does not push a private source to a destination without credentials", func() {
			const orderName = "order-private-anonymous"
			createRegistry("order-private-anonymous-src", "oci://ghcr.io/kokumi-test/charts")
			createOrder(orderName, refs("order-private-anonymous-src"), nil, nil)

			reconcileOrder(orderName)

			ready := readyOf(orderName)
			Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonCredentialsRequired))
			Expect(ready.Message).To(ContainSubstring(artifact.DefaultDestination(ns, orderName)))
			Expect(listPreparations(orderName)).To(BeEmpty())
			Expect(private.Pushes).To(BeEmpty())
			Expect(denied.Pushes).To(BeEmpty())
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
			fake := oci.NewFakeClient(fs)
			return &OrderReconciler{
				Client:           k8sClient,
				APIReader:        k8sClient,
				Scheme:           k8sClient.Scheme(),
				Pipeline:         artifact.NewPipeline(artifact.NewStore(fake, fs, "")),
				Registries:       fakeRegistries(fake),
				Recorder:         events.NewFakeRecorder(100),
				InstallNamespace: testNamespace,
				BuilderID:        testBuilderID,
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
				deleteTestProduct(ctx, orderName)
			}()

			createTestProduct(ctx, orderName, deliveryv1alpha1.OCISource{
				URL:     "oci://registry.kokumi.svc.cluster.local:5000/order/edit-revert",
				Version: "0.1.0",
			})
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: orderName},
					Promotion: &deliveryv1alpha1.PromotionSpec{
						Mode: deliveryv1alpha1.PromotionModeManual,
					},
					Destination: &deliveryv1alpha1.OCIDestination{
						URL: "oci://registry.kokumi.svc.cluster.local:5000/preparation/edit-revert",
					},
				},
			})).To(Succeed())

			reconcile := func() {
				err := settleOrder(ctx, newReconciler(), types.NamespacedName{Namespace: ns, Name: orderName})
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
				Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName},
				Set:    map[string]string{testReplicasPath: "3"},
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

	Context("When the Order has an approval policy", func() {
		const orderName = "order-gated-auto"

		ctx := context.Background()

		It("snapshots the policy into the Preparation and creates the Automatic Serving", func() {
			policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 2, AllowedGroups: []string{testApproverGroup}}
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeAutomatic, policy)
			DeferCleanup(func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(testNamespace), client.MatchingFields{deliveryv1alpha1.FieldOrderName: orderName})
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: orderName, Namespace: testNamespace})
			})

			fs := afero.NewMemMapFs()
			fake := oci.NewFakeClient(fs)
			r := &OrderReconciler{
				Client:           k8sClient,
				APIReader:        k8sClient,
				Scheme:           k8sClient.Scheme(),
				Pipeline:         artifact.NewPipeline(artifact.NewStore(fake, fs, "")),
				Registries:       fakeRegistries(fake),
				Recorder:         events.NewFakeRecorder(100),
				InstallNamespace: testNamespace,
				BuilderID:        testBuilderID,
			}
			Expect(settleOrder(ctx, r, types.NamespacedName{Namespace: testNamespace, Name: orderName})).To(Succeed())

			preps := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, preps, client.InNamespace(testNamespace),
				client.MatchingFields{deliveryv1alpha1.FieldOrderName: orderName})).To(Succeed())
			Expect(preps.Items).To(HaveLen(1))
			Expect(preps.Items[0].ApprovalPolicy()).To(Equal(policy))
			Expect(preps.Items[0].Labels).NotTo(HaveKey("delivery.kokumi.dev/auto-deploy"))

			serving := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, serving)).To(Succeed())
			Expect(serving.Spec.OrderName).To(Equal(orderName))
			Expect(serving.Spec.PreparationName).To(BeEmpty())

			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, order)).To(Succeed())
			Expect(metav1.IsControlledBy(serving, order)).To(BeTrue())
		})
	})

	Context("When promoting creates the Serving of a Manual Order", func() {
		const orderName = "order-promoted-manual"

		ctx := context.Background()
		key := types.NamespacedName{Namespace: testNamespace, Name: orderName}

		var r *OrderReconciler

		BeforeEach(func() {
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual)
			DeferCleanup(func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(testNamespace), client.MatchingFields{deliveryv1alpha1.FieldOrderName: orderName})
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, key, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: orderName, Namespace: testNamespace})
			})

			r = newTestOrderReconciler(oci.NewFakeClient(afero.NewMemMapFs()))
			Expect(settleOrder(ctx, r, key)).To(Succeed())
			Expect(k8sClient.Get(ctx, key, &deliveryv1alpha1.Serving{})).To(Satisfy(apierrors.IsNotFound),
				"a Manual Order has no Serving before its first promote")
		})

		// promote creates the Serving as the API server does on the first
		// promote, with the given owner references.
		promote := func(owners ...metav1.OwnerReference) {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, order)).To(Succeed())
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
				Name:            orderName,
				Namespace:       testNamespace,
				Labels:          map[string]string{deliveryv1alpha1.LabelOrder: orderName},
				OwnerReferences: owners,
				Spec: deliveryv1alpha1.ServingSpec{
					OrderName:       orderName,
					PreparationName: order.Status.LatestPreparationName,
				},
			})).To(Succeed())
		}

		It("adopts the Serving, so that it goes with the Order", func() {
			promote()

			Expect(settleOrder(ctx, r, key)).To(Succeed())

			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, order)).To(Succeed())
			serving := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, key, serving)).To(Succeed())
			Expect(metav1.IsControlledBy(serving, order)).To(BeTrue())
			Expect(serving.Spec.PreparationName).To(Equal(order.Status.LatestPreparationName))
		})

		It("leaves the Serving of a deleted Order of the same name to the garbage collector", func() {
			deleted := metav1.OwnerReference{
				APIVersion: deliveryv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Order",
				Name:       orderName,
				UID:        "00000000-0000-0000-0000-000000000000",
				Controller: new(true),
			}
			promote(deleted)

			Expect(settleOrder(ctx, r, key)).To(Succeed())

			serving := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, key, serving)).To(Succeed())
			Expect(serving.OwnerReferences).To(ConsistOf(deleted))
		})
	})

	Context("When recording what a Preparation is made of", func() {
		const ns = testNamespace

		ctx := context.Background()

		var (
			fake *oci.FakeClient
			r    *OrderReconciler
		)

		BeforeEach(func() {
			fs := afero.NewMemMapFs()
			fake = oci.NewFakeClient(fs)
			r = &OrderReconciler{
				Client:           k8sClient,
				APIReader:        k8sClient,
				Scheme:           k8sClient.Scheme(),
				Pipeline:         artifact.NewPipeline(artifact.NewStore(fake, fs, "")),
				Registries:       fakeRegistries(fake),
				Recorder:         events.NewFakeRecorder(100),
				InstallNamespace: testNamespace,
				BuilderID:        testBuilderID,
			}
		})

		reconcileOrder := func(name string) error {
			return settleOrder(ctx, r, types.NamespacedName{Namespace: ns, Name: name})
		}

		getOrder := func(name string) *deliveryv1alpha1.Order {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order)).To(Succeed())
			return order
		}

		listPreparations := func(orderName string) []deliveryv1alpha1.Preparation {
			list := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, list, client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})).To(Succeed())
			return list.Items
		}

		cleanupOrder := func(name string) {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
				client.InNamespace(ns), client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})
			order := &deliveryv1alpha1.Order{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order); err == nil {
				order.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, order)
				_ = k8sClient.Delete(ctx, order)
			}
		}

		lastConfig := func() *artifact.Config {
			Expect(fake.Pushes).NotTo(BeEmpty())
			last := fake.Pushes[len(fake.Pushes)-1]
			config, err := artifact.ParseConfig(last.Options.ConfigMediaType, last.Options.Config)
			Expect(err).NotTo(HaveOccurred())
			return config
		}

		It("stores the intent in the artifact and an honest renderer on the Preparation", func() {
			const orderName = "order-intent"
			// The second group holds the characters encoding/json escapes by
			// default; the stored intent must keep them as hashed.
			policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1, AllowedGroups: []string{testApproverGroup, "ops & <release>"}}
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual, policy)
			DeferCleanup(func() { cleanupOrder(orderName) })

			Expect(reconcileOrder(orderName)).To(Succeed())
			order := getOrder(orderName)

			By("pushing a typed artifact whose config blob carries the intent")
			Expect(fake.Pushes).To(HaveLen(1))
			Expect(fake.Pushes[0].Options.ArtifactType).To(Equal(oci.PreparationArtifactType))
			config := lastConfig()
			Expect(config.Order.Name).To(Equal(orderName))
			Expect(config.Order.UID).To(Equal(order.UID))
			Expect(config.ConfigHash).To(Equal(order.Status.LatestConfigHash))
			Expect(config.Parent).To(BeEmpty())
			Expect(config.Product.Name).To(Equal(testProductName))
			Expect(config.Source.OCI).To(Equal(testOCIRef))
			Expect(config.Source.Version).To(Equal(testVersion))
			Expect(config.Source.Digest).To(HavePrefix("sha256:"))
			Expect(config.CreatedTime).NotTo(BeEmpty())

			By("hashing exactly the stored intent")
			var intent resolve.Intent
			Expect(json.Unmarshal(config.Intent, &intent)).To(Succeed())
			Expect(intent.Promotion.Approvals).To(Equal(policy))
			Expect(resolve.CalculateSpecHash(config.Intent)).To(Equal(config.ConfigHash))

			By("recording who rendered it")
			preps := listPreparations(orderName)
			Expect(preps).To(HaveLen(1))
			Expect(preps[0].Spec.Renderer.Type).To(Equal(deliveryv1alpha1.RenderTypeManifest))
			Expect(preps[0].Spec.Renderer.Builder).To(Equal(testBuilderID))
			Expect(preps[0].Spec.Renderer.Version).To(Equal("v" + version.Version))
			Expect(config.Renderer.Builder).To(Equal(testBuilderID))
		})

		It("does not render again when only the promotion mode changes", func() {
			const orderName = "order-mode-change"
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual)
			DeferCleanup(func() {
				cleanupOrder(orderName)
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: orderName, Namespace: ns})
			})

			Expect(reconcileOrder(orderName)).To(Succeed())

			order := getOrder(orderName)
			order.Spec.Promotion.Mode = deliveryv1alpha1.PromotionModeAutomatic
			Expect(k8sClient.Update(ctx, order)).To(Succeed())

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(listPreparations(orderName)).To(HaveLen(1))
			Expect(fake.Pushes).To(HaveLen(1))
		})

		It("renders again when the Product changes what is rendered", func() {
			const (
				orderName   = "order-of-product"
				productName = "product-for-order"
			)
			product := createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})

			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: productName},
					Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
				},
			})).To(Succeed())
			DeferCleanup(func() {
				cleanupOrder(orderName)
				deleteTestProduct(ctx, productName)
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(listPreparations(orderName)).To(HaveLen(1))
			config := lastConfig()
			Expect(config.Product.Name).To(Equal(productName))
			Expect(config.Product.ConfigHash).To(Equal(product.Status.ConfigHash))
			Expect(string(config.Intent)).NotTo(ContainSubstring("productRef"))

			By("leaving the Order alone while the Product is unchanged")
			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(listPreparations(orderName)).To(HaveLen(1))

			By("bumping the Product's source version without touching the Order")
			resolveTestProduct(ctx, productName, testOCIRef, "0.2.0")
			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(listPreparations(orderName)).To(HaveLen(2))
			Expect(lastConfig().Source.Version).To(Equal("0.2.0"))
		})
	})

	Context("When the Product is missing or not ready", func() {
		const (
			ns          = testNamespace
			orderName   = "order-waiting"
			productName = "product-arriving-late"
		)

		ctx := context.Background()

		var (
			fake *oci.FakeClient
			r    *OrderReconciler
		)

		BeforeEach(func() {
			fake = oci.NewFakeClient(afero.NewMemMapFs())
			r = newTestOrderReconciler(fake)

			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: productName},
					Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
				},
			})).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(ns), client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: orderName}, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
				deleteTestProduct(ctx, productName)
			})
		})

		reconcileOrder := func() {
			Expect(settleOrder(ctx, r, types.NamespacedName{Namespace: ns, Name: orderName})).To(Succeed())
		}

		readyOf := func() *metav1.Condition {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: orderName}, order)).To(Succeed())
			return meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		}

		preparations := func() []deliveryv1alpha1.Preparation {
			list := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, list, client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})).To(Succeed())
			return list.Items
		}

		It("waits for the Product and renders once it is ready", func() {
			By("waiting while the Product does not exist")
			reconcileOrder()
			Expect(readyOf().Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf().Reason).To(Equal(deliveryv1alpha1.ReasonProductNotFound))
			Expect(preparations()).To(BeEmpty())

			By("waiting while the Product has not resolved its source")
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Product{
				Name: productName, Namespace: ns,
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}},
			})).To(Succeed())
			reconcileOrder()
			Expect(readyOf().Reason).To(Equal(deliveryv1alpha1.ReasonProductNotReady))
			Expect(preparations()).To(BeEmpty())

			By("rendering once the Product is ready")
			resolveTestProduct(ctx, productName, testOCIRef, testVersion)
			reconcileOrder()
			Expect(readyOf().Status).To(Equal(metav1.ConditionTrue))
			Expect(preparations()).To(HaveLen(1))

			By("pulling exactly the artifact the Product resolved")
			Expect(fake.Pulls).To(HaveLen(1))
			Expect(fake.Pulls[0].Digest).To(Equal(oci.FakeDigest))
		})

		It("waits while its Product is being deleted", func() {
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Product{
				Name: productName, Namespace: ns,
				// Keeps the deleted Product, as the protection finalizer does.
				Finalizers: []string{deliveryv1alpha1.FinalizerProductProtection},
				Spec:       deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}},
			})).To(Succeed())
			resolveTestProduct(ctx, productName, testOCIRef, testVersion)
			Expect(k8sClient.Delete(ctx, getTestProduct(ctx, productName))).To(Succeed())

			reconcileOrder()
			Expect(readyOf().Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf().Reason).To(Equal(deliveryv1alpha1.ReasonProductDeleting))
			Expect(preparations()).To(BeEmpty())
		})

		It("does not render again when the Product comes back unchanged", func() {
			createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
			reconcileOrder()
			Expect(preparations()).To(HaveLen(1))

			By("waiting while a Product spec change is not resolved yet")
			product := &deliveryv1alpha1.Product{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: productName}, product)).To(Succeed())
			product.Spec.Patches = []deliveryv1alpha1.Patch{{
				Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName},
				Set:    map[string]string{testReplicasPath: "2"},
			}}
			Expect(k8sClient.Update(ctx, product)).To(Succeed())
			reconcileOrder()
			Expect(readyOf().Reason).To(Equal(deliveryv1alpha1.ReasonProductNotReady))

			By("reverting the change and resolving the Product again")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: productName}, product)).To(Succeed())
			product.Spec.Patches = nil
			Expect(k8sClient.Update(ctx, product)).To(Succeed())
			resolveTestProduct(ctx, productName, testOCIRef, testVersion)
			reconcileOrder()
			Expect(readyOf().Status).To(Equal(metav1.ConditionTrue))
			Expect(preparations()).To(HaveLen(1))
		})
	})

	Context("When the Order breaks what its Product allows", func() {
		const ns = testNamespace

		ctx := context.Background()

		create := func(orderName string, product *deliveryv1alpha1.Product, spec deliveryv1alpha1.OrderSpec) {
			Expect(k8sClient.Create(ctx, product)).To(Succeed())
			resolveTestProduct(ctx, product.Name, testOCIRef, testVersion)
			spec.ProductRef = deliveryv1alpha1.ProductRef{Name: product.Name}
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{Name: orderName, Namespace: ns, Spec: spec})).To(Succeed())
			DeferCleanup(func() {
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: orderName}, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
				deleteTestProduct(ctx, product.Name)
			})
		}

		expectRejected := func(orderName, message string) {
			r := newTestOrderReconciler(oci.NewFakeClient(afero.NewMemMapFs()))
			_, err := r.Reconcile(ctx, reconcile.Request{Namespace: ns, Name: orderName})
			Expect(err).To(HaveOccurred())
			Expect(stderrors.Is(err, reconcile.TerminalError(nil))).To(BeTrue(), "retrying does not help")

			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: orderName}, order)).To(Succeed())
			ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal("ProcessingFailed"))
			Expect(ready.Message).To(ContainSubstring(message))
		}

		It("rejects patches the Product does not allow", func() {
			create("order-forbidden-patch", &deliveryv1alpha1.Product{
				Name: "product-restricted", Namespace: ns,
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}},
			}, deliveryv1alpha1.OrderSpec{
				EnvironmentRef: localEnvironment,
				Patches: []deliveryv1alpha1.Patch{{
					Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName},
					Set:    map[string]string{testReplicasPath: "3"},
				}},
			})

			expectRejected("order-forbidden-patch", "patch target Deployment/app is not allowed")
			Expect(getTestProduct(ctx, "product-restricted").Spec.Overrides.Patches.Policy).
				To(Equal(deliveryv1alpha1.OverridePolicyNone), "Products allow no patches by default")
		})

		It("rejects values on a Product that does not render with Helm", func() {
			create("order-values-no-helm", &deliveryv1alpha1.Product{
				Name: "product-manifests", Namespace: ns,
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}},
			}, deliveryv1alpha1.OrderSpec{
				EnvironmentRef: localEnvironment,
				Values:         &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount":3}`)},
			})

			expectRejected("order-values-no-helm", "only allowed when the Product renders with Helm")
		})
	})

	Context("When the Order renders", func() {
		const orderName = "order-two-passes"

		ctx := context.Background()
		key := types.NamespacedName{Namespace: testNamespace, Name: orderName}

		BeforeEach(func() {
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual)
			DeferCleanup(func() {
				_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
					client.InNamespace(testNamespace), client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, key, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
			})
		})

		getOrder := func() *deliveryv1alpha1.Order {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, order)).To(Succeed())
			return order
		}

		It("reports Processing first and renders in the reconcile that report triggers", func() {
			fake := oci.NewFakeClient(afero.NewMemMapFs())
			r := newTestOrderReconciler(fake)

			By("reporting Processing without rendering")
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			order := getOrder()
			ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(ready.Status).To(Equal(metav1.ConditionUnknown))
			Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonProcessing))
			Expect(order.Status.LatestConfigHash).To(BeEmpty(), "the hash records finished renders only, so a crash renders again")
			Expect(order.Status.ObservedGeneration).To(Equal(order.Generation))
			Expect(fake.Pushes).To(BeEmpty())

			By("rendering in the next reconcile")
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			order = getOrder()
			Expect(meta.IsStatusConditionTrue(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(order.Status.LatestConfigHash).NotTo(BeEmpty())
			Expect(order.Status.LatestPreparationName).NotTo(BeEmpty())
			Expect(fake.Pushes).To(HaveLen(1))

			By("writing nothing once its latest Preparation renders its intent")
			_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(getOrder().ResourceVersion).To(Equal(order.ResourceVersion))
			Expect(fake.Pushes).To(HaveLen(1))
		})

		It("consumes the commit message with the render", func() {
			order := getOrder()
			order.Annotations = map[string]string{deliveryv1alpha1.AnnotationCommitMessage: "Raise the replicas"}
			Expect(k8sClient.Update(ctx, order)).To(Succeed())

			Expect(settleOrder(ctx, newTestOrderReconciler(oci.NewFakeClient(afero.NewMemMapFs())), key)).To(Succeed())

			order = getOrder()
			Expect(order.Annotations).NotTo(HaveKey(deliveryv1alpha1.AnnotationCommitMessage))
			Expect(meta.IsStatusConditionTrue(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue(),
				"removing the annotation keeps the status of the render")
			prep := &deliveryv1alpha1.Preparation{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: order.Status.LatestPreparationName}, prep)).To(Succeed())
			Expect(prep.Spec.CommitMessage).To(Equal("Raise the replicas"))
		})
	})

	Context("When the Product of an Order is changed", func() {
		It("rejects the change", func() {
			ctx := context.Background()
			order := &deliveryv1alpha1.Order{
				Name: "order-immutable-product", Namespace: testNamespace,
				Spec: deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: "one"}},
			}
			Expect(k8sClient.Create(ctx, order)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })

			order.Spec.ProductRef.Name = "other"
			err := k8sClient.Update(ctx, order)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("productRef is immutable"))
		})
	})

	Context("When the Environment of an Order is changed", func() {
		It("rejects the change", func() {
			ctx := context.Background()
			order := &deliveryv1alpha1.Order{
				Name: "order-immutable-environment", Namespace: testNamespace,
				Spec: deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: testProductName}},
			}
			Expect(k8sClient.Create(ctx, order)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })

			order.Spec.EnvironmentRef.Name = "prod"
			Expect(k8sClient.Update(ctx, order)).To(MatchError(ContainSubstring("environmentRef is immutable")))
		})
	})

	Context("When the values of an Order are not an object", func() {
		It("rejects the Order", func() {
			err := k8sClient.Create(context.Background(), &deliveryv1alpha1.Order{
				Name: "order-values-string", Namespace: testNamespace,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: "one"},
					Values:         &apiextensionsv1.JSON{Raw: []byte(`"replicaCount=3"`)},
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be of type object"))
		})
	})
})

const (
	// testProductName is the Product the Orders of createTestOrder render from.
	testProductName = "test-product"

	testDeploymentKind = "Deployment"
	testDeploymentName = "app"
	testReplicasPath   = ".spec.replicas"
)

// settleOrder reconciles the Order of key until a pass leaves it as it was,
// as the watch on the Order's own writes does in a cluster: a render takes
// one pass that reports Processing and one that renders. It returns the
// error of the first pass that fails.
func settleOrder(ctx context.Context, r *OrderReconciler, key types.NamespacedName) error {
	for range 5 {
		before := &deliveryv1alpha1.Order{}
		if err := k8sClient.Get(ctx, key, before); err != nil {
			return err
		}
		if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			return err
		}
		after := &deliveryv1alpha1.Order{}
		if err := k8sClient.Get(ctx, key, after); err != nil {
			return err
		}
		if after.ResourceVersion == before.ResourceVersion {
			return nil
		}
	}
	return fmt.Errorf("order %s did not settle", key)
}

// newTestOrderReconciler returns an OrderReconciler pushing to fake.
func newTestOrderReconciler(fake *oci.FakeClient) *OrderReconciler {
	return &OrderReconciler{
		Client:           k8sClient,
		APIReader:        k8sClient,
		Scheme:           k8sClient.Scheme(),
		Pipeline:         artifact.NewPipeline(artifact.NewStore(fake, afero.NewMemMapFs(), "")),
		Registries:       fakeRegistries(fake),
		Recorder:         events.NewFakeRecorder(100),
		InstallNamespace: testNamespace,
		BuilderID:        testBuilderID,
	}
}

// createTestProduct creates a Product with the given source, whose Orders
// may change anything, and marks it resolved as the Product controller would;
// an existing Product is only resolved again.
func createTestProduct(ctx context.Context, name string, source deliveryv1alpha1.OCISource) *deliveryv1alpha1.Product {
	product := &deliveryv1alpha1.Product{
		Name:      name,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.ProductSpec{
			Source: source,
			Overrides: deliveryv1alpha1.Overrides{
				Values:  deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
				Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyAll},
			},
		},
	}
	if err := k8sClient.Create(ctx, product); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	return resolveTestProduct(ctx, name, source.URL, source.Version)
}

// resolveTestProduct publishes the source at sourceOCI and sourceVersion,
// resolved to oci.FakeDigest, in the Product's status, as the Product
// controller does once it has resolved the current generation.
func resolveTestProduct(ctx context.Context, name, sourceOCI, sourceVersion string) *deliveryv1alpha1.Product {
	product := getTestProduct(ctx, name)

	product.Status.ConfigHash = "sha256:product-" + sourceVersion
	product.Status.Source = &deliveryv1alpha1.ProductSourceStatus{
		URL:     sourceOCI,
		Version: sourceVersion,
		Digest:  oci.FakeDigest,
	}
	meta.SetStatusCondition(&product.Status.Conditions, metav1.Condition{
		Type:               deliveryv1alpha1.ConditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             "Resolved",
		Message:            "Source resolved",
		ObservedGeneration: product.Generation,
	})
	Expect(k8sClient.Status().Update(ctx, product)).To(Succeed())

	return product
}

// getTestProduct returns the named Product.
func getTestProduct(ctx context.Context, name string) *deliveryv1alpha1.Product {
	product := &deliveryv1alpha1.Product{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, product)).To(Succeed())
	return product
}

// deleteTestProduct deletes the named Product, bypassing its protection.
func deleteTestProduct(ctx context.Context, name string) {
	product := &deliveryv1alpha1.Product{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, product); err != nil {
		return
	}
	product.SetFinalizers(nil)
	_ = k8sClient.Update(ctx, product)
	_ = k8sClient.Delete(ctx, product)
}

// createTestOrder creates an Order of the resolved Product testProductName
// with the given promotion mode and optional approval policy; an existing
// Order is left as is.
func createTestOrder(ctx context.Context, name string, mode deliveryv1alpha1.PromotionMode, policy ...*deliveryv1alpha1.ApprovalPolicy) {
	createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})

	order := &deliveryv1alpha1.Order{
		Name:      name,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.OrderSpec{
			EnvironmentRef: localEnvironment,
			ProductRef:     deliveryv1alpha1.ProductRef{Name: testProductName},
			Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: mode},
		},
	}
	if len(policy) > 0 {
		order.Spec.Promotion.Approvals = policy[0]
	}
	if err := k8sClient.Create(ctx, order); err != nil && !apierrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

// imagesMissingRef is the image of imagesManifest that only fakes that
// resolve everything resolve.
const imagesMissingRef = "ghcr.io/example/missing:1"

const (
	imagesNginxName = "docker.io/library/nginx"
	imagesNginxRef  = "nginx:1.27"
	// imagesNginxLookup is the reference nginx:1.27 is looked up by.
	imagesNginxLookup = imagesNginxName + ":1.27"
	imagesNginxDigest = "sha256:abababababababababababababababababababababababababababababababab"
	imagesManifest    = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
        - name: helper
          image: ghcr.io/example/missing:1
`
)

// workloadClient pulls a manifest with images and resolves only nginx:1.27.
type workloadClient struct {
	*oci.FakeClient
	fs afero.Fs
	// onPull, when set, runs at each pull, while a render is in progress.
	onPull func()
}

func (c *workloadClient) Pull(_ context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	c.Pulls = append(c.Pulls, ref)
	if c.onPull != nil {
		c.onPull()
	}
	if err := afero.WriteFile(c.fs, filepath.Join(targetDir, "manifest.yaml"), []byte(imagesManifest), 0600); err != nil {
		return "", "", nil, err
	}
	return "", oci.FakeDigest, nil, nil
}

// Resolve resolves the source artifact, which the Product pinned to
// oci.FakeDigest, and leaves the images to the fake's Resolved.
func (c *workloadClient) Resolve(ctx context.Context, ref oci.Reference) (oci.Descriptor, error) {
	if ref.Digest == oci.FakeDigest {
		return oci.Descriptor{Digest: oci.FakeDigest, MediaType: ocispec.MediaTypeImageManifest}, nil
	}
	return c.FakeClient.Resolve(ctx, ref)
}

// laggingCache reads like an informer cache that has not caught up with the
// last render: it serves the Order from a snapshot taken before, when one is
// set, and holds none of the Preparations.
type laggingCache struct {
	client.Client
	order *deliveryv1alpha1.Order
}

func (c *laggingCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch obj := obj.(type) {
	case *deliveryv1alpha1.Order:
		if c.order != nil && key == client.ObjectKeyFromObject(c.order) {
			c.order.DeepCopyInto(obj)
			return nil
		}
	case *deliveryv1alpha1.Preparation:
		return apierrors.NewNotFound(deliveryv1alpha1.GroupVersion.WithResource("preparations").GroupResource(), key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// capturedEvent is an Event a capturingRecorder received.
type capturedEvent struct {
	regarding, related              runtime.Object
	eventType, reason, action, note string
}

// capturingRecorder records Events with the objects they name, which
// events.FakeRecorder reduces to a string.
type capturingRecorder struct {
	mu     sync.Mutex
	events []capturedEvent
}

var _ events.EventRecorder = (*capturingRecorder)(nil)

func (r *capturingRecorder) Eventf(regarding, related runtime.Object, eventType, reason, action, note string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, capturedEvent{
		regarding: regarding, related: related,
		eventType: eventType, reason: reason, action: action, note: fmt.Sprintf(note, args...),
	})
}

var _ = Describe("Order images", func() {
	ctx := context.Background()

	setupImages := func(productName, orderName string, images deliveryv1alpha1.ProductImages) {
		createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		DeferCleanup(deleteTestProduct, ctx, productName)

		product := getTestProduct(ctx, productName)
		product.Spec.Images = images
		Expect(k8sClient.Update(ctx, product)).To(Succeed())
		resolveTestProduct(ctx, productName, testOCIRef, testVersion)

		order := &deliveryv1alpha1.Order{
			Name:      orderName,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: productName}},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{}, client.InNamespace(testNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})
			_ = k8sClient.Delete(ctx, order)
		})
	}

	newImagesReconciler := func(resolved map[string]oci.Descriptor) (*OrderReconciler, *workloadClient, *capturingRecorder) {
		fs := afero.NewMemMapFs()
		registry := &workloadClient{FakeClient: &oci.FakeClient{Resolved: resolved}, fs: fs}
		recorder := &capturingRecorder{}
		return &OrderReconciler{
			Client:           k8sClient,
			APIReader:        k8sClient,
			Scheme:           k8sClient.Scheme(),
			Pipeline:         artifact.NewPipeline(artifact.NewStore(registry, fs, "")),
			Registries:       fakeRegistries(registry),
			Recorder:         recorder,
			InstallNamespace: testNamespace,
			BuilderID:        testBuilderID,
		}, registry, recorder
	}

	render := func(r *OrderReconciler, orderName string) error {
		return settleOrder(ctx, r, types.NamespacedName{Namespace: testNamespace, Name: orderName})
	}

	reconcileImages := func(productName, orderName string, images deliveryv1alpha1.ProductImages) (*workloadClient, *capturingRecorder, error) {
		setupImages(productName, orderName, images)
		r, registry, recorder := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		})
		return registry, recorder, render(r, orderName)
	}

	preparationOf := func(orderName string) deliveryv1alpha1.Preparation {
		list := &deliveryv1alpha1.PreparationList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace),
			client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		return list.Items[0]
	}

	It("records every image and warns about the ones it could not resolve", func() {
		registry, recorder, err := reconcileImages("images-record", "images-record", deliveryv1alpha1.ProductImages{})
		Expect(err).NotTo(HaveOccurred())

		prep := preparationOf("images-record")
		Expect(prep.Spec.Images).To(Equal([]deliveryv1alpha1.PreparationImage{
			{Ref: imagesMissingRef, Name: "ghcr.io/example/missing"},
			{Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		}))

		Expect(recorder.events).To(HaveLen(1), "one Event per render")
		event := recorder.events[0]
		Expect(event.eventType).To(Equal(corev1.EventTypeWarning))
		Expect(event.reason).To(Equal(deliveryv1alpha1.ReasonImageNotResolved))
		Expect(event.note).To(HavePrefix("Could not resolve 1 of 2 images: ghcr.io/example/missing:1: "))
		Expect(event.regarding.(*deliveryv1alpha1.Order).Name).To(Equal("images-record"))
		Expect(event.related).To(BeAssignableToTypeOf(&deliveryv1alpha1.Preparation{}))
		Expect(event.related.(*deliveryv1alpha1.Preparation).Name).To(Equal(prep.Name),
			"the Event names the Preparation, so client-go keeps the Events of two renders apart")

		pushed := registry.Pushes[len(registry.Pushes)-1]
		config, err := artifact.ParseConfig(pushed.Options.ConfigMediaType, pushed.Options.Config)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Images).To(HaveLen(2))
		Expect(config.Images[0].Error).To(ContainSubstring("not found"))
		Expect(string(config.Intent)).NotTo(ContainSubstring(`"images"`), "the default policy leaves the intent unchanged")
	})

	It("fails the render under Pin when an image cannot be resolved", func() {
		_, _, err := reconcileImages("images-pin", "images-pin", deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(imagesMissingRef))

		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-pin"}, order)).To(Succeed())
		ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Message).To(ContainSubstring("Pin image policy"))
	})

	It("vendors the images below the Product's vendor location", func() {
		const (
			name      = "images-vendor"
			vendorURL = "oci://registry.kokumi.svc.cluster.local:5000/default/vendor/images-vendor"
		)
		createTestProduct(ctx, name, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		DeferCleanup(deleteTestProduct, ctx, name)
		product := getTestProduct(ctx, name)
		product.Spec.Images = deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyVendor}
		product.Spec.Vendor = &deliveryv1alpha1.OCIDestination{URL: vendorURL}
		Expect(k8sClient.Update(ctx, product)).To(Succeed())
		// The Product publishes its vendored copy.
		resolveTestProduct(ctx, name, vendorURL, testVersion)

		order := &deliveryv1alpha1.Order{
			Name:      name,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: name}},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{}, client.InNamespace(testNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})
			_ = k8sClient.Delete(ctx, order)
		})

		r, registry, _ := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
			imagesMissingRef:  {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		})
		Expect(render(r, name)).To(Succeed())

		vendoredRoot := strings.TrimPrefix(vendorURL, "oci://") + "/images/"
		for _, img := range preparationOf(name).Spec.Images {
			Expect(img.Vendored).To(HavePrefix(vendoredRoot), "below the vendor location, not the Order's destination")
		}
		Expect(registry.Copies).NotTo(BeEmpty())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, order)).To(Succeed())
		Expect(order.Status.RegistryAccess).To(ContainElements(
			deliveryv1alpha1.RegistryAccess{URL: vendorURL + "/images", Operation: deliveryv1alpha1.RegistryOperationPush, Client: deliveryv1alpha1.RegistryClientKokumi},
			deliveryv1alpha1.RegistryAccess{URL: vendorURL + "/images", Operation: deliveryv1alpha1.RegistryOperationPull, Client: deliveryv1alpha1.RegistryClientKubelet},
		))
	})

	It("puts the image policy into the intent", func() {
		registry, _, err := reconcileImages("images-intent", "images-intent", deliveryv1alpha1.ProductImages{
			FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Kind: "Prometheus", Path: "spec/image"}},
		})
		Expect(err).NotTo(HaveOccurred())

		pushed := registry.Pushes[len(registry.Pushes)-1]
		config, err := artifact.ParseConfig(pushed.Options.ConfigMediaType, pushed.Options.Config)
		Expect(err).NotTo(HaveOccurred())

		var intent map[string]any
		Expect(json.Unmarshal(config.Intent, &intent)).To(Succeed())
		Expect(intent["images"]).To(Equal(map[string]any{
			"policy":     "Record",
			"fieldSpecs": []any{map[string]any{"kind": "Prometheus", "path": "spec/image"}},
		}))
	})

	It("keeps the digests of the latest Preparation when a Pin Order renders again", func() {
		const (
			movedDigest   = "sha256:cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
			missingDigest = "sha256:efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
		)
		setupImages("images-lock", "images-lock", deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin})
		r, registry, _ := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
			imagesMissingRef:  {Digest: missingDigest, MediaType: ocispec.MediaTypeImageManifest},
		})
		Expect(render(r, "images-lock")).To(Succeed())

		By("moving the tag upstream and changing the Order before the cache holds the first Preparation")
		registry.Resolved[imagesNginxLookup] = oci.Descriptor{Digest: movedDigest, MediaType: ocispec.MediaTypeImageIndex}
		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-lock"}, order)).To(Succeed())
		first := order.Status.LatestPreparationName
		order.Spec.Edits = []deliveryv1alpha1.Patch{{
			Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: "web"},
			Set:    map[string]string{testReplicasPath: "3"},
		}}
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		registry.Resolves = nil
		lagging := *r
		lagging.Client = &laggingCache{Client: k8sClient}
		Expect(render(&lagging, "images-lock")).To(Succeed())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-lock"}, order)).To(Succeed())
		Expect(order.Status.LatestPreparationName).NotTo(Equal(first), "the edit renders a new Preparation")
		prep := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: order.Status.LatestPreparationName}, prep)).To(Succeed())
		Expect(prep.Spec.Images).To(ContainElement(deliveryv1alpha1.PreparationImage{
			Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex,
		}), "the locked digest is kept, not the moved tag")
		Expect(registry.Resolves).To(BeEmpty(), "locked references are not looked up again")
	})

	It("does not render again while the cache still holds the Order as it was while rendering", func() {
		setupImages("images-stale", "images-stale", deliveryv1alpha1.ProductImages{})
		r, registry, _ := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		})
		key := types.NamespacedName{Namespace: testNamespace, Name: "images-stale"}
		var rendering *deliveryv1alpha1.Order
		registry.onPull = func() {
			rendering = &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, rendering)).To(Succeed())
		}
		Expect(render(r, "images-stale")).To(Succeed())
		registry.onPull = nil
		Expect(registry.Pushes).To(HaveLen(1))
		Expect(rendering.Status.LatestPreparationName).To(BeEmpty(), "the snapshot predates the render's Ready write")

		By("reconciling with a cache that has not seen the render's Ready write")
		lagging := *r
		lagging.Client = &laggingCache{Client: k8sClient, order: rendering}
		_, err := lagging.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(registry.Pushes).To(HaveLen(1), "the Order is not rendered again")
		Expect(err).NotTo(HaveOccurred())
		preparationOf("images-stale")
	})
})

var _ = Describe("Image schema", func() {
	ctx := context.Background()

	It("accepts image digests with every algorithm the OCI image spec registers", func() {
		sha512 := "sha512:" + strings.Repeat("ab", 64)
		digest := "sha256:" + strings.Repeat("cd", 32)
		prep := &deliveryv1alpha1.Preparation{
			Name:      "images-sha512",
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.PreparationSpec{
				TargetNamespace: testNamespace,
				OrderName:       "images-sha512",
				Source:          deliveryv1alpha1.OrderSource{URL: testOCIRef, BaseDigest: digest},
				Renderer: deliveryv1alpha1.Renderer{
					Type:    deliveryv1alpha1.RenderTypeManifest,
					Version: testRendererVersion,
					Builder: testBuilderID,
				},
				ConfigHash:      testConfigHash,
				ProductRevision: testProductRevision,
				Artifact: deliveryv1alpha1.Artifact{
					OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/images-sha512@" + digest,
					Digest: digest,
				},
				Images: []deliveryv1alpha1.PreparationImage{
					{Ref: "ghcr.io/example/app@" + sha512, Name: "ghcr.io/example/app", Digest: sha512},
					{Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest},
				},
			},
		}
		Expect(k8sClient.Create(ctx, prep)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, prep) })
	})

	It("requires mirror prefixes to start with a registry host", func() {
		kitchen := func(prefix string) *deliveryv1alpha1.Kitchen {
			return &deliveryv1alpha1.Kitchen{
				Name:      deliveryv1alpha1.DefaultKitchenName,
				Namespace: testNamespace,
				Spec: deliveryv1alpha1.KitchenSpec{Images: &deliveryv1alpha1.KitchenImages{
					Mirrors: []deliveryv1alpha1.ImageMirror{{Prefix: prefix, Location: "oci://harbor.example.com/hub"}},
				}},
			}
		}

		for _, prefix := range []string{"docker.io", "docker.io/library", "localhost:5000/app", "localhost", "registry:5000"} {
			Expect(k8sClient.Create(ctx, kitchen(prefix), client.DryRunAll)).To(Succeed(), prefix)
		}
		for _, prefix := range []string{"library/nginx", "nginx"} {
			Expect(k8sClient.Create(ctx, kitchen(prefix), client.DryRunAll)).To(
				MatchError(ContainSubstring("must start with a registry host")), prefix)
		}
	})
})

var _ = Describe("Order field indexes", func() {
	It("indexes an Order by every Registry it lists", func() {
		const upstream, mirror = "order-upstream", "order-mirror"
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{
			EnvironmentRef: localEnvironment,
			ProductRef:     deliveryv1alpha1.ProductRef{Name: "chart"},
			RegistryRefs:   []corev1.LocalObjectReference{{Name: upstream}, {Name: mirror}},
		}}
		Expect(orderRegistryNames(order)).To(Equal([]string{upstream, mirror}))
		Expect(orderRegistryNames(&deliveryv1alpha1.Order{})).To(BeEmpty())
		Expect(orderProduct(order)).To(Equal([]string{"chart"}))
	})
})

var _ = Describe("servingControllerChanged", func() {
	// servingOwnedBy returns a Serving whose controller has the given UID,
	// or that has none for "".
	servingOwnedBy := func(uid types.UID) *deliveryv1alpha1.Serving {
		const orderName = "order-watched"
		serving := &deliveryv1alpha1.Serving{Name: orderName, Namespace: testNamespace}
		if uid != "" {
			serving.OwnerReferences = []metav1.OwnerReference{{
				APIVersion: deliveryv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Order",
				Name:       orderName,
				UID:        uid,
				Controller: new(true),
			}}
		}
		return serving
	}

	It("passes creates and deletes, and no generic events", func() {
		Expect(servingControllerChanged.Create(event.CreateEvent{Object: servingOwnedBy("")})).To(BeTrue())
		Expect(servingControllerChanged.Delete(event.DeleteEvent{Object: servingOwnedBy("a")})).To(BeTrue())
		Expect(servingControllerChanged.Generic(event.GenericEvent{Object: servingOwnedBy("a")})).To(BeFalse())
	})

	DescribeTable("passes updates that change the controller",
		func(oldUID, newUID types.UID, passes bool) {
			old, updated := servingOwnedBy(oldUID), servingOwnedBy(newUID)
			updated.Status.TargetPreparationName = "order-watched-0123456789ab"
			Expect(servingControllerChanged.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})).To(Equal(passes))
		},
		Entry("adopted", types.UID(""), types.UID("a"), true),
		Entry("orphaned", types.UID("a"), types.UID(""), true),
		Entry("controlled by another Order", types.UID("a"), types.UID("b"), true),
		Entry("unchanged controller", types.UID("a"), types.UID("a"), false),
		Entry("no controller", types.UID(""), types.UID(""), false),
	)
})

var _ = Describe("Order pipelines", func() {
	const (
		ns           = testNamespace
		otherProduct = "pipeline-other-product"
	)

	ctx := context.Background()

	var (
		fake *oci.FakeClient
		r    *OrderReconciler
	)

	BeforeEach(func() {
		fake = oci.NewFakeClient(afero.NewMemMapFs())
		r = newTestOrderReconciler(fake)
	})

	// createStage creates a Manual Order of product that runs after the
	// given upstream stages.
	createStage := func(name, product string, after ...deliveryv1alpha1.UpstreamStage) {
		createTestProduct(ctx, product, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
			Name: name, Namespace: ns,
			Spec: deliveryv1alpha1.OrderSpec{
				EnvironmentRef: localEnvironment,
				ProductRef:     deliveryv1alpha1.ProductRef{Name: product},
				Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual, After: after},
			},
		})).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
				client.InNamespace(ns), client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})
			order := &deliveryv1alpha1.Order{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order); err == nil {
				order.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, order)
				_ = k8sClient.Delete(ctx, order)
			}
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: name, Namespace: ns})
		})
	}

	settle := func(name string) {
		Expect(settleOrder(ctx, r, types.NamespacedName{Namespace: ns, Name: name})).To(Succeed())
	}

	ready := func(name string) *metav1.Condition {
		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order)).To(Succeed())
		return meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}

	preparationsOf := func(name string) []deliveryv1alpha1.Preparation {
		list := &deliveryv1alpha1.PreparationList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(ns),
			client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})).To(Succeed())
		return list.Items
	}

	It("renders a stage with its upstream stages and the Product revision", func() {
		createStage("pipeline-dev", testProductName)
		createStage("pipeline-prod", testProductName, deliveryv1alpha1.UpstreamStage{Order: "pipeline-dev", SoakSeconds: 600})
		settle("pipeline-dev")
		settle("pipeline-prod")

		Expect(ready("pipeline-prod").Status).To(Equal(metav1.ConditionTrue))
		preps := preparationsOf("pipeline-prod")
		Expect(preps).To(HaveLen(1))
		Expect(preps[0].UpstreamStages()).To(Equal([]deliveryv1alpha1.UpstreamStage{{Order: "pipeline-dev", SoakSeconds: 600}}))

		revision, err := resolve.ProductRevision(getTestProduct(ctx, testProductName))
		Expect(err).NotTo(HaveOccurred())
		Expect(preps[0].Spec.ProductRevision).To(Equal(revision))

		By("recording the revision in the intent of the artifact")
		last := fake.Pushes[len(fake.Pushes)-1]
		config, err := artifact.ParseConfig(last.Options.ConfigMediaType, last.Options.Config)
		Expect(err).NotTo(HaveOccurred())
		var intent resolve.Intent
		Expect(json.Unmarshal(config.Intent, &intent)).To(Succeed())
		Expect(intent.ProductRevision).To(Equal(revision))
		Expect(intent.Promotion.After).To(Equal(preps[0].UpstreamStages()))

		By("selecting the Preparations every stage rendered from the revision")
		list := &deliveryv1alpha1.PreparationList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(ns),
			client.MatchingFields{deliveryv1alpha1.FieldProductRevision: revision})).To(Succeed())
		orders := make([]string, 0, len(list.Items))
		for _, p := range list.Items {
			orders = append(orders, p.Spec.OrderName)
		}
		Expect(orders).To(ContainElements("pipeline-dev", "pipeline-prod"))
	})

	It("renders a stage whose upstream Order does not exist yet; its Serving waits for it", func() {
		createStage("pipeline-early", testProductName, deliveryv1alpha1.UpstreamStage{Order: "pipeline-later"})
		settle("pipeline-early")
		Expect(ready("pipeline-early").Status).To(Equal(metav1.ConditionTrue))
	})

	It("does not render a stage that runs after an Order of another Product", func() {
		createStage("pipeline-foreign", otherProduct)
		DeferCleanup(func() { deleteTestProduct(ctx, otherProduct) })
		createStage("pipeline-mixed", testProductName, deliveryv1alpha1.UpstreamStage{Order: "pipeline-foreign"})
		settle("pipeline-mixed")

		cond := ready("pipeline-mixed")
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonInvalidPipeline))
		Expect(cond.Message).To(ContainSubstring("Product " + otherProduct))
		Expect(preparationsOf("pipeline-mixed")).To(BeEmpty())
	})

	It("reports a cycle on every Order in it", func() {
		createStage("pipeline-a", testProductName, deliveryv1alpha1.UpstreamStage{Order: "pipeline-b"})
		createStage("pipeline-b", testProductName, deliveryv1alpha1.UpstreamStage{Order: "pipeline-a"})
		settle("pipeline-a")
		settle("pipeline-b")

		for _, name := range []string{"pipeline-a", "pipeline-b"} {
			cond := ready(name)
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonInvalidPipeline), name)
			Expect(cond.Message).To(ContainSubstring("cycle"))
			Expect(preparationsOf(name)).To(BeEmpty())
		}
	})

	It("indexes an Order by the Orders it runs after", func() {
		order := &deliveryv1alpha1.Order{Spec: deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, Promotion: &deliveryv1alpha1.PromotionSpec{
			After: []deliveryv1alpha1.UpstreamStage{{Order: "staging"}, {Order: "qa", SoakSeconds: 60}},
		}}}
		Expect(orderUpstreamNames(order)).To(Equal([]string{"staging", "qa"}))
		Expect(orderUpstreamNames(&deliveryv1alpha1.Order{})).To(BeEmpty())
	})

	It("adopts the Serving of a stage that does not render", func() {
		const loop, loopBack = "pipeline-loop", "pipeline-loop-back"
		createStage(loop, testProductName, deliveryv1alpha1.UpstreamStage{Order: loopBack})
		createStage(loopBack, testProductName, deliveryv1alpha1.UpstreamStage{Order: loop})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
			Name: loop, Namespace: ns,
			Spec: deliveryv1alpha1.ServingSpec{OrderName: loop, PreparationName: "promoted-before"},
		})).To(Succeed())

		settle(loop)
		Expect(ready(loop).Reason).To(Equal(deliveryv1alpha1.ReasonInvalidPipeline))

		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: loop}, order)).To(Succeed())
		serving := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: loop}, serving)).To(Succeed())
		Expect(metav1.IsControlledBy(serving, order)).To(BeTrue())
	})

	It("renders every stage again when a Product change its own patches hide", func() {
		const product = "pipeline-hidden-change"
		patch := func(replicas string) []deliveryv1alpha1.Patch {
			return []deliveryv1alpha1.Patch{{
				Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName},
				Set:    map[string]string{testReplicasPath: replicas},
			}}
		}
		createTestProduct(ctx, product, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		DeferCleanup(func() { deleteTestProduct(ctx, product) })
		p := getTestProduct(ctx, product)
		p.Spec.Patches = patch("1")
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
		resolveTestProduct(ctx, product, testOCIRef, testVersion)

		createStage("pipeline-hiding", product)
		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "pipeline-hiding"}, order)).To(Succeed())
		order.Spec.Patches = patch("3")
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		settle("pipeline-hiding")
		Expect(preparationsOf("pipeline-hiding")).To(HaveLen(1))

		By("changing the Product's patch, which the Order's own patch overrides")
		p = getTestProduct(ctx, product)
		p.Spec.Patches = patch("2")
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
		resolveTestProduct(ctx, product, testOCIRef, testVersion)
		settle("pipeline-hiding")

		preps := preparationsOf("pipeline-hiding")
		Expect(preps).To(HaveLen(2), "every Product revision gets a Preparation in every stage")
		revision, err := resolve.ProductRevision(getTestProduct(ctx, product))
		Expect(err).NotTo(HaveOccurred())
		Expect([]string{preps[0].Spec.ProductRevision, preps[1].Spec.ProductRevision}).To(ContainElement(revision))
	})
})

var _ = Describe("pipelineChanged", func() {
	invalid := func(generation int64, invalid bool) *deliveryv1alpha1.Order {
		order := &deliveryv1alpha1.Order{}
		order.Generation = generation
		if invalid {
			order.Status.Conditions = []metav1.Condition{{Type: deliveryv1alpha1.ConditionTypeReady, Reason: deliveryv1alpha1.ReasonInvalidPipeline}}
		}
		return order
	}

	DescribeTable("passes the updates after which the Orders after it check their pipeline again",
		func(oldOrder, newOrder *deliveryv1alpha1.Order, want bool) {
			Expect(pipelineChanged.Update(event.UpdateEvent{ObjectOld: oldOrder, ObjectNew: newOrder})).To(Equal(want))
		},
		Entry("a spec change", invalid(1, false), invalid(2, false), true),
		Entry("becoming an invalid stage", invalid(1, false), invalid(1, true), true),
		Entry("staying an invalid stage", invalid(1, true), invalid(1, true), false),
		Entry("a status write", invalid(1, false), invalid(1, false), false),
	)
})

var _ = Describe("The Order's watch of its Preparations", func() {
	It("passes the start of a deletion, and not a status write", func() {
		ctx := context.Background()
		const digest = "sha256:0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
		watched := predicate.GenerationChangedPredicate{}
		prep := createTestPreparation(ctx, "watched-0f0f0f0f0f0f", "watched", digest, nil)
		prep.Finalizers = []string{deliveryv1alpha1.Finalizer}
		Expect(k8sClient.Update(ctx, prep)).To(Succeed())
		DeferCleanup(func() {
			current := &deliveryv1alpha1.Preparation{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(prep), current); err == nil {
				current.Finalizers = nil
				Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}
		})

		old := prep.DeepCopy()
		meta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeSigned, Status: metav1.ConditionTrue, Reason: testReadyReason,
		})
		Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())
		Expect(watched.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: prep})).To(BeFalse(), "a status write changes no generation")

		old = prep.DeepCopy()
		Expect(k8sClient.Delete(ctx, prep)).To(Succeed())
		deleting := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(prep), deleting)).To(Succeed())
		Expect(deleting.DeletionTimestamp).NotTo(BeNil())
		Expect(watched.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: deleting})).To(BeTrue(), "the start of a deletion changes the generation")
	})
})

// laggingReader reads objects as an API server that holds a newer version
// than the cache does.
type laggingReader struct{ client.Reader }

func (r laggingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	obj.SetResourceVersion(obj.GetResourceVersion() + "0")
	return nil
}

var _ = Describe("Order renders that fail", func() {
	const ns = testNamespace
	ctx := context.Background()

	// reconcilers returns an OrderReconciler whose renders fail, since its
	// pipeline cannot write, and one whose renders succeed.
	reconcilers := func() (failing, working *OrderReconciler) {
		fake := oci.NewFakeClient(afero.NewMemMapFs())
		failing = newTestOrderReconciler(fake)
		failing.Pipeline = artifact.NewPipeline(artifact.NewStore(fake, afero.NewReadOnlyFs(afero.NewMemMapFs()), ""))

		fs := afero.NewMemMapFs()
		pulls := oci.NewFakeClient(fs)
		working = newTestOrderReconciler(pulls)
		working.Pipeline = artifact.NewPipeline(artifact.NewStore(pulls, fs, ""))
		return failing, working
	}

	// create creates the Order name of the test Product, and deletes it
	// with its Preparations afterwards.
	create := func(name string) types.NamespacedName {
		createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{Name: name, Namespace: ns, Spec: deliveryv1alpha1.OrderSpec{
			EnvironmentRef: localEnvironment,
			ProductRef:     deliveryv1alpha1.ProductRef{Name: testProductName},
			Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
		}})).To(Succeed())
		DeferCleanup(func() {
			order := &deliveryv1alpha1.Order{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, order); err == nil {
				order.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, order)
				_ = k8sClient.Delete(ctx, order)
			}
			Expect(k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{}, client.InNamespace(ns),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: name})).To(Succeed())
		})
		return types.NamespacedName{Namespace: ns, Name: name}
	}

	get := func(key types.NamespacedName) *deliveryv1alpha1.Order {
		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, key, order)).To(Succeed())
		return order
	}
	readyReason := func(key types.NamespacedName) string {
		ready := meta.FindStatusCondition(get(key).Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(ready).NotTo(BeNil())
		return ready.Reason
	}

	It("records the render, retries it without reporting Processing again, and clears the record once it renders", func() {
		key := create("order-render-fails")
		failing, working := reconcilers()

		_, err := failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred(), "the first pass records the render")
		Expect(get(key).Status.Render).NotTo(BeNil())
		hash := get(key).Status.Render.ConfigHash
		Expect(readyReason(key)).To(Equal(deliveryv1alpha1.ReasonProcessing))

		for range 2 {
			_, err = failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).To(HaveOccurred(), "a failure is returned, so that it is retried with back-off")
			Expect(stderrors.Is(err, reconcile.TerminalError(nil))).To(BeFalse())
			Expect(readyReason(key)).To(Equal(deliveryv1alpha1.ReasonProcessingFailed), "a retry keeps the failure")
			Expect(get(key).Status.Render).To(Equal(&deliveryv1alpha1.OrderRender{ConfigHash: hash}))
			Expect(get(key).Status.LatestConfigHash).To(BeEmpty())
		}

		_, err = working.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(get(key).Status.LatestConfigHash).To(Equal(hash))
		Expect(get(key).Status.Render).To(BeNil())
		Expect(readyReason(key)).To(Equal(deliveryv1alpha1.ReasonReady))
	})

	It("starts over for a new intent, and drops a failed render once its intent is rendered again", func() {
		key := create("order-render-new-intent")
		failing, working := reconcilers()
		Expect(settleOrder(ctx, working, key)).To(Succeed())
		rendered := get(key).Status.LatestConfigHash

		By("failing to render another target namespace")
		order := get(key)
		order.Spec.TargetNamespace = "render-retargeted"
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		_, err := failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		failed := get(key).Status.Render.ConfigHash
		Expect(failed).NotTo(Equal(rendered))
		_, err = failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).To(HaveOccurred())

		By("changing the intent once more")
		order = get(key)
		order.Spec.TargetNamespace = "render-retargeted-again"
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		_, err = failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred(), "a new intent goes through the first pass again")
		Expect(get(key).Status.Render.ConfigHash).NotTo(Equal(failed))
		Expect(readyReason(key)).To(Equal(deliveryv1alpha1.ReasonProcessing))

		By("going back to the rendered intent")
		order = get(key)
		order.Spec.TargetNamespace = ""
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		_, err = failing.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(get(key).Status.Render).To(BeNil(), "the latest Preparation renders this intent")
		Expect(readyReason(key)).To(Equal(deliveryv1alpha1.ReasonReady))
	})

	It("postpones a render while the cache lags behind the Order", func() {
		key := create("order-render-lagging")
		_, working := reconcilers()
		_, err := working.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		working.APIReader = laggingReader{Reader: k8sClient}
		result, err := working.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(orderCacheRetryInterval), "no event follows a status write")
		Expect(get(key).Status.LatestPreparationName).To(BeEmpty())
	})
})

var _ = Describe("renderStarted", func() {
	order := func(render string) *deliveryv1alpha1.Order {
		o := &deliveryv1alpha1.Order{Name: "order-watched", Namespace: testNamespace}
		if render != "" {
			o.Status.Render = &deliveryv1alpha1.OrderRender{ConfigHash: render}
		}
		return o
	}

	DescribeTable("passes the updates that record a new render",
		func(old, updated string, passes bool) {
			Expect(renderStarted.Update(event.UpdateEvent{ObjectOld: order(old), ObjectNew: order(updated)})).To(Equal(passes))
		},
		Entry("a render starts", "", "sha256:a", true),
		Entry("a render of another intent starts", "sha256:a", "sha256:b", true),
		Entry("the render goes on, or fails", "sha256:a", "sha256:a", false),
		Entry("the render completes", "sha256:a", "", false),
		Entry("no render", "", "", false),
	)
})

var _ = Describe("productChanged", func() {
	product := func() *deliveryv1alpha1.Product {
		p := &deliveryv1alpha1.Product{Name: "product-watched", Namespace: testNamespace, Generation: 2}
		p.Status.ConfigHash = "sha256:product"
		p.Status.Source = &deliveryv1alpha1.ProductSourceStatus{URL: testOCIRef, Version: testVersion, Digest: oci.FakeDigest}
		p.Status.Conditions = []metav1.Condition{{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason, ObservedGeneration: 2,
		}}
		return p
	}

	DescribeTable("passes the updates that change what its Orders read",
		func(change func(*deliveryv1alpha1.Product), passes bool) {
			updated := product()
			change(updated)
			Expect(productChanged.Update(event.UpdateEvent{ObjectOld: product(), ObjectNew: updated})).To(Equal(passes))
		},
		Entry("its spec", func(p *deliveryv1alpha1.Product) { p.Generation++ }, true),
		Entry("the start of its deletion", func(p *deliveryv1alpha1.Product) { p.DeletionTimestamp = new(metav1.Now()) }, true),
		Entry("its source", func(p *deliveryv1alpha1.Product) { p.Status.Source.Digest = "sha256:moved" }, true),
		Entry("its config hash", func(p *deliveryv1alpha1.Product) { p.Status.ConfigHash = "sha256:changed" }, true),
		Entry("its readiness", func(p *deliveryv1alpha1.Product) { p.Status.Conditions[0].Status = metav1.ConditionFalse }, true),
		Entry("the generation Ready describes", func(p *deliveryv1alpha1.Product) { p.Status.Conditions[0].ObservedGeneration = 1 }, true),
		Entry("the message of Ready", func(p *deliveryv1alpha1.Product) { p.Status.Conditions[0].Message = "verified" }, true),
		Entry("its scan results", func(p *deliveryv1alpha1.Product) {
			p.Status.Vulnerabilities = &deliveryv1alpha1.ProductVulnerabilities{}
		}, false),
		Entry("another condition", func(p *deliveryv1alpha1.Product) {
			p.Status.Conditions = append(p.Status.Conditions, metav1.Condition{Type: "Scanned", Status: metav1.ConditionTrue})
		}, false),
	)
})
