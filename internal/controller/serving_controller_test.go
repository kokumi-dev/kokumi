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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/promotion"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

var argoAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// argoNamespace is the namespace the Serving controller places Applications in.
const argoNamespace = argocd.Namespace

// appKey returns the key of the Argo CD Application of the Order name in
// testNamespace on the cluster Kokumi runs in.
func appKey(order string) client.ObjectKey {
	return argocd.Key(&deliveryv1alpha1.Serving{Namespace: testNamespace, Spec: deliveryv1alpha1.ServingSpec{OrderName: order}}, cluster.InCluster)
}

// comparedCurrentSource records on app that Argo CD compared its current
// source, as its Application controller does before it reports a sync
// status.
func comparedCurrentSource(app *unstructured.Unstructured) {
	source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	Expect(unstructured.SetNestedMap(app.Object, source, statusField, "sync", "comparedTo", "source")).To(Succeed())
}

var _ = Describe("Serving Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "serving"
		const orderName = "order"
		const preparationName = "preparation-fdf90e00e76"
		const fakeDigest = "sha256:fdf90e00e76bf3f0d2e5042c4c4e6c42a6d38c1e2b4f5a7d8e9f0a1b2c3d4e5f"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testNamespace,
		}
		serving := &deliveryv1alpha1.Serving{}
		var registry *oci.FakeClient

		BeforeEach(func() {
			registry = oci.NewFakeClient(nil)

			By("creating the Preparation referenced by the Serving")
			preparation := &deliveryv1alpha1.Preparation{}
			preparationKey := types.NamespacedName{Name: preparationName, Namespace: testNamespace}
			err := k8sClient.Get(ctx, preparationKey, preparation)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Preparation{
					Name:      preparationName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.PreparationSpec{
						TargetNamespace: testNamespace,
						OrderName:       orderName,
						Source: deliveryv1alpha1.OrderSource{
							URL:        testOCIRef,
							BaseDigest: fakeDigest,
						},
						Renderer: deliveryv1alpha1.Renderer{
							Type:    deliveryv1alpha1.RenderTypeManifest,
							Version: "v1.0.0",
							Builder: testBuilderID,
						},
						ConfigHash:      "sha256:abc123",
						ProductRevision: testProductRevision,
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@" + fakeDigest,
							Digest: fakeDigest,
						},
					},
				})).To(Succeed())
			}
			markPreparationReady(ctx, registry, preparationKey)

			By("ensuring the argocd namespace exists")

			By("creating the custom resource for the Kind Serving")
			err = k8sClient.Get(ctx, typeNamespacedName, serving)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Serving{
					Name:      resourceName,
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.ServingSpec{
						OrderName:       orderName,
						PreparationName: preparationName,
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}

			By("creating the Manual Order the Serving belongs to")
			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual)
		})

		AfterEach(func() {
			By("Cleanup the Order")
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: orderName, Namespace: testNamespace})

			By("Cleanup the Serving")
			resource := &deliveryv1alpha1.Serving{}
			if err := k8sClient.Get(ctx, typeNamespacedName, resource); err == nil {
				resource.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, resource)
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}

			By("Cleanup the Preparation")
			preparation := &deliveryv1alpha1.Preparation{}
			preparationKey := types.NamespacedName{Name: preparationName, Namespace: testNamespace}
			if err := k8sClient.Get(ctx, preparationKey, preparation); err == nil {
				Expect(k8sClient.Delete(ctx, preparation)).To(Succeed())
			}

			By("Cleanup any Argo CD Application created during the test")
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			app.SetNamespace(argoNamespace)
			app.SetName(appKey(orderName).Name)
			_ = k8sClient.Delete(ctx, app)
		})

		newReconciler := func() *ServingReconciler {
			return newServingReconciler(registry)
		}

		// setAppStatus simulates the Argo CD Application controller by
		// patching the Application's status subresource.
		setAppStatus := func(syncStatus, revision, healthStatus, healthMessage string) {
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(k8sClient.Get(ctx, appKey(orderName), app)).To(Succeed())

			updated := app.DeepCopy()
			status := map[string]any{}
			if syncStatus != "" || revision != "" {
				status["sync"] = map[string]any{
					"status":    syncStatus,
					keyRevision: revision,
				}
			}
			if healthStatus != "" || healthMessage != "" {
				health := map[string]any{
					"status": healthStatus,
				}
				if healthMessage != "" {
					health["message"] = healthMessage
				}
				status["health"] = health
			}
			updated.Object["status"] = status
			if syncStatus != "" {
				comparedCurrentSource(updated)
			}
			Expect(k8sClient.Status().Update(ctx, updated)).To(Succeed())
		}

		getApp := func() *unstructured.Unstructured {
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(k8sClient.Get(ctx, appKey(orderName), app)).To(Succeed())
			return app
		}

		getServing := func() *deliveryv1alpha1.Serving {
			s := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, s)).To(Succeed())
			return s
		}

		// buildArgoAppMap returns a map for an unstructured Argo CD
		// Application named like the Serving, as if created out-of-band.
		buildArgoAppMap := func(repoURL, targetRevision string, annotations map[string]any) map[string]any {
			meta := map[string]any{
				"name":       appKey(orderName).Name,
				keyNamespace: argoNamespace,
			}
			if len(annotations) > 0 {
				meta["annotations"] = annotations
			}
			return map[string]any{
				"apiVersion": "argoproj.io/v1alpha1",
				"kind":       "Application",
				"metadata":   meta,
				"spec": map[string]any{
					"project": "default",
					"source": map[string]any{
						"repoURL":        repoURL,
						"targetRevision": targetRevision,
						"path":           ".",
					},
					"destination": map[string]any{
						"server":     "https://kubernetes.default.svc",
						keyNamespace: testNamespace,
					},
				},
			}
		}

		It("creates an Argo CD Application with the allowed-order annotation set to the Order name", func() {
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, testNamespace+"/"+orderName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelOrder, orderName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelServing, resourceName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelServingNamespace, testNamespace))

			By("staying in Deploying until the Application reports healthy at the desired revision")
			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal("Deploying"))
			Expect(s.DeployedPreparationName()).To(Equal(preparationName),
				"the promoted preparation must already be the deployed (active) preparation while deploying")
			Expect(healthyDigest(s)).To(BeEmpty())
			Expect(s.Status.History[0].PromotionRecord).NotTo(BeEmpty(), "the promotion is recorded before it deploys")

			By("simulating Argo CD syncing the Application to the desired revision")
			setAppStatus("Synced", fakeDigest, "Healthy", "")

			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			s = getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(healthyDigest(s)).To(Equal(fakeDigest))
			Expect(s.DeployedPreparationName()).To(Equal(preparationName))
		})

		DescribeTable("reports a promoted Preparation as Deploying while the Application is still at an older revision",
			func(oldHealth string) {
				const v2Digest = "sha256:aaaaaaaaaabbbbbbbbbbccccccccccddddddddddeeeeeeeeeeffffffffffffff"

				By("deploying and syncing to the desired revision")
				_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				setAppStatus("Synced", fakeDigest, "Healthy", "")
				_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				Expect(healthyDigest(getServing())).To(Equal(fakeDigest))
				setAppStatus("Synced", fakeDigest, oldHealth, "old revision is "+oldHealth)

				By("pointing the Serving at a new preparation digest")
				s := getServing()
				s.Spec.PreparationName = preparationName + "-v2"
				Expect(k8sClient.Update(ctx, s)).To(Succeed())

				preparationV2 := &deliveryv1alpha1.Preparation{
					Name:      preparationName + "-v2",
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.PreparationSpec{
						TargetNamespace: testNamespace,
						OrderName:       orderName,
						Source: deliveryv1alpha1.OrderSource{
							URL:        testOCIRef,
							BaseDigest: fakeDigest,
						},
						Renderer: deliveryv1alpha1.Renderer{
							Type:    deliveryv1alpha1.RenderTypeManifest,
							Version: "v1.0.0",
							Builder: testBuilderID,
						},
						ConfigHash:      "sha256:abc123",
						ProductRevision: testProductRevision,
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@" + v2Digest,
							Digest: v2Digest,
						},
					},
				}
				Expect(k8sClient.Create(ctx, preparationV2)).To(Succeed())
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, preparationV2)
				})
				markPreparationReady(ctx, registry, client.ObjectKeyFromObject(preparationV2))

				By("reconciling while the Application is still at the old revision")
				_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())

				s = getServing()
				cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal("Deploying"),
					"the health of the old revision must not be reported for the promoted Preparation")
				Expect(s.DeployedPreparationName()).To(Equal(preparationName + "-v2"))
			},
			Entry("healthy", "Healthy"),
			Entry("degraded", "Degraded"),
		)

		It("does not deploy a promoted Preparation until it is ready", func() {
			const v2Digest = "sha256:bbbbbbbbbbccccccccccddddddddddeeeeeeeeeeffffffffffaaaaaaaaaaaaaa"

			targetRevision := func() string {
				revision, _, _ := unstructured.NestedString(getApp().Object, "spec", "source", "targetRevision")
				return revision
			}

			By("deploying the ready Preparation")
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(targetRevision()).To(Equal(fakeDigest))

			By("promoting a Preparation that is not ready")
			notReady := createTestPreparation(ctx, preparationName+"-pending", orderName, v2Digest, nil)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, notReady)
			})
			s := getServing()
			s.Spec.PreparationName = notReady.Name
			Expect(k8sClient.Update(ctx, s)).To(Succeed())

			result, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "the Preparation watch triggers the next reconcile")

			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonDeploying), "Ready describes what runs")
			Expect(cond.Message).To(ContainSubstring("Waiting for Preparation %q to become ready", notReady.Name))
			Expect(targetRevision()).To(Equal(fakeDigest), "the deployed Preparation must stay in place")
			Expect(getServing().DeployedPreparationName()).To(Equal(preparationName))

			By("deploying it once it is ready")
			markPreparationReady(ctx, registry, client.ObjectKeyFromObject(notReady))
			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(targetRevision()).To(Equal(v2Digest))
		})

		DescribeTable("does not deploy a Preparation whose evidence does not verify",
			func(sign func(prep *deliveryv1alpha1.Preparation), reason string) {
				const v2Digest = "sha256:cccccccccceeeeeeeeeeddddddddddbbbbbbbbbbffffffffffaaaaaaaaaaaaaa"

				targetRevision := func() string {
					revision, _, _ := unstructured.NestedString(getApp().Object, "spec", "source", "targetRevision")
					return revision
				}

				By("deploying the verified Preparation")
				_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				Expect(targetRevision()).To(Equal(fakeDigest))
				Expect(apimeta.IsStatusConditionTrue(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)).To(BeTrue())

				By("promoting a ready Preparation whose evidence does not verify")
				forged := createTestPreparation(ctx, preparationName+"-forged", orderName, v2Digest, nil)
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, forged)
				})
				sign(forged)
				apimeta.SetStatusCondition(&forged.Status.Conditions, metav1.Condition{
					Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason,
				})
				Expect(k8sClient.Status().Update(ctx, forged)).To(Succeed())
				s := getServing()
				s.Spec.PreparationName = forged.Name
				Expect(k8sClient.Update(ctx, s)).To(Succeed())

				_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())

				cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(reason))
				Expect(cond.Message).To(ContainSubstring(forged.Name))
				Expect(targetRevision()).To(Equal(fakeDigest), "the deployed Preparation must stay in place")
			},
			Entry("unsigned", func(*deliveryv1alpha1.Preparation) {}, deliveryv1alpha1.ReasonSignatureMissing),
			Entry("signed by an untrusted key", func(prep *deliveryv1alpha1.Preparation) {
				signTestEvidence(registry, signingtest.NewSigner(GinkgoT()), prep, testNamespace+"/"+orderName)
			}, deliveryv1alpha1.ReasonSignatureInvalid),
			Entry("signed for another Order", func(prep *deliveryv1alpha1.Preparation) {
				signTestEvidence(registry, testSigner, prep, testNamespace+"/another-order")
			}, deliveryv1alpha1.ReasonSignatureInvalid),
		)

		It("waits for the signing key before deploying", func() {
			r := newReconciler()
			r.Signing = signing.NewProvider(SigningInputs(k8sClient, testNamespace, "no-signing-key"))

			_, err := settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonSigningKeyNotReady))
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, appKey(orderName), app))).To(BeTrue())
		})

		It("holds when its deployed Preparation was deleted, keeps what runs, and keeps Ready's message as it is", func() {
			r := newReconciler()
			_, err := settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(getServing().DeployedPreparationName()).To(Equal(preparationName))

			By("pointing the Serving at a Preparation that does not exist yet, and deleting the deployed one")
			s := getServing()
			s.Spec.PreparationName = "next-preparation"
			Expect(k8sClient.Update(ctx, s)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: preparationName, Namespace: testNamespace})).To(Succeed())

			messages := make([]string, 0, 3)
			for range 3 {
				_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())
				cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonPreparationNotFound))
				messages = append(messages, cond.Message)
			}
			Expect(messages[0]).To(ContainSubstring(`Waiting for Preparation "next-preparation"`))
			Expect(messages).To(HaveEach(messages[0]), "each pass adds what holds the Serving once")
			Expect(getApp()).NotTo(BeNil(), "the Application keeps the deployed artifact running")
		})

		It("waits for a Manual Preparation that does not exist yet, and deploys it once it is created", func() {
			preparation := &deliveryv1alpha1.Preparation{}
			preparationKey := types.NamespacedName{Name: preparationName, Namespace: testNamespace}
			Expect(k8sClient.Get(ctx, preparationKey, preparation)).To(Succeed())
			Expect(k8sClient.Delete(ctx, preparation)).To(Succeed())

			r := newReconciler()
			_, err := settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonPending))
			Expect(cond.Message).To(Equal(fmt.Sprintf("Waiting for Preparation %q", preparationName)))

			By("creating the Preparation")
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Preparation{
				Name: preparationName, Namespace: testNamespace, Labels: preparation.Labels, Spec: preparation.Spec,
			})).To(Succeed())
			markPreparationReady(ctx, registry, preparationKey)

			_, err = settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(getServing().DeployedPreparationName()).To(Equal(preparationName))
		})

		It("holds a Preparation whose Registry is missing, and verifies it once a Registry event brings it back", func() {
			const registryName = "late-registry"
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, order)).To(Succeed())
			order.Spec.RegistryRefs = []corev1.LocalObjectReference{{Name: registryName}}
			Expect(k8sClient.Update(ctx, order)).To(Succeed())
			// The Order may outlive this test, held by a finalizer another
			// test left, so later tests must not find its Registry.
			DeferCleanup(func() {
				order := &deliveryv1alpha1.Order{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: orderName}, order); err == nil {
					order.Spec.RegistryRefs = nil
					Expect(k8sClient.Update(ctx, order)).To(Succeed())
				}
			})

			r := newReconciler()
			_, err := settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonRegistryNotFound))

			By("creating the Registry, whose event enqueues the Servings of its namespace")
			reg := &deliveryv1alpha1.Registry{
				Name:      registryName,
				Namespace: testNamespace,
				Spec:      deliveryv1alpha1.RegistrySpec{URL: "oci://registry.kokumi.svc.cluster.local:5000/preparation"},
			}
			Expect(k8sClient.Create(ctx, reg)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, reg) })
			Expect(r.enqueueServingsOfRegistry(ctx, reg)).To(ContainElement(ctrl.Request{NamespacedName: typeNamespacedName}))
			Expect(r.enqueueServingsOfRegistry(ctx, &deliveryv1alpha1.Registry{Name: registryName, Namespace: "elsewhere"})).
				NotTo(ContainElement(ctrl.Request{NamespacedName: typeNamespacedName}), "only the Servings of the Registry's namespace")

			_, err = settleServing(ctx, r, typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			cond = apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue), cond.Message)
		})

		It("marks the Serving as failed when the Application reports Degraded", func() {
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			By("simulating a degraded Application")
			setAppStatus("Synced", fakeDigest, "Degraded", "some resources are unhealthy")

			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("DeploymentFailed"))
			Expect(cond.Message).To(ContainSubstring("some resources are unhealthy"))
		})

		It("surfaces degradation of an already deployed Serving", func() {
			By("deploying and syncing to the desired revision")
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			setAppStatus("Synced", fakeDigest, "Healthy", "")
			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(apimeta.IsStatusConditionTrue(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())

			By("degrading the Application without changing the Serving spec")
			setAppStatus("Synced", fakeDigest, "Degraded", "pod crashed")

			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("DeploymentFailed"))
			Expect(cond.Message).To(ContainSubstring("pod crashed"))
		})

		DescribeTable("does not rewrite the status of a Serving that stays degraded",
			func(healthyFirst bool) {
				_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				if healthyFirst {
					setAppStatus("Synced", fakeDigest, "Healthy", "")
					_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
					Expect(err).NotTo(HaveOccurred())
				}
				setAppStatus("Synced", fakeDigest, "Degraded", "hpa unhealthy")
				_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())

				before := getServing()
				cond := apimeta.FindStatusCondition(before.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal("DeploymentFailed"))
				Expect(before.DeployedPreparationName()).To(Equal(preparationName))

				By("reconciling again without any change")
				_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				Expect(getServing().ResourceVersion).To(Equal(before.ResourceVersion),
					"an unchanged degraded Serving must not be written, every write re-triggers the reconcile")
			},
			Entry("while rolling out", false),
			Entry("after it was deployed", true),
		)

		It("refuses to update a pre-existing Argo CD Application that is missing the opt-in annotation", func() {
			By("creating an Argo CD Application out-of-band without the opt-in annotation")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap("oci://example.com/foreign", "sha256:foreign", nil),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			result, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred(), "opt-in denial must not return an error (would cause requeue/flapping)")
			Expect(result.RequeueAfter).To(BeZero())

			By("verifying the pre-existing Application was NOT modified")
			app := getApp()
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			source, _ := spec["source"].(map[string]any)
			Expect(source["repoURL"]).To(Equal("oci://example.com/foreign"))
			Expect(source["targetRevision"]).To(Equal("sha256:foreign"))
			Expect(app.GetAnnotations()).NotTo(HaveKey(deliveryv1alpha1.AnnotationAllowedOrder))

			By("verifying the Serving status surfaces the opt-in failure")
			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("DeploymentFailed"))
			Expect(cond.Message).To(ContainSubstring("not opted in"))

			By("verifying the status never flapped through Deploying")
			// The reconciler must not transition through Deploying when the
			// opt-in check fails. The condition should sit at DeploymentFailed.
			Expect(cond.Reason).NotTo(Equal("Deploying"))

			By("re-reconciling and ensuring the status remains DeploymentFailed (no flapping)")
			lastTransition := cond.LastTransitionTime
			for range 3 {
				_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
				Expect(err).NotTo(HaveOccurred())
				s := getServing()
				cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				Expect(cond.Reason).To(Equal("DeploymentFailed"))
				Expect(cond.LastTransitionTime).To(Equal(lastTransition),
					"LastTransitionTime must not change on repeated reconciles (no flapping)")
			}
		})

		It("refuses to update an Application whose allowed-order annotation references a different Order", func() {
			By("creating an Argo CD Application annotated for a different Order")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap("oci://example.com/foreign", "sha256:foreign", map[string]any{
					deliveryv1alpha1.AnnotationAllowedOrder: "some-other-order",
				}),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred(), "opt-in denial must not return an error (would cause requeue/flapping)")
			app := getApp()
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, "some-other-order"))

			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("DeploymentFailed"))
			Expect(cond.Message).To(ContainSubstring("not opted in"))
			Expect(cond.Message).To(ContainSubstring("some-other-order"))
		})

		It("updates an Application whose allowed-order annotation matches the Order name", func() {
			By("creating an Argo CD Application annotated with the matching opt-in")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap("oci://example.com/stale", "sha256:stale", map[string]any{
					deliveryv1alpha1.AnnotationAllowedOrder: testNamespace + "/" + orderName,
				}),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			source, _ := spec["source"].(map[string]any)
			Expect(source["targetRevision"]).To(Equal(fakeDigest))
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, testNamespace+"/"+orderName))

			By("completing the rollout")
			setAppStatus("Synced", fakeDigest, "Healthy", "")
			_, err = settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			s := getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
		})

		// deleteServing deletes the Serving and reconciles it, as the
		// controller does once the deletion timestamp is set.
		deleteServing := func() {
			Expect(k8sClient.Delete(ctx, getServing())).To(Succeed())
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			Expect(errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &deliveryv1alpha1.Serving{}))).To(BeTrue(),
				"the finalizer must be removed")
		}

		It("deletes its Application with the Serving", func() {
			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())
			getApp()

			deleteServing()

			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, appKey(orderName), app))).To(BeTrue())
		})

		It("keeps an Application that is not opted in when the Serving is deleted", func() {
			By("creating an Argo CD Application out-of-band without the opt-in annotation")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap("oci://example.com/foreign", "sha256:foreign", nil),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := settleServing(ctx, newReconciler(), typeNamespacedName)
			Expect(err).NotTo(HaveOccurred())

			deleteServing()

			app := getApp()
			Expect(app.GetUID()).To(Equal(preExisting.GetUID()))
			Expect(app.GetDeletionTimestamp()).To(BeNil())
			targetRevision, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
			Expect(targetRevision).To(Equal("sha256:foreign"))
		})
	})

	Context("When the target Preparation has an approval policy", func() {
		const (
			orderName   = "serving-gated"
			prepOlder   = "serving-gated-p1"
			prepNewer   = "serving-gated-p2"
			digestOlder = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
			digestNewer = "sha256:9999999999999999999999999999999999999999999999999999999999999999"
		)
		release := []string{testApproverGroup}
		policy := &deliveryv1alpha1.ApprovalPolicy{RequiredApprovals: 1, AllowedGroups: release}
		key := types.NamespacedName{Namespace: testNamespace, Name: orderName}
		var registry *oci.FakeClient

		reconcileServing := func() {
			_, err := settleServing(ctx, newServingReconciler(registry), key)
			Expect(err).NotTo(HaveOccurred())
		}
		getServing := func() *deliveryv1alpha1.Serving {
			s := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, key, s)).To(Succeed())
			return s
		}
		approvedReason := func() string {
			if c := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeApproved); c != nil {
				return c.Reason
			}
			return ""
		}
		appExists := func() bool {
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			return k8sClient.Get(ctx, appKey(orderName), app) == nil
		}
		readyPreparation := func(name, digest string) *deliveryv1alpha1.Preparation {
			p := createTestPreparation(ctx, name, orderName, digest, policy)
			markPreparationReady(ctx, registry, client.ObjectKeyFromObject(p))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p), p)).To(Succeed())
			return p
		}
		// sealPreparation stands in for the Preparation controller sealing and archiving the votes.
		sealPreparation := func(p *deliveryv1alpha1.Preparation) {
			approvals := &deliveryv1alpha1.ApprovalList{}
			Expect(k8sClient.List(ctx, approvals, client.InNamespace(p.Namespace),
				client.MatchingFields{deliveryv1alpha1.FieldPreparationRefName: p.Name})).To(Succeed())
			res := approval.Evaluate(p, approvals.Items)
			Expect(res.Approved).To(BeTrue())

			sealedTime := approval.NewSealTime(time.Now())
			p.Status.Approval = res.Status
			p.Status.Approval.SealedTime = &sealedTime
			archived, err := approval.Archive(ctx, registry, testSigner, p, approvals.Items)
			Expect(err).NotTo(HaveOccurred())
			p.Status.Approval.Attestation = &archived
			apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
				Type: deliveryv1alpha1.ConditionTypeApprovalsSealed, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonSealed,
			})
			Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		}

		BeforeEach(func() {
			registry = oci.NewFakeClient(nil)

			createTestOrder(ctx, orderName, deliveryv1alpha1.PromotionModeManual, policy)
			DeferCleanup(func() {
				deleteApprovalsOfOrder(ctx, orderName)
				s := &deliveryv1alpha1.Serving{}
				if err := k8sClient.Get(ctx, key, s); err == nil {
					s.SetFinalizers(nil)
					_ = k8sClient.Update(ctx, s)
					_ = k8sClient.Delete(ctx, s)
				}
				for _, name := range []string{prepOlder, prepNewer} {
					_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: name, Namespace: testNamespace})
				}
				_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: orderName, Namespace: testNamespace})
				app := &unstructured.Unstructured{}
				app.SetGroupVersionKind(argoAppGVK)
				app.SetNamespace(argoNamespace)
				app.SetName(appKey(orderName).Name)
				_ = k8sClient.Delete(ctx, app)
			})
		})

		It("keeps a Manual promotion blocked until the Preparation is approved and sealed", func() {
			p := readyPreparation(prepOlder, digestOlder)
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
				Name:      orderName,
				Namespace: testNamespace,
				Spec:      deliveryv1alpha1.ServingSpec{OrderName: orderName, PreparationName: prepOlder},
			})).To(Succeed())

			reconcileServing()
			Expect(getServing().Status.TargetPreparationName).To(Equal(prepOlder))
			Expect(approvedReason()).To(Equal(deliveryv1alpha1.ReasonAwaitingApprovals))
			Expect(appExists()).To(BeFalse(), "an unapproved Preparation must never be deployed")

			createTestApproval(ctx, p, "alice", release, deliveryv1alpha1.ApprovalDecisionReject)
			reconcileServing()
			Expect(approvedReason()).To(Equal(deliveryv1alpha1.ReasonChangesRequested))
			Expect(appExists()).To(BeFalse())

			createTestApproval(ctx, p, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			reconcileServing()
			Expect(approvedReason()).To(Equal(deliveryv1alpha1.ReasonSealingApprovals))
			Expect(appExists()).To(BeFalse(), "approvals must be sealed before deployment")

			sealPreparation(p)
			reconcileServing()
			Expect(approvedReason()).To(Equal(deliveryv1alpha1.ReasonApproved))
			Expect(appExists()).To(BeTrue())
			Expect(getServing().Spec.PreparationName).To(Equal(prepOlder), "the controller never writes the Serving spec")
		})

		It("names only its current target, so that a vote for a former target is not sealed", func() {
			older := readyPreparation(prepOlder, digestOlder)
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
				Name:      orderName,
				Namespace: testNamespace,
				Spec:      deliveryv1alpha1.ServingSpec{OrderName: orderName, PreparationName: prepOlder},
			})).To(Succeed())
			reconcileServing()
			Expect(getServing().Status.TargetPreparationName).To(Equal(prepOlder))

			By("promoting a Preparation that is not Ready yet")
			createTestPreparation(ctx, prepNewer, orderName, digestNewer, policy)
			s := getServing()
			s.Spec.PreparationName = prepNewer
			Expect(k8sClient.Update(ctx, s)).To(Succeed())
			reconcileServing()
			Expect(getServing().Status.TargetPreparationName).To(BeEmpty())

			createTestApproval(ctx, older, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			target, err := (&PreparationReconciler{Client: k8sClient}).isPromotionTarget(ctx, older)
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(BeFalse(), "the Preparation controller seals only the target's votes")

			By("the new Preparation turning Ready")
			markPreparationReady(ctx, registry, types.NamespacedName{Namespace: testNamespace, Name: prepNewer})
			reconcileServing()
			Expect(getServing().Status.TargetPreparationName).To(Equal(prepNewer))
		})

		It("keeps Automatic promotion waiting on the newest Preparation", func() {
			order := &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, order)).To(Succeed())
			order.Spec.Promotion.Mode = deliveryv1alpha1.PromotionModeAutomatic
			Expect(k8sClient.Update(ctx, order)).To(Succeed())

			older := readyPreparation(prepOlder, digestOlder)
			createTestApproval(ctx, older, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			sealPreparation(older)

			// CreationTimestamp has second precision.
			time.Sleep(1100 * time.Millisecond)
			newer := readyPreparation(prepNewer, digestNewer)

			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
				Name:      orderName,
				Namespace: testNamespace,
				Spec:      deliveryv1alpha1.ServingSpec{OrderName: orderName},
			})).To(Succeed())
			reconcileServing()

			s := getServing()
			Expect(s.Spec.PreparationName).To(BeEmpty(), "the controller never writes the Serving spec")
			Expect(s.Status.TargetPreparationName).To(Equal(prepNewer))
			Expect(approvedReason()).To(Equal(deliveryv1alpha1.ReasonAwaitingApprovals))
			Expect(appExists()).To(BeFalse(), "a superseded Preparation must not be deployed instead")

			createTestApproval(ctx, newer, "alice", release, deliveryv1alpha1.ApprovalDecisionApprove)
			sealPreparation(newer)
			reconcileServing()
			Expect(appExists()).To(BeTrue())
		})
	})
})

var _ = Describe("Serving vulnerability checks", func() {
	const (
		orderName       = "checked"
		productName     = "checked-product"
		preparationName = "checked-0123456789ab"
		artifactDigest  = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
		imageDigest     = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
		vendoredRepo    = "kokumi-registry.kokumi.svc.cluster.local:5000/default/vendor/checked-product/images/docker.io/library/nginx"
		cleanReport     = `{"invocation":{"parameters":null},"scanner":{"uri":"pkg:github/aquasecurity/trivy@0.71.0","version":"0.71.0","db":{"uri":"","version":""},"result":{"Results":[]}},"metadata":{}}`
	)
	key := types.NamespacedName{Namespace: testNamespace, Name: orderName}

	var (
		registry   *oci.FakeClient
		recorder   *events.FakeRecorder
		now        time.Time
		vulnerable string
		clean      string
	)

	subject := func() oci.Reference {
		ref, err := oci.Parse("oci://" + vendoredRepo + "@" + imageDigest)
		Expect(err).NotTo(HaveOccurred())
		return ref
	}
	// attachReport attaches report to the vendored image, signed by signer,
	// and returns the referrer's digest.
	attachReport := func(signer *signing.Signer, report string) string {
		statement, err := attestation.NewStatement(vendoredRepo, imageDigest, vulnerability.PredicateType, json.RawMessage(report))
		Expect(err).NotTo(HaveOccurred())
		digest, err := attestation.Push(ctx, registry, signer, subject(), vulnerability.PredicateType, statement, now)
		Expect(err).NotTo(HaveOccurred())
		return digest
	}
	// recordScan records on the Product a scan of the image with report,
	// scanned at scanned.
	recordScan := func(report string, scanned time.Time, summary deliveryv1alpha1.VulnerabilitySummary) {
		product := &deliveryv1alpha1.Product{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: productName}, product)).To(Succeed())
		product.Status.Vulnerabilities = &deliveryv1alpha1.ProductVulnerabilities{
			Scanner: deliveryv1alpha1.VulnerabilityScanner{Name: "trivy", Version: "0.71.0"},
			Images: []deliveryv1alpha1.ImageScan{{
				Digest: imageDigest, Repository: "oci://" + vendoredRepo, ScannedTime: metav1.Time{Time: scanned},
				SBOM:   deliveryv1alpha1.ImageSBOM{Digest: imageDigest, Origin: deliveryv1alpha1.SBOMOriginImage},
				Report: report, Summary: summary,
			}},
		}
		Expect(k8sClient.Status().Update(ctx, product)).To(Succeed())
	}
	reconcileServing := func() ctrl.Result {
		r := newServingReconciler(registry)
		r.Recorder = recorder
		r.InstallNamespace = "kokumi-none"
		r.now = func() time.Time { return now }
		result, err := settleServing(ctx, r, key)
		Expect(err).NotTo(HaveOccurred())
		return result
	}
	getServing := func() *deliveryv1alpha1.Serving {
		s := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, key, s)).To(Succeed())
		return s
	}
	checksPassed := func() *metav1.Condition {
		return apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeChecksPassed)
	}
	appExists := func() bool {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(argoAppGVK)
		return k8sClient.Get(ctx, appKey(orderName), app) == nil
	}
	setAppHealthy := func() {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(argoAppGVK)
		Expect(k8sClient.Get(ctx, appKey(orderName), app)).To(Succeed())
		Expect(unstructured.SetNestedField(app.Object, "Synced", "status", "sync", "status")).To(Succeed())
		Expect(unstructured.SetNestedField(app.Object, artifactDigest, "status", "sync", "revision")).To(Succeed())
		Expect(unstructured.SetNestedField(app.Object, "Healthy", "status", "health", "status")).To(Succeed())
		comparedCurrentSource(app)
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
	}

	BeforeEach(func() {
		registry = oci.NewFakeClient(nil)
		recorder = events.NewFakeRecorder(10)
		now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

		_ = k8sClient.Create(ctx, &corev1.Namespace{Name: argoNamespace})

		report, err := os.ReadFile(filepath.Join("..", "vulnerability", "testdata", "report.json"))
		Expect(err).NotTo(HaveOccurred())
		vulnerable = attachReport(testSigner, string(report))
		clean = attachReport(testSigner, cleanReport)

		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Product{
			Name:      productName,
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.ProductSpec{
				Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
				Vendor: &deliveryv1alpha1.OCIDestination{},
				Images: deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyVendor},
			},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
			Name:      orderName,
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.OrderSpec{
				EnvironmentRef: localEnvironment,
				ProductRef:     deliveryv1alpha1.ProductRef{Name: productName},
				Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
			},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Preparation{
			Name:      preparationName,
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.PreparationSpec{
				TargetNamespace: testNamespace,
				OrderName:       orderName,
				Source:          deliveryv1alpha1.OrderSource{URL: testOCIRef, BaseDigest: oci.FakeDigest},
				Renderer:        deliveryv1alpha1.Renderer{Type: deliveryv1alpha1.RenderTypeManifest, Version: testRendererVersion, Builder: testBuilderID},
				ConfigHash:      testConfigHash,
				ProductRevision: testProductRevision,
				Artifact: deliveryv1alpha1.Artifact{
					OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/" + orderName + "@" + artifactDigest,
					Digest: artifactDigest,
				},
				Promotion: &deliveryv1alpha1.PromotionGates{Checks: &deliveryv1alpha1.PromotionChecks{
					Vulnerabilities: &deliveryv1alpha1.VulnerabilityCheck{MaxSeverity: deliveryv1alpha1.SeverityHigh},
				}},
				Images: []deliveryv1alpha1.PreparationImage{{
					Ref: "nginx:1.27", Name: "docker.io/library/nginx", Digest: imageDigest,
					Vendored: vendoredRepo + ":1.27@" + imageDigest,
				}},
			},
		})).To(Succeed())
		markPreparationReady(ctx, registry, types.NamespacedName{Namespace: testNamespace, Name: preparationName})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
			Name:      orderName,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.ServingSpec{OrderName: orderName, PreparationName: preparationName},
		})).To(Succeed())

		DeferCleanup(func() {
			s := &deliveryv1alpha1.Serving{}
			if err := k8sClient.Get(ctx, key, s); err == nil {
				s.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: preparationName, Namespace: testNamespace})
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: orderName, Namespace: testNamespace})
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Product{Name: productName, Namespace: testNamespace})
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			app.SetNamespace(argoNamespace)
			app.SetName(appKey(orderName).Name)
			_ = k8sClient.Delete(ctx, app)
		})
	})

	It("does not deploy a Preparation with vulnerabilities at or above the allowed severity", func() {
		recordScan(vulnerable, now.Add(-time.Hour), deliveryv1alpha1.VulnerabilitySummary{Critical: 1, High: 2})

		reconcileServing()
		cond := checksPassed()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonVulnerabilitiesFound))
		Expect(cond.Message).To(ContainSubstring("3 vulnerabilities at or above High"))
		Expect(cond.Message).To(ContainSubstring("CVE-2026-19931 (Critical, curl)"))
		Expect(appExists()).To(BeFalse())
	})

	It("deploys a Preparation that passes, and checks again before its scan gets too old", func() {
		recordScan(clean, now.Add(-time.Hour), deliveryv1alpha1.VulnerabilitySummary{})

		result := reconcileServing()
		cond := checksPassed()
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonPassed))
		Expect(appExists()).To(BeTrue())
		Expect(result.RequeueAfter).To(Equal(47*time.Hour), "twice the default interval, less the hour since the scan")
	})

	It("does not deploy a Preparation whose images were not scanned, or long ago", func() {
		reconcileServing()
		Expect(checksPassed().Reason).To(Equal(deliveryv1alpha1.ReasonScanMissing))
		Expect(checksPassed().Message).To(ContainSubstring("docker.io/library/nginx has not been scanned"))
		Expect(appExists()).To(BeFalse())

		recordScan(clean, now.Add(-72*time.Hour), deliveryv1alpha1.VulnerabilitySummary{})
		reconcileServing()
		Expect(checksPassed().Reason).To(Equal(deliveryv1alpha1.ReasonScanStale))
		Expect(appExists()).To(BeFalse())
	})

	It("does not trust a report no trusted key signed", func() {
		forged := attachReport(signingtest.NewSigner(GinkgoT()), cleanReport+" ")
		recordScan(forged, now.Add(-time.Hour), deliveryv1alpha1.VulnerabilitySummary{})

		reconcileServing()
		Expect(checksPassed().Reason).To(Equal(deliveryv1alpha1.ReasonScanMissing))
		Expect(checksPassed().Message).To(ContainSubstring("did not verify"))
		Expect(appExists()).To(BeFalse())
	})

	It("keeps a deployed Preparation deployed when a new scan finds vulnerabilities, and warns", func() {
		recordScan(clean, now.Add(-time.Hour), deliveryv1alpha1.VulnerabilitySummary{Low: 1})
		reconcileServing()
		setAppHealthy()
		reconcileServing()
		s := getServing()
		Expect(healthyDigest(s)).To(Equal(artifactDigest))
		Expect(s.Status.DeployedVulnerabilities).To(Equal(&deliveryv1alpha1.VulnerabilitySummary{Low: 1}))

		recordScan(vulnerable, now, deliveryv1alpha1.VulnerabilitySummary{Critical: 1, High: 2, Fixable: 2})
		reconcileServing()

		s = getServing()
		Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue(), "the checks hold new deployments only")
		Expect(checksPassed().Reason).To(Equal(deliveryv1alpha1.ReasonVulnerabilitiesFound))
		Expect(s.Status.DeployedVulnerabilities).To(Equal(&deliveryv1alpha1.VulnerabilitySummary{Critical: 1, High: 2, Fixable: 2}))
		Expect(recorder.Events).To(Receive(ContainSubstring("VulnerabilitiesIncreased")))
	})
})

// newServingReconciler returns a ServingReconciler that verifies evidence in
// registry against testSigner.
func newServingReconciler(registry *oci.FakeClient) *ServingReconciler {
	return &ServingReconciler{
		Client:              k8sClient,
		Scheme:              k8sClient.Scheme(),
		Registries:          fakeRegistries(registry),
		Signing:             testSigning,
		BundledRegistryHost: artifact.DefaultRegistryHost,
	}
}

// markPreparationReady signs the evidence of the Preparation in registry and
// sets the Ready condition, as the Preparation controller does.
func markPreparationReady(ctx context.Context, registry *oci.FakeClient, key types.NamespacedName) {
	p := &deliveryv1alpha1.Preparation{}
	Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
	signTestEvidence(registry, testSigner, p, p.Namespace+"/"+p.Spec.OrderName)
	apimeta.SetStatusCondition(&p.Status.Conditions, metav1.Condition{
		Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason,
	})
	Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
}

// signTestEvidence pushes the signature and the provenance of prep's
// artifact, rendered for order ("namespace/name"), signed by signer.
func signTestEvidence(registry *oci.FakeClient, signer *signing.Signer, prep *deliveryv1alpha1.Preparation, order string) {
	subject, err := oci.Parse(prep.Spec.Artifact.OCIRef)
	Expect(err).NotTo(HaveOccurred())

	namespace, name, _ := strings.Cut(order, "/")
	cfg := &artifact.Config{
		SchemaVersion: artifact.ConfigSchemaVersion,
		Order:         &artifact.ConfigObject{Namespace: namespace, Name: name},
		CreatedTime:   "2026-10-12T09:14:03Z",
		Source:        artifact.ConfigSource{OCI: prep.Spec.Source.URL, Version: testVersion, Digest: prep.Spec.Source.BaseDigest},
		Renderer:      artifact.ConfigRenderer{Type: "Manifest", Version: testRendererVersion, Builder: testBuilderID},
	}
	created, err := cfg.Created()
	Expect(err).NotTo(HaveOccurred())

	_, err = attestation.Sign(context.Background(), registry, signer, subject, created)
	Expect(err).NotTo(HaveOccurred())
	_, err = provenance.Attach(context.Background(), registry, signer, subject, cfg)
	Expect(err).NotTo(HaveOccurred())
}

// statusField is the status of an Argo CD Application, and of its sync and
// health.
const statusField = "status"

// settleServing reconciles the Serving of key with r until it no longer
// asks for the next pass of a record, as the controller does by requeueing,
// and returns the last result.
func settleServing(ctx context.Context, r *ServingReconciler, key types.NamespacedName) (ctrl.Result, error) {
	for range 8 {
		result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		if err != nil || result.RequeueAfter != historyPassInterval {
			return result, err
		}
	}
	return ctrl.Result{}, fmt.Errorf("Serving %s did not settle", key)
}

// healthyDigest returns the digest of the Preparation s deploys once it was
// healthy, "" before.
func healthyDigest(s *deliveryv1alpha1.Serving) string {
	for _, e := range s.Status.History {
		if e.PromotionRecord != "" {
			if e.HealthyTime == nil {
				return ""
			}
			return e.Digest
		}
	}
	return ""
}

var _ = Describe("Serving promotion pipelines", func() {
	const (
		dev       = "pipeline-dev"
		staging   = "pipeline-staging"
		prod      = "pipeline-prod"
		revision1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
		revision2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
		soak      = time.Hour
	)

	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

	var (
		registry *oci.FakeClient
		r        *ServingReconciler
		now      time.Time
	)

	// createStage creates an Order of product in mode that runs after
	// upstreams, with its Serving.
	createStage := func(name, product string, mode deliveryv1alpha1.PromotionMode, upstreams ...deliveryv1alpha1.UpstreamStage) {
		createTestProduct(ctx, product, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
			Name: name, Namespace: testNamespace,
			Spec: deliveryv1alpha1.OrderSpec{
				EnvironmentRef: localEnvironment,
				ProductRef:     deliveryv1alpha1.ProductRef{Name: product},
				Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: mode, After: upstreams},
			},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
			Name: name, Namespace: testNamespace, Spec: deliveryv1alpha1.ServingSpec{OrderName: name},
		})).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{},
				client.InNamespace(testNamespace), client.MatchingFields{deliveryv1alpha1.FieldOrderName: name})
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: name, Namespace: testNamespace})
			serving := &deliveryv1alpha1.Serving{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, serving); err == nil {
				serving.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, serving)
				_ = k8sClient.Delete(ctx, serving)
			}
			app := argocd.New()
			app.SetNamespace(argoNamespace)
			app.SetName(appKey(name).Name)
			_ = k8sClient.Delete(ctx, app)
		})
	}

	// prepare creates the ready Preparation name of order, rendered from
	// revision under the upstream stages of the Order.
	prepare := func(name, order, revision string) *deliveryv1alpha1.Preparation {
		o := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: order}, o)).To(Succeed())
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(name)))
		prep := createTestPreparation(ctx, name, order, digest, nil)
		prep.Spec.ProductRevision = revision
		prep.Spec.Promotion = o.PromotionGates()
		Expect(k8sClient.Delete(ctx, prep)).To(Succeed())
		prep.ResourceVersion = ""
		Expect(k8sClient.Create(ctx, prep)).To(Succeed())
		markPreparationReady(ctx, registry, client.ObjectKeyFromObject(prep))
		return prep
	}

	reconcileStage := func(name string) ctrl.Result {
		result, err := settleServing(ctx, r, types.NamespacedName{Namespace: testNamespace, Name: name})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	servingOf := func(name string) *deliveryv1alpha1.Serving {
		s := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, s)).To(Succeed())
		return s
	}

	upstreamDeployed := func(name string) *metav1.Condition {
		return apimeta.FindStatusCondition(servingOf(name).Status.Conditions, deliveryv1alpha1.ConditionTypeUpstreamDeployed)
	}

	appRevision := func(name string) string {
		app := argocd.New()
		if err := k8sClient.Get(ctx, appKey(name), app); err != nil {
			return ""
		}
		revision, _, _ := unstructured.NestedString(app.Object, "spec", "source", "targetRevision")
		return revision
	}

	// report sets what Argo CD reports on the Application of stage name.
	report := func(name, health string) {
		app := argocd.New()
		Expect(k8sClient.Get(ctx, appKey(name), app)).To(Succeed())
		app.Object[statusField] = map[string]any{
			"sync":   map[string]any{statusField: "Synced", keyRevision: appRevision(name)},
			"health": map[string]any{statusField: health},
		}
		comparedCurrentSource(app)
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
	}

	// deployHealthy reconciles stage name until it deploys, reports it
	// healthy, and reconciles it until its deployment is recorded.
	deployHealthy := func(name string) {
		reconcileStage(name)
		Expect(appRevision(name)).NotTo(BeEmpty(), "stage %s deploys", name)
		report(name, "Healthy")
		reconcileStage(name)
		Expect(servingOf(name).Status.History[0].DeploymentRecord).NotTo(BeEmpty())
	}

	// predicate reads the verified record of predicateType with digest of
	// the deployment e into v.
	predicate := func(e deliveryv1alpha1.DeploymentEntry, digest, predicateType string, v any) {
		subject, err := oci.Parse(e.OCIRef)
		Expect(err).NotTo(HaveOccurred())
		found, err := attestation.VerifyReferrer(ctx, registry, testSigner.TrustSet(), subject, digest, predicateType)
		Expect(err).NotTo(HaveOccurred())
		Expect(found.Predicate(v)).To(Succeed())
	}

	BeforeEach(func() {
		registry = oci.NewFakeClient(nil)
		now = t0
		r = newServingReconciler(registry)
		r.now = func() time.Time { return now }

		createStage(dev, testProductName, deliveryv1alpha1.PromotionModeAutomatic)
		createStage(staging, testProductName, deliveryv1alpha1.PromotionModeAutomatic, deliveryv1alpha1.UpstreamStage{Order: dev})
		createStage(prod, testProductName, deliveryv1alpha1.PromotionModeAutomatic,
			deliveryv1alpha1.UpstreamStage{Order: staging, SoakSeconds: int32(soak / time.Second)})
	})

	It("deploys each stage once its upstream stage ran the revision healthily for its soak", func() {
		prepare("dev-1", dev, revision1)
		prepare("staging-1", staging, revision1)
		prepare("prod-1", prod, revision1)

		By("holding staging until dev deployed the revision")
		reconcileStage(staging)
		Expect(upstreamDeployed(staging).Reason).To(Equal(deliveryv1alpha1.ReasonNotDeployedUpstream))
		Expect(appRevision(staging)).To(BeEmpty())
		ready := apimeta.FindStatusCondition(servingOf(staging).Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonPending))
		Expect(ready.Message).To(ContainSubstring("Waiting for upstream stages"))

		By("recording dev's promotion and its healthy deployment")
		deployHealthy(dev)
		devEntry := servingOf(dev).Status.History[0]
		Expect(devEntry.PreparationName).To(Equal("dev-1"))
		Expect(devEntry.HealthyTime.Time).To(BeTemporally("==", t0))
		Expect(apimeta.FindStatusCondition(servingOf(dev).Status.Conditions, deliveryv1alpha1.ConditionTypeUpstreamDeployed)).
			To(BeNil(), "a stage without upstream stages has no such gate")
		var deployment promotion.DeploymentPredicate
		predicate(devEntry, devEntry.DeploymentRecord, promotion.DeploymentPredicateType, &deployment)
		Expect(deployment.Order).To(Equal(testNamespace + "/" + dev))
		Expect(deployment.Promotion).To(Equal(devEntry.PromotionRecord))
		Expect(deployment.ProductRevision).To(Equal(revision1))

		By("deploying staging, which names dev's deployment in its promotion record")
		deployHealthy(staging)
		Expect(upstreamDeployed(staging).Reason).To(Equal(deliveryv1alpha1.ReasonSatisfied))
		stagingEntry := servingOf(staging).Status.History[0]
		var promoted promotion.PromotionPredicate
		predicate(stagingEntry, stagingEntry.PromotionRecord, promotion.PromotionPredicateType, &promoted)
		Expect(promoted.Gates.After).To(HaveLen(1))
		Expect(promoted.Gates.After[0].Deployment.Record).To(Equal(devEntry.DeploymentRecord))
		Expect(promoted.Mode).To(Equal(string(deliveryv1alpha1.PromotionModeAutomatic)))

		By("soaking prod, which keeps its target and its votes open")
		now = t0.Add(10 * time.Minute)
		result := reconcileStage(prod)
		Expect(upstreamDeployed(prod).Reason).To(Equal(deliveryv1alpha1.ReasonSoaking))
		Expect(result.RequeueAfter).To(Equal(50*time.Minute), "the soak wakes the Serving")
		Expect(servingOf(prod).Status.TargetPreparationName).To(BeEmpty())
		Expect(appRevision(prod)).To(BeEmpty())

		By("deploying prod once staging soaked")
		now = t0.Add(soak)
		reconcileStage(prod)
		Expect(upstreamDeployed(prod).Reason).To(Equal(deliveryv1alpha1.ReasonSatisfied))
		Expect(servingOf(prod).Status.TargetPreparationName).To(Equal("prod-1"))
		Expect(appRevision(prod)).NotTo(BeEmpty())

		By("mapping a stage's Serving to the Servings of the stages after it")
		Expect(historyChanged.Update(event.UpdateEvent{ObjectOld: &deliveryv1alpha1.Serving{}, ObjectNew: servingOf(staging)})).To(BeTrue())
		Expect(historyChanged.Update(event.UpdateEvent{ObjectOld: servingOf(staging), ObjectNew: servingOf(staging)})).To(BeFalse())
	})

	It("soaks again after an upstream stage degraded and recovered", func() {
		prepare("staging-1", staging, revision1)
		prepare("prod-1", prod, revision1)
		prepare("dev-1", dev, revision1)
		deployHealthy(dev)
		deployHealthy(staging)

		By("ending the deployment of staging when it degrades")
		now = t0.Add(30 * time.Minute)
		report(staging, "Degraded")
		reconcileStage(staging)
		Expect(servingOf(staging).Status.History[0].EndReason).To(Equal(deliveryv1alpha1.DeploymentEndReasonDegraded))
		reconcileStage(prod)
		Expect(upstreamDeployed(prod).Reason).To(Equal(deliveryv1alpha1.ReasonNotDeployedUpstream))

		By("soaking from the recovery")
		now = t0.Add(40 * time.Minute)
		report(staging, "Healthy")
		reconcileStage(staging)
		history := servingOf(staging).Status.History
		Expect(history).To(HaveLen(2))
		Expect(history[0].HealthyTime.Time).To(BeTemporally("==", now))
		Expect(history[0].PromotionRecord).To(Equal(history[1].PromotionRecord), "the recovery continues the promotion")

		now = t0.Add(time.Hour + 30*time.Minute)
		result := reconcileStage(prod)
		Expect(upstreamDeployed(prod).Reason).To(Equal(deliveryv1alpha1.ReasonSoaking))
		Expect(result.RequeueAfter).To(Equal(10 * time.Minute))
	})

	It("lets a stage roll back to a Preparation it promoted before, after upstream moved on", func() {
		o := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: prod}, o)).To(Succeed())
		o.Spec.Promotion.Mode = deliveryv1alpha1.PromotionModeManual
		o.Spec.Promotion.After[0].SoakSeconds = 0
		Expect(k8sClient.Update(ctx, o)).To(Succeed())
		promote := func(name string) {
			s := servingOf(prod)
			s.Spec.PreparationName = name
			Expect(k8sClient.Update(ctx, s)).To(Succeed())
			reconcileStage(prod)
		}

		prepare("dev-1", dev, revision1)
		prepare("staging-1", staging, revision1)
		prepare("prod-1", prod, revision1)
		deployHealthy(dev)
		deployHealthy(staging)
		promote("prod-1")
		Expect(servingOf(prod).DeployedPreparationName()).To(Equal("prod-1"))

		By("moving dev and staging on to the next revision, and staging forgetting the first")
		now = t0.Add(time.Hour)
		prepare("dev-2", dev, revision2)
		prepare("staging-2", staging, revision2)
		prepare("prod-2", prod, revision2)
		deployHealthy(dev)
		s := servingOf(staging)
		s.Status.History = nil
		Expect(k8sClient.Status().Update(ctx, s)).To(Succeed())
		deployHealthy(staging)
		Expect(servingOf(staging).Status.History).To(HaveLen(1), "staging forgot revision1")
		promote("prod-2")
		Expect(servingOf(prod).DeployedPreparationName()).To(Equal("prod-2"))

		By("rolling prod back without staging running revision1")
		promote("prod-1")
		Expect(upstreamDeployed(prod).Reason).To(Equal(deliveryv1alpha1.ReasonSatisfied))
		Expect(upstreamDeployed(prod).Message).To(ContainSubstring("promoted here at"))
		Expect(servingOf(prod).DeployedPreparationName()).To(Equal("prod-1"))

		history := servingOf(prod).Status.History
		Expect(history[1].EndReason).To(Equal(deliveryv1alpha1.DeploymentEndReasonSuperseded))
		var rollback promotion.PromotionPredicate
		predicate(history[0], history[0].PromotionRecord, promotion.PromotionPredicateType, &rollback)
		Expect(rollback.Previous.Name).To(Equal("prod-2"))
		Expect(rollback.Gates.After[0].Promotion).To(Equal(history[2].PromotionRecord), "it names the earlier promotion")
	})

	It("does not trust an upstream deployment record that does not verify", func() {
		prepare("dev-1", dev, revision1)
		prepare("staging-1", staging, revision1)
		deployHealthy(dev)

		s := servingOf(dev)
		s.Status.History[0].DeploymentRecord = "sha256:" + strings.Repeat("f", 64)
		Expect(k8sClient.Status().Update(ctx, s)).To(Succeed())

		_, err := r.Reconcile(ctx, reconcile.Request{Namespace: testNamespace, Name: staging})
		Expect(err).To(HaveOccurred(), "a record the registry does not hold is retried")
		Expect(upstreamDeployed(staging).Reason).To(Equal(deliveryv1alpha1.ReasonSignatureMissing))
		Expect(appRevision(staging)).To(BeEmpty())
	})

	It("does not deploy a stage that runs after an Order of another Product", func() {
		const other = "pipeline-other-product"
		createStage("pipeline-foreign", other, deliveryv1alpha1.PromotionModeAutomatic)
		DeferCleanup(func() { deleteTestProduct(ctx, other) })
		createStage("pipeline-mixed", testProductName, deliveryv1alpha1.PromotionModeAutomatic,
			deliveryv1alpha1.UpstreamStage{Order: "pipeline-foreign"})
		prepare("mixed-1", "pipeline-mixed", revision1)

		reconcileStage("pipeline-mixed")
		Expect(upstreamDeployed("pipeline-mixed").Reason).To(Equal(deliveryv1alpha1.ReasonInvalidPipeline))
		Expect(appRevision("pipeline-mixed")).To(BeEmpty())
	})
})

var _ = Describe("Serving environments", Ordered, func() {
	const (
		eu      = "srv-eu"
		us      = "srv-us"
		envName = "srv-edge"
		order   = "srv-edge-order"
		prep    = "srv-edge-order-p1"
		fleet   = "srv-fleet"
		project = "srv-edge"
		digest  = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
	)
	servers := map[string]string{eu: "https://eu.example:6443", us: "https://us.example:6443"}
	ctx := context.Background()
	key := types.NamespacedName{Namespace: testNamespace, Name: order}
	envKey := types.NamespacedName{Namespace: testNamespace, Name: envName}
	var (
		registry *oci.FakeClient
		r        *ServingReconciler
	)

	reconcileServing := func() {
		_, err := settleServing(ctx, r, key)
		Expect(err).NotTo(HaveOccurred())
	}
	getServing := func() *deliveryv1alpha1.Serving {
		s := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, key, s)).To(Succeed())
		return s
	}
	ready := func(conds []metav1.Condition) *metav1.Condition {
		return apimeta.FindStatusCondition(conds, deliveryv1alpha1.ConditionTypeReady)
	}
	application := func(clusterName string) (*unstructured.Unstructured, error) {
		app := argocd.New()
		err := k8sClient.Get(ctx, argocd.Key(getServing(), clusterName), app)
		return app, err
	}
	// reportHealthy simulates Argo CD reporting the Application of
	// clusterName synced to the Preparation and healthy.
	reportHealthy := func(clusterName string) {
		app, err := application(clusterName)
		Expect(err).NotTo(HaveOccurred())
		app.Object[statusField] = map[string]any{
			"sync":   map[string]any{statusField: "Synced", keyRevision: digest},
			"health": map[string]any{statusField: "Healthy"},
		}
		comparedCurrentSource(app)
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())
	}
	// setEnvironmentReady records Ready on the Environment, as its
	// controller would before a change it has not seen yet.
	setEnvironmentReady := func() {
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, envKey, env)).To(Succeed())
		conditions.MarkTrue(env, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "Deploys to its clusters")
		Expect(k8sClient.Status().Update(ctx, env)).To(Succeed())
	}
	// setClusterStatus records the status the Cluster controller reports.
	setClusterStatus := func(name, reason string) {
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, c)).To(Succeed())
		c.Status.Server = servers[name]
		if reason == deliveryv1alpha1.ReasonReady {
			conditions.MarkTrue(c, deliveryv1alpha1.ConditionTypeReady, reason, "The control plane is ready")
		} else {
			conditions.MarkFalse(c, deliveryv1alpha1.ConditionTypeReady, reason, "The control plane cannot be reached")
		}
		Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())
	}
	// labelCluster sets the label the Environment selects its Clusters by.
	labelCluster := func(name, value string) {
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, c)).To(Succeed())
		c.Labels[fleet] = value
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
	}
	targetReasons := func() []string {
		targets := getServing().Status.Targets
		reasons := make([]string, 0, len(targets))
		for _, t := range targets {
			reasons = append(reasons, ready(t.Conditions).Reason)
		}
		return reasons
	}
	targetClusters := func() []string {
		names := make([]string, 0, len(getServing().Status.Targets))
		for _, t := range getServing().Status.Targets {
			names = append(names, t.Cluster)
		}
		return names
	}
	// admitNamespaces sets the namespaces the AppProject admits Applications
	// of.
	admitNamespaces := func(namespaces ...string) {
		p := newAppProject()
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: argoNamespace, Name: project}, p)).To(Succeed())
		Expect(unstructured.SetNestedStringSlice(p.Object, namespaces, "spec", "sourceNamespaces")).To(Succeed())
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
	}

	BeforeAll(func() {
		registry = oci.NewFakeClient(nil)
		r = newServingReconciler(registry)

		for _, name := range []string{eu, us} {
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Cluster{
				Name: name, Namespace: testNamespace, Labels: map[string]string{fleet: testEdge},
				Spec: deliveryv1alpha1.ClusterSpec{
					Connection: deliveryv1alpha1.ClusterConnectionKubeconfig,
					Kubeconfig: &deliveryv1alpha1.ClusterKubeconfig{SecretRef: deliveryv1alpha1.KubeconfigSecretReference{Name: name + "-kubeconfig"}},
				},
			})).To(Succeed())
			setClusterStatus(name, deliveryv1alpha1.ReasonReady)
		}
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Environment{
			Name: envName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.EnvironmentSpec{
				ClusterSelector: metav1.LabelSelector{MatchLabels: map[string]string{fleet: testEdge}},
				Deployer:        deliveryv1alpha1.Deployer{Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: project}},
			},
		})).To(Succeed())
		appProject := newAppProject()
		appProject.SetNamespace(argoNamespace)
		appProject.SetName(project)
		Expect(unstructured.SetNestedSlice(appProject.Object, []any{map[string]any{keyServer: "*", keyNamespace: "*"}}, "spec", "destinations")).
			To(Succeed())
		Expect(unstructured.SetNestedStringSlice(appProject.Object, []string{testNamespace}, "spec", "sourceNamespaces")).To(Succeed())
		Expect(k8sClient.Create(ctx, appProject)).To(Succeed())

		createTestProduct(ctx, testProductName, deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion})
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
			Name:      order,
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.OrderSpec{
				EnvironmentRef: deliveryv1alpha1.EnvironmentRef{Name: envName},
				ProductRef:     deliveryv1alpha1.ProductRef{Name: testProductName},
				Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
			},
		})).To(Succeed())
		p := createTestPreparation(ctx, prep, order, digest, nil)
		markPreparationReady(ctx, registry, client.ObjectKeyFromObject(p))
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
			Name:      order,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.ServingSpec{OrderName: order, PreparationName: prep},
		})).To(Succeed())
	})

	AfterAll(func() {
		s := &deliveryv1alpha1.Serving{}
		if k8sClient.Get(ctx, key, s) == nil {
			s.SetFinalizers(nil)
			_ = k8sClient.Update(ctx, s)
			_ = k8sClient.Delete(ctx, s)
		}
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: prep, Namespace: testNamespace})
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: order, Namespace: testNamespace})
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Environment{Name: envName, Namespace: testNamespace})
		for _, name := range []string{eu, us} {
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Cluster{Name: name, Namespace: testNamespace})
		}
		appProject := newAppProject()
		appProject.SetNamespace(argoNamespace)
		appProject.SetName(project)
		_ = k8sClient.Delete(ctx, appProject)
		_ = k8sClient.DeleteAllOf(ctx, argocd.New(), client.InNamespace(argoNamespace),
			client.MatchingLabels{deliveryv1alpha1.LabelServing: order})
	})

	It("deploys to every cluster of its Environment, into the Preparation's target namespace, through an Application each", func() {
		reconcileServing()

		Expect(targetClusters()).To(Equal([]string{eu, us}))
		for clusterName, server := range servers {
			app, err := application(clusterName)
			Expect(err).NotTo(HaveOccurred())
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelCluster, clusterName))
			Expect(app.Object).To(HaveKeyWithValue("spec", And(
				HaveKeyWithValue("project", project),
				HaveKeyWithValue("destination", map[string]any{keyServer: server, keyNamespace: testNamespace}))))
		}
		s := getServing()
		Expect(ready(s.Status.Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonDeploying),
			HaveField("Message", ContainSubstring("to srv-eu, srv-us"))))
		Expect(s.Status.Clusters).To(Equal(&deliveryv1alpha1.DeploymentClusters{Selected: 2, Progressing: 2}))
		Expect(ready(s.Status.Targets[0].Conditions)).To(HaveField("Reason", deliveryv1alpha1.ReasonProgressing))
	})

	It("is deployed once every cluster runs the Preparation healthily", func() {
		reportHealthy(eu)
		reconcileServing()
		s := getServing()
		Expect(ready(s.Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonDeploying))
		Expect(s.Status.Clusters).To(Equal(&deliveryv1alpha1.DeploymentClusters{Selected: 2, Healthy: 1, Progressing: 1}))
		Expect(targetClusters()).To(Equal([]string{us}), "a healthy cluster is counted, not listed")
		Expect(healthyDigest(s)).To(BeEmpty(), "one cluster does not make the deployment healthy")

		reportHealthy(us)
		reconcileServing()
		s = getServing()
		Expect(ready(s.Status.Conditions)).To(HaveField("Reason", deliveryv1alpha1.ReasonDeployed))
		Expect(s.Status.Targets).To(BeEmpty())
		Expect(healthyDigest(s)).To(Equal(digest))
		Expect(s.Status.History[0].DeploymentRecord).NotTo(BeEmpty())
	})

	It("stops deploying to a cluster that leaves the Environment, and keeps the others", func() {
		labelCluster(us, testRetired)

		reconcileServing()
		Expect(getServing().Status.Clusters).To(HaveField("Selected", BeEquivalentTo(1)))
		_, err := application(us)
		Expect(errors.IsNotFound(err)).To(BeTrue())
		_, err = application(eu)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready(getServing().Status.Conditions).Reason).To(Equal(deliveryv1alpha1.ReasonDeployed))
	})

	It("holds a target whose Cluster is not Ready, and stays deployed on the others", func() {
		labelCluster(us, testEdge)
		setClusterStatus(us, deliveryv1alpha1.ReasonUnreachable)

		reconcileServing()
		s := getServing()
		Expect(s.Status.Clusters).To(HaveField("Held", BeEquivalentTo(1)))
		Expect(ready(s.Status.Targets[0].Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonClusterNotReady),
			HaveField("Message", ContainSubstring("cannot be reached"))))
		_, err := application(us)
		Expect(errors.IsNotFound(err)).To(BeTrue())
		Expect(ready(s.Status.Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonDeployed),
			HaveField("Message", ContainSubstring("not ready: "+us))), "a Cluster that is not Ready does not hold the others back")

		setClusterStatus(us, deliveryv1alpha1.ReasonReady)
		reconcileServing()
		_, err = application(us)
		Expect(err).NotTo(HaveOccurred())
	})

	It("deploys once to a server two of its Clusters connect to", func() {
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: us}, c)).To(Succeed())
		c.Status.Server = servers[eu]
		Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())

		reconcileServing()
		Expect(ready(getServing().Status.Targets[0].Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonClusterConflict),
			HaveField("Message", ContainSubstring("as Cluster "+eu+" does"))))

		setClusterStatus(us, deliveryv1alpha1.ReasonReady)
		reconcileServing()
		Expect(targetReasons()).NotTo(ContainElement(deliveryv1alpha1.ReasonClusterConflict))
	})

	It("holds a target Argo CD rejects, with Argo CD's message", func() {
		app, err := application(eu)
		Expect(err).NotTo(HaveOccurred())
		app.Object[statusField] = map[string]any{"conditions": []any{map[string]any{
			"type": "InvalidSpecError", "message": "application destination is not permitted in project",
		}}}
		Expect(k8sClient.Status().Update(ctx, app)).To(Succeed())

		reconcileServing()
		Expect(ready(getServing().Status.Targets[0].Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonRejected),
			HaveField("Message", ContainSubstring("is not permitted in project"))))

		reportHealthy(eu)
		reconcileServing()
		Expect(targetReasons()).NotTo(ContainElement(deliveryv1alpha1.ReasonRejected))
	})

	It("does not deploy to a cluster whose kubelet cannot reach the bundled registry", func() {
		template := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: prep}, template)).To(Succeed())
		vendored := &deliveryv1alpha1.Preparation{Name: prep + "-vendored", Namespace: testNamespace, Spec: template.Spec}
		vendored.Spec.Images = []deliveryv1alpha1.PreparationImage{{
			Ref:      "ghcr.io/stefanprodan/podinfo:6.14.1",
			Name:     "ghcr.io/stefanprodan/podinfo",
			Vendored: artifact.DefaultRegistryHost + "/default/vendor/images/ghcr.io/stefanprodan/podinfo:6.14.1@" + digest,
		}}
		Expect(k8sClient.Create(ctx, vendored)).To(Succeed())
		markPreparationReady(ctx, registry, client.ObjectKeyFromObject(vendored))
		s := getServing()
		s.Spec.PreparationName = vendored.Name
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		reconcileServing()
		s = getServing()
		Expect(s.Status.Targets).To(HaveLen(2))
		Expect(ready(s.Status.Targets[0].Conditions)).To(HaveField("Reason", deliveryv1alpha1.ReasonRegistryUnreachable))
		Expect(ready(s.Status.Conditions)).To(HaveField("Reason", deliveryv1alpha1.ReasonDeploymentFailed))

		s.Spec.PreparationName = prep
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcileServing()
		Expect(targetReasons()).NotTo(ContainElement(deliveryv1alpha1.ReasonRegistryUnreachable))
		Expect(k8sClient.Delete(ctx, vendored)).To(Succeed())
	})

	It("does not deploy through an AppProject that does not admit its namespace, whatever the Environment reports", func() {
		admitNamespaces("team-*")
		setEnvironmentReady()

		reconcileServing()
		s := getServing()
		Expect(ready(s.Status.Conditions)).To(And(HaveField("Reason", deliveryv1alpha1.ReasonProjectNotPermitted),
			HaveField("Message", ContainSubstring("does not admit namespace "+testNamespace))))
		Expect(s.Status.Targets).To(BeEmpty())
		_, err := application(eu)
		Expect(errors.IsNotFound(err)).To(BeTrue(), "it stops deploying there")

		admitNamespaces(testNamespace)
		reconcileServing()
		_, err = application(eu)
		Expect(err).NotTo(HaveOccurred())
	})

	It("holds a new Preparation that does not run after the stages its Environment requires", func() {
		env := &deliveryv1alpha1.Environment{}
		Expect(k8sClient.Get(ctx, envKey, env)).To(Succeed())
		env.Spec.Promotion = &deliveryv1alpha1.EnvironmentPromotion{
			RequireAfter: []deliveryv1alpha1.RequiredEnvironment{{Environment: "pre-edge", MinSoakSeconds: 60}},
		}
		Expect(k8sClient.Update(ctx, env)).To(Succeed())
		next := createTestPreparation(ctx, prep+"-next", order, "sha256:6666666666666666666666666666666666666666666666666666666666666666", nil)
		markPreparationReady(ctx, registry, client.ObjectKeyFromObject(next))
		s := getServing()
		s.Spec.PreparationName = next.Name
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		reconcileServing()
		s = getServing()
		Expect(apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeUpstreamDeployed)).To(And(
			HaveField("Reason", deliveryv1alpha1.ReasonPipelineRequired),
			HaveField("Message", ContainSubstring("an Order of Environment pre-edge with a soak of at least 60s"))))
		Expect(s.DeployedPreparationName()).To(Equal(prep), "the deployed Preparation keeps running")

		By("letting a rollback to the Preparation it promoted before pass")
		s.Spec.PreparationName = prep
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcileServing()
		Expect(apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeUpstreamDeployed)).To(BeNil())
		_ = k8sClient.Delete(ctx, next)
	})

	It("deletes the Applications of every cluster with the Serving", func() {
		Expect(k8sClient.Delete(ctx, getServing())).To(Succeed())
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		apps := argocd.NewList()
		Expect(k8sClient.List(ctx, apps, client.InNamespace(argoNamespace), client.MatchingLabels{deliveryv1alpha1.LabelServing: order})).To(Succeed())
		Expect(apps.Items).To(BeEmpty())
	})
})

var _ = Describe("Serving Applications", func() {
	const (
		order  = "podinfo"
		prep   = "podinfo-p1"
		digest = "sha256:7676767676767676767676767676767676767676767676767676767676767676"
	)
	ctx := context.Background()
	namespaces := []string{testNamespace, "srv-tenant-b"}
	var registry *oci.FakeClient

	BeforeEach(func() {
		registry = oci.NewFakeClient(nil)
		for _, ns := range namespaces {
			if ns != testNamespace {
				_ = k8sClient.Create(ctx, &corev1.Namespace{Name: ns})
				createLocalEnvironment(ctx, ns)
			}
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
				Name: order, Namespace: ns,
				Spec: deliveryv1alpha1.OrderSpec{
					EnvironmentRef: localEnvironment,
					ProductRef:     deliveryv1alpha1.ProductRef{Name: testProductName},
					Promotion:      &deliveryv1alpha1.PromotionSpec{Mode: deliveryv1alpha1.PromotionModeManual},
				},
			})).To(Succeed())
			var p *deliveryv1alpha1.Preparation
			if ns == testNamespace {
				p = createTestPreparation(ctx, prep, order, digest, nil)
			} else {
				template := &deliveryv1alpha1.Preparation{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: prep}, template)).To(Succeed())
				// An artifact of its own: its provenance names its own Order.
				p = &deliveryv1alpha1.Preparation{Name: prep, Namespace: ns, Spec: template.Spec}
				p.Spec.Artifact.Digest = "sha256:7878787878787878787878787878787878787878787878787878787878787878"
				p.Spec.Artifact.OCIRef = "oci://registry.kokumi.svc.cluster.local:5000/" + ns + "/" + order + "@" + p.Spec.Artifact.Digest
				Expect(k8sClient.Create(ctx, p)).To(Succeed())
			}
			markPreparationReady(ctx, registry, client.ObjectKeyFromObject(p))
			Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Serving{
				Name: order, Namespace: ns,
				Spec: deliveryv1alpha1.ServingSpec{OrderName: order, PreparationName: prep},
			})).To(Succeed())
		}
	})

	AfterEach(func() {
		for _, ns := range namespaces {
			s := &deliveryv1alpha1.Serving{}
			if k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: order}, s) == nil {
				s.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, s)
				_ = k8sClient.Delete(ctx, s)
			}
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: prep, Namespace: ns})
			_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: order, Namespace: ns})
			_ = k8sClient.DeleteAllOf(ctx, argocd.New(), client.InNamespace(argoNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelServingNamespace: ns})
		}
	})

	It("gives Orders of one name in two namespaces an Application each, and deletes only its own", func() {
		r := newServingReconciler(registry)
		keys := map[string]client.ObjectKey{}
		for _, ns := range namespaces {
			key := types.NamespacedName{Namespace: ns, Name: order}
			_, err := settleServing(ctx, r, key)
			Expect(err).NotTo(HaveOccurred())
			s := &deliveryv1alpha1.Serving{}
			Expect(k8sClient.Get(ctx, key, s)).To(Succeed())
			keys[ns] = argocd.Key(s, cluster.InCluster)
			app := argocd.New()
			Expect(k8sClient.Get(ctx, keys[ns], app)).To(Succeed())
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, ns+"/"+order))
		}
		Expect(keys[namespaces[0]]).NotTo(Equal(keys[namespaces[1]]))

		s := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespaces[1], Name: order}, s)).To(Succeed())
		Expect(k8sClient.Delete(ctx, s)).To(Succeed())
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(s)})
		Expect(err).NotTo(HaveOccurred())

		Expect(errors.IsNotFound(k8sClient.Get(ctx, keys[namespaces[1]], argocd.New()))).To(BeTrue())
		Expect(k8sClient.Get(ctx, keys[namespaces[0]], argocd.New())).To(Succeed(), "the other namespace's Application stays")
	})

	It("deploys nothing while Argo CD is not installed, and says so", func() {
		r := newServingReconciler(registry)
		engine := &notInstalled{argoCD: &argoCD{Client: k8sClient}, missing: true}
		r.deployers = map[deliveryv1alpha1.DeployerType]servingDeployer{deliveryv1alpha1.DeployerTypeArgoCD: engine}
		key := types.NamespacedName{Namespace: testNamespace, Name: order}
		_, err := settleServing(ctx, r, key)
		Expect(err).NotTo(HaveOccurred())

		s := &deliveryv1alpha1.Serving{}
		Expect(k8sClient.Get(ctx, key, s)).To(Succeed())
		Expect(apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(
			HaveField("Reason", deliveryv1alpha1.ReasonArgoCDNotInstalled))
		Expect(apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)).To(
			HaveField("Status", metav1.ConditionTrue), "the gates still run")
		Expect(errors.IsNotFound(k8sClient.Get(ctx, argocd.Key(s, cluster.InCluster), argocd.New()))).To(BeTrue())

		By("deploying once Argo CD is installed")
		engine.missing = false
		_, err = settleServing(ctx, r, key)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, argocd.Key(s, cluster.InCluster), argocd.New())).To(Succeed())
	})
})

// notInstalled is Argo CD's engine as it is without its CRDs while missing
// is set, and as it is otherwise.
type notInstalled struct {
	*argoCD
	missing bool
}

func (n *notInstalled) Admit(ctx context.Context, env *deliveryv1alpha1.Environment) (admission, error) {
	if n.missing {
		return admission{deliveryv1alpha1.ReasonArgoCDNotInstalled, "Argo CD's CRDs are not installed"}, nil
	}
	return n.argoCD.Admit(ctx, env)
}

var _ = Describe("Serving targets", func() {
	// unready is a cluster whose Cluster is not Ready.
	const unready = "down"
	const digest = "sha256:8989898989898989898989898989898989898989898989898989898989898989"
	deployed := &promotion.Entry{Preparation: "web-p1", Digest: digest}
	healthy := targetState{health: promotion.HealthHealthy, revision: digest}
	ready := func(t deliveryv1alpha1.DeploymentTarget) *metav1.Condition {
		return apimeta.FindStatusCondition(t.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}
	readyCluster := func(name, server string) *deliveryv1alpha1.Cluster {
		c := &deliveryv1alpha1.Cluster{Name: name, Namespace: testWorkspace, Status: deliveryv1alpha1.ClusterStatus{Server: server},
			Spec: deliveryv1alpha1.ClusterSpec{Connection: deliveryv1alpha1.ClusterConnectionKubeconfig}}
		conditions.MarkTrue(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "ready")
		return c
	}

	It("lists held, then degraded, then progressing targets, at most 64, with short messages, and counts them all", func() {
		targets := make([]deployTarget, 0, 73)
		states := map[string]targetState{}
		for i := range 70 {
			name := fmt.Sprintf("progressing-%02d", i)
			targets = append(targets, deployTarget{cluster: name})
			states[name] = targetState{health: promotion.HealthProgressing, revision: digest}
		}
		targets = append(targets,
			deployTarget{cluster: "healthy"},
			deployTarget{cluster: "degraded"},
			deployTarget{cluster: "held", holdReason: deliveryv1alpha1.ReasonClusterNotReady, holdMessage: strings.Repeat("x", 4096)})
		states["healthy"] = healthy
		states["degraded"] = targetState{health: promotion.HealthDegraded, revision: digest}
		serving := &deliveryv1alpha1.Serving{}

		setTargets(serving, deployed, targets, states)
		Expect(serving.Status.Clusters).To(Equal(&deliveryv1alpha1.DeploymentClusters{
			Selected: 73, Healthy: 1, Degraded: 1, Held: 1, Progressing: 70,
		}))
		Expect(serving.Status.Targets).To(HaveLen(maxListedTargets))
		Expect(serving.Status.Targets[0].Cluster).To(Equal("held"))
		Expect(len(ready(serving.Status.Targets[0]).Message)).To(BeNumerically("<=", maxTargetMessageLength))
		Expect(serving.Status.Targets[1].Cluster).To(Equal("degraded"))
		Expect(serving.Status.Targets[2].Cluster).To(Equal("progressing-00"))
	})

	It("is healthy once every target whose Cluster is Ready is, and degraded once one is", func() {
		targets := []deployTarget{
			{cluster: "eu"}, {cluster: "us"},
			{cluster: unready, holdReason: deliveryv1alpha1.ReasonClusterNotReady},
		}
		Expect(observationOf(deployed, targets, map[string]targetState{"eu": healthy}).Health).To(Equal(promotion.HealthProgressing))
		Expect(observationOf(deployed, targets, map[string]targetState{"eu": healthy, "us": healthy})).To(Equal(
			promotion.Observation{Revision: digest, Health: promotion.HealthHealthy}), "a Cluster that is not Ready does not count")
		Expect(observationOf(deployed, targets, map[string]targetState{
			"eu": healthy, "us": {health: promotion.HealthDegraded, revision: digest},
		}).Health).To(Equal(promotion.HealthDegraded))
		Expect(observationOf(deployed, targets, map[string]targetState{
			"eu": healthy, "us": {health: promotion.HealthHealthy, revision: "sha256:other"},
		}).Health).To(Equal(promotion.HealthProgressing), "the health of another revision says nothing")
		Expect(observationOf(deployed, targets[2:], nil).Health).To(Equal(promotion.HealthProgressing), "no Cluster is Ready")
	})

	It("names at most five failures in Ready, and the Clusters that are not Ready", func() {
		targets := make([]deployTarget, 0, 8)
		for i := range 7 {
			targets = append(targets, deployTarget{cluster: fmt.Sprintf("held-%d", i), holdReason: deliveryv1alpha1.ReasonRejected, holdMessage: "refused"})
		}
		targets = append(targets, deployTarget{cluster: unready, holdReason: deliveryv1alpha1.ReasonClusterNotReady, holdMessage: "unreachable"})
		serving := &deliveryv1alpha1.Serving{}
		setTargets(serving, deployed, targets, nil)

		markDeployment(serving, deployed, targets, observationOf(deployed, targets, nil))
		condition := apimeta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(condition.Reason).To(Equal(deliveryv1alpha1.ReasonDeploymentFailed))
		Expect(condition.Message).To(SatisfyAll(ContainSubstring("failed on 7 of 8 clusters"), HaveSuffix("; and 2 more")))

		targets = []deployTarget{{cluster: "eu"}, {cluster: unready, holdReason: deliveryv1alpha1.ReasonClusterNotReady}}
		states := map[string]targetState{"eu": healthy}
		setTargets(serving, deployed, targets, states)
		markDeployment(serving, deployed, targets, observationOf(deployed, targets, states))
		condition = apimeta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(condition.Reason).To(Equal(deliveryv1alpha1.ReasonDeployed))
		Expect(condition.Message).To(Equal("Deployed Preparation web-p1 to eu; not ready: down"))

		targets = targets[1:]
		setTargets(serving, deployed, targets, nil)
		markDeployment(serving, deployed, targets, observationOf(deployed, targets, nil))
		Expect(apimeta.FindStatusCondition(serving.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(And(
			HaveField("Reason", deliveryv1alpha1.ReasonDeploymentFailed), HaveField("Message", ContainSubstring("no Cluster of the Environment is ready"))))
	})

	It("holds a Cluster that is not Ready, the second of two for one server, and one whose kubelet cannot pull", func() {
		servers := map[string]string{}
		Expect(targetOf(readyCluster("a", "https://one.example/"), "web", "", servers).holdReason).To(BeEmpty())
		Expect(targetOf(readyCluster("b", "https://one.example"), "web", "", servers).holdReason).To(Equal(deliveryv1alpha1.ReasonClusterConflict))

		unready := readyCluster("c", "https://two.example")
		conditions.MarkFalse(unready, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonUnauthorized, "rejected")
		held := targetOf(unready, "web", "", servers)
		Expect(held.holdReason).To(Equal(deliveryv1alpha1.ReasonClusterNotReady))
		Expect(held.holdMessage).To(ContainSubstring("rejected"))
		Expect(targetOf(&deliveryv1alpha1.Cluster{Name: "new"}, "web", "", servers).holdMessage).To(ContainSubstring("not been checked yet"))

		Expect(targetOf(readyCluster("d", "https://three.example"), "web", "registry.kokumi:5000/img", servers).holdReason).
			To(Equal(deliveryv1alpha1.ReasonRegistryUnreachable))
	})
})
