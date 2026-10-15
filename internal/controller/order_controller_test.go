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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/index"
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
			createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion})

			By("creating the custom resource for the Kind Order")
			err := k8sClient.Get(ctx, typeNamespacedName, order)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Order{
					Name:      resourceName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.OrderSpec{
						ProductRef: deliveryv1alpha1.ProductRef{Name: testProductName},
						Promotion: &deliveryv1alpha1.PromotionSpec{
							Mode: deliveryv1alpha1.PromotionModeManual,
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
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Pipeline:       artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, "")),
				PantryResolver: credential.NewKubeResolver(k8sClient),
				BuilderID:      testBuilderID,
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

	Context("When the Order or its Product uses a Pantry", func() {
		const ns = testNamespace

		ctx := context.Background()

		newReconciler := func() *OrderReconciler {
			fs := afero.NewMemMapFs()
			return &OrderReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Pipeline:       artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, "")),
				PantryResolver: credential.NewKubeResolver(k8sClient),
				BuilderID:      testBuilderID,
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

		// createOrder creates an Order of its own Product, named after the
		// Order, whose source resolves to source.
		createOrder := func(name string, source deliveryv1alpha1.OCISource, dest *deliveryv1alpha1.OCIDestination) {
			createTestProduct(ctx, name, source)
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: name, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					ProductRef: deliveryv1alpha1.ProductRef{Name: name},
					Promotion: &deliveryv1alpha1.PromotionSpec{
						Mode: deliveryv1alpha1.PromotionModeManual,
					},
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
				deleteTestProduct(ctx, orderName)
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
			createOrder(orderName, deliveryv1alpha1.OCISource{
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
			createOrder(orderName, deliveryv1alpha1.OCISource{
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
			createOrder(orderName, deliveryv1alpha1.OCISource{
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
			createOrder(orderName, deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.kokumi.svc.cluster.local:5000/order/app",
				Version: testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName},
			})

			Expect(reconcileOrder(orderName)).To(Succeed())
			firstHash := getOrder(orderName).Status.LatestConfigHash
			Expect(listPreparations(orderName).Items).To(HaveLen(1))
			Expect(listPreparations(orderName).Items[0].Spec.Artifact.OCIRef).
				To(HavePrefix(fmt.Sprintf("%s:%s@", firstURL, testVersion)))

			pantry := &deliveryv1alpha1.Pantry{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pantryName, Namespace: ns}, pantry)).To(Succeed())
			pantry.Spec.URL = secondURL
			Expect(k8sClient.Update(ctx, pantry)).To(Succeed())

			Expect(reconcileOrder(orderName)).To(Succeed())
			Expect(getOrder(orderName).Status.LatestConfigHash).NotTo(Equal(firstHash))

			preps := listPreparations(orderName)
			Expect(preps.Items).To(HaveLen(2))
			refs := []string{preps.Items[0].Spec.Artifact.OCIRef, preps.Items[1].Spec.Artifact.OCIRef}
			Expect(refs).To(ContainElement(HavePrefix(fmt.Sprintf("%s:%s@", firstURL, testVersion))))
			Expect(refs).To(ContainElement(HavePrefix(fmt.Sprintf("%s:%s@", secondURL, testVersion))))
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
			createOrder(orderName, deliveryv1alpha1.OCISource{
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

		It("fails when the destination Pantry is missing", func() {
			const orderName = "order-missing-pantry"
			defer cleanup(orderName, "")

			createOrder(orderName, deliveryv1alpha1.OCISource{
				OCI:     "oci://registry.kokumi.svc.cluster.local:5000/order/missing",
				Version: testVersion,
			}, &deliveryv1alpha1.OCIDestination{
				PantryRef: &deliveryv1alpha1.PantryRef{Name: "does-not-exist"},
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
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Pipeline:       artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, "")),
				PantryResolver: credential.NewKubeResolver(k8sClient),
				BuilderID:      testBuilderID,
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
				OCI:     "oci://registry.kokumi.svc.cluster.local:5000/order/edit-revert",
				Version: "0.1.0",
			})
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					ProductRef: deliveryv1alpha1.ProductRef{Name: orderName},
					Promotion: &deliveryv1alpha1.PromotionSpec{
						Mode: deliveryv1alpha1.PromotionModeManual,
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
					client.InNamespace(testNamespace), client.MatchingFields{index.FieldOrderName: orderName})
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, order); err == nil {
					order.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, order)
					_ = k8sClient.Delete(ctx, order)
				}
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Serving{Name: orderName, Namespace: testNamespace})
			})

			fs := afero.NewMemMapFs()
			r := &OrderReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Pipeline:       artifact.NewPipeline(artifact.NewStore(oci.NewFakeClient(fs), fs, "")),
				PantryResolver: credential.NewKubeResolver(k8sClient),
				BuilderID:      testBuilderID,
			}
			_, err := r.Reconcile(ctx, reconcile.Request{Namespace: testNamespace, Name: orderName})
			Expect(err).NotTo(HaveOccurred())

			preps := &deliveryv1alpha1.PreparationList{}
			Expect(k8sClient.List(ctx, preps, client.InNamespace(testNamespace),
				client.MatchingFields{index.FieldOrderName: orderName})).To(Succeed())
			Expect(preps.Items).To(HaveLen(1))
			Expect(preps.Items[0].Spec.ApprovalPolicy).To(Equal(policy))
			Expect(preps.Items[0].Labels).NotTo(HaveKey("delivery.kokumi.dev/auto-deploy"))

			serving := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, serving)).To(Succeed())
			Expect(serving.Spec.OrderName).To(Equal(orderName))
			Expect(serving.Spec.PreparationName).To(BeEmpty())
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
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Pipeline:       artifact.NewPipeline(artifact.NewStore(fake, fs, "")),
				PantryResolver: credential.NewKubeResolver(k8sClient),
				BuilderID:      testBuilderID,
			}
		})

		reconcileOrder := func(name string) error {
			_, err := r.Reconcile(ctx, reconcile.Request{Namespace: ns, Name: name})
			return err
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
			product := createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion})

			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: orderName, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					ProductRef: deliveryv1alpha1.ProductRef{Name: productName},
					Promotion:  &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
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
					ProductRef: deliveryv1alpha1.ProductRef{Name: productName},
					Promotion:  &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
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
			_, err := r.Reconcile(ctx, reconcile.Request{Namespace: ns, Name: orderName})
			Expect(err).NotTo(HaveOccurred())
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
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}},
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
				Spec:       deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}},
			})).To(Succeed())
			resolveTestProduct(ctx, productName, testOCIRef, testVersion)
			Expect(k8sClient.Delete(ctx, getTestProduct(ctx, productName))).To(Succeed())

			reconcileOrder()
			Expect(readyOf().Status).To(Equal(metav1.ConditionFalse))
			Expect(readyOf().Reason).To(Equal(deliveryv1alpha1.ReasonProductDeleting))
			Expect(preparations()).To(BeEmpty())
		})

		It("does not render again when the Product comes back unchanged", func() {
			createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion})
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
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}},
			}, deliveryv1alpha1.OrderSpec{
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
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}},
			}, deliveryv1alpha1.OrderSpec{
				Values: &apiextensionsv1.JSON{Raw: []byte(`{"replicaCount":3}`)},
			})

			expectRejected("order-values-no-helm", "only allowed when the Product renders with Helm")
		})
	})

	Context("When the Product of an Order is changed", func() {
		It("rejects the change", func() {
			ctx := context.Background()
			order := &deliveryv1alpha1.Order{
				Name: "order-immutable-product", Namespace: testNamespace,
				Spec: deliveryv1alpha1.OrderSpec{ProductRef: deliveryv1alpha1.ProductRef{Name: "one"}},
			}
			Expect(k8sClient.Create(ctx, order)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })

			order.Spec.ProductRef.Name = "other"
			err := k8sClient.Update(ctx, order)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("productRef is immutable"))
		})
	})

	Context("When the values of an Order are not an object", func() {
		It("rejects the Order", func() {
			err := k8sClient.Create(context.Background(), &deliveryv1alpha1.Order{
				Name: "order-values-string", Namespace: testNamespace,
				Spec: deliveryv1alpha1.OrderSpec{
					ProductRef: deliveryv1alpha1.ProductRef{Name: "one"},
					Values:     &apiextensionsv1.JSON{Raw: []byte(`"replicaCount=3"`)},
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

// newTestOrderReconciler returns an OrderReconciler pushing to fake.
func newTestOrderReconciler(fake *oci.FakeClient) *OrderReconciler {
	return &OrderReconciler{
		Client:         k8sClient,
		Scheme:         k8sClient.Scheme(),
		Pipeline:       artifact.NewPipeline(artifact.NewStore(fake, afero.NewMemMapFs(), "")),
		PantryResolver: credential.NewKubeResolver(k8sClient),
		BuilderID:      testBuilderID,
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
	if err := k8sClient.Create(ctx, product); err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	resolvedURL := source.OCI
	if source.PantryRef != nil {
		pantry := &deliveryv1alpha1.Pantry{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: source.PantryRef.Name}, pantry)).To(Succeed())
		resolvedURL = pantry.Spec.URL
	}

	return resolveTestProduct(ctx, name, resolvedURL, source.Version)
}

// resolveTestProduct publishes the source at sourceOCI and sourceVersion,
// resolved to oci.FakeDigest, in the Product's status, as the Product
// controller does once it has resolved the current generation.
func resolveTestProduct(ctx context.Context, name, sourceOCI, sourceVersion string) *deliveryv1alpha1.Product {
	product := getTestProduct(ctx, name)

	product.Status.ConfigHash = "sha256:product-" + sourceVersion
	product.Status.Source = &deliveryv1alpha1.ProductSourceStatus{
		OCI:       sourceOCI,
		Version:   sourceVersion,
		PantryRef: product.Spec.Source.PantryRef,
		Digest:    oci.FakeDigest,
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
	createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion})

	order := &deliveryv1alpha1.Order{
		Name:      name,
		Namespace: testNamespace,
		Spec: deliveryv1alpha1.OrderSpec{
			ProductRef: deliveryv1alpha1.ProductRef{Name: testProductName},
			Promotion:  &deliveryv1alpha1.PromotionSpec{Mode: mode},
		},
	}
	if len(policy) > 0 {
		order.Spec.Promotion.Approvals = policy[0]
	}
	if err := k8sClient.Create(ctx, order); err != nil && !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}
