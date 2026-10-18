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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/approval"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/provenance"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
)

var argoAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// argoNamespace is the namespace the Serving controller places Applications in.
const argoNamespace = argocd.Namespace

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
						OrderName: orderName,
						Source: deliveryv1alpha1.OrderSource{
							URL:        testOCIRef,
							BaseDigest: fakeDigest,
						},
						Renderer: deliveryv1alpha1.Renderer{
							Type:    deliveryv1alpha1.RenderTypeManifest,
							Version: "v1.0.0",
							Builder: testBuilderID,
						},
						ConfigHash: "sha256:abc123",
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@" + fakeDigest,
							Digest: fakeDigest,
						},
					},
				})).To(Succeed())
			}
			markPreparationReady(ctx, registry, preparationKey)

			By("ensuring the argocd namespace exists")
			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
			ns.SetName(argoNamespace)
			_ = k8sClient.Create(ctx, ns)

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
			app.SetName(resourceName)
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
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: resourceName}, app)).To(Succeed())

			updated := app.DeepCopy()
			status := map[string]any{}
			if syncStatus != "" || revision != "" {
				status["sync"] = map[string]any{
					"status":   syncStatus,
					"revision": revision,
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
			Expect(k8sClient.Status().Update(ctx, updated)).To(Succeed())
		}

		getApp := func() *unstructured.Unstructured {
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: resourceName}, app)).To(Succeed())
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
				"name":      resourceName,
				"namespace": argoNamespace,
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
						"server":    "https://kubernetes.default.svc",
						"namespace": testNamespace,
					},
				},
			}
		}

		It("creates an Argo CD Application with the allowed-order annotation set to the Order name", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, orderName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelOrder, orderName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelServing, resourceName))
			Expect(app.GetLabels()).To(HaveKeyWithValue(deliveryv1alpha1.LabelServingNamespace, testNamespace))

			By("staying in Deploying until the Application reports healthy at the desired revision")
			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal("Deploying"))
			Expect(s.Status.ObservedPreparationName).To(Equal(preparationName),
				"the promoted preparation must already be the observed (active) preparation while deploying")
			Expect(s.Status.DeployedDigest).To(BeEmpty())

			By("simulating Argo CD syncing the Application to the desired revision")
			setAppStatus("Synced", fakeDigest, "Healthy", "")

			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			s = getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
			Expect(s.Status.DeployedDigest).To(Equal(fakeDigest))
			Expect(s.Status.ObservedPreparationName).To(Equal(preparationName))
		})

		DescribeTable("reports a promoted Preparation as Deploying while the Application is still at an older revision",
			func(oldHealth string) {
				const v2Digest = "sha256:aaaaaaaaaabbbbbbbbbbccccccccccddddddddddeeeeeeeeeeffffffffffffff"

				By("deploying and syncing to the desired revision")
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())
				setAppStatus("Synced", fakeDigest, "Healthy", "")
				_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())
				Expect(getServing().Status.DeployedDigest).To(Equal(fakeDigest))
				setAppStatus("Synced", fakeDigest, oldHealth, "old revision is "+oldHealth)

				By("pointing the Serving at a new preparation digest")
				s := getServing()
				s.Spec.PreparationName = preparationName + "-v2"
				Expect(k8sClient.Update(ctx, s)).To(Succeed())

				preparationV2 := &deliveryv1alpha1.Preparation{
					Name:      preparationName + "-v2",
					Namespace: testNamespace,
					Spec: deliveryv1alpha1.PreparationSpec{
						OrderName: orderName,
						Source: deliveryv1alpha1.OrderSource{
							URL:        testOCIRef,
							BaseDigest: fakeDigest,
						},
						Renderer: deliveryv1alpha1.Renderer{
							Type:    deliveryv1alpha1.RenderTypeManifest,
							Version: "v1.0.0",
							Builder: testBuilderID,
						},
						ConfigHash: "sha256:abc123",
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
				_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				s = getServing()
				cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal("Deploying"),
					"the health of the old revision must not be reported for the promoted Preparation")
				Expect(s.Status.ObservedPreparationName).To(Equal(preparationName + "-v2"))
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
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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

			result, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero(), "the Preparation watch triggers the next reconcile")

			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal("Pending"))
			Expect(cond.Message).To(ContainSubstring(notReady.Name))
			Expect(targetRevision()).To(Equal(fakeDigest), "the deployed Preparation must stay in place")

			By("deploying it once it is ready")
			markPreparationReady(ctx, registry, client.ObjectKeyFromObject(notReady))
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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

				_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			cond := apimeta.FindStatusCondition(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeVerified)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonSigningKeyNotReady))
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: resourceName}, app))).To(BeTrue())
		})

		It("marks the Serving as failed when the Application reports Degraded", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			By("simulating a degraded Application")
			setAppStatus("Synced", fakeDigest, "Degraded", "some resources are unhealthy")

			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			setAppStatus("Synced", fakeDigest, "Healthy", "")
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(apimeta.IsStatusConditionTrue(getServing().Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())

			By("degrading the Application without changing the Serving spec")
			setAppStatus("Synced", fakeDigest, "Degraded", "pod crashed")

			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())
				if healthyFirst {
					setAppStatus("Synced", fakeDigest, "Healthy", "")
					_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
					Expect(err).NotTo(HaveOccurred())
				}
				setAppStatus("Synced", fakeDigest, "Degraded", "hpa unhealthy")
				_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
				Expect(err).NotTo(HaveOccurred())

				before := getServing()
				cond := apimeta.FindStatusCondition(before.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
				Expect(cond).NotTo(BeNil())
				Expect(cond.Reason).To(Equal("DeploymentFailed"))
				Expect(before.Status.ObservedPreparationName).To(Equal(preparationName))

				By("reconciling again without any change")
				_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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

			result, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
				_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
					deliveryv1alpha1.AnnotationAllowedOrder: orderName,
				}),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			source, _ := spec["source"].(map[string]any)
			Expect(source["targetRevision"]).To(Equal(fakeDigest))
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, orderName))

			By("completing the rollout")
			setAppStatus("Synced", fakeDigest, "Healthy", "")
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			s := getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
		})

		// deleteServing deletes the Serving and reconciles it, as the
		// controller does once the deletion timestamp is set.
		deleteServing := func() {
			Expect(k8sClient.Delete(ctx, getServing())).To(Succeed())
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(errors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &deliveryv1alpha1.Serving{}))).To(BeTrue(),
				"the finalizer must be removed")
		}

		It("deletes its Application with the Serving", func() {
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			getApp()

			deleteServing()

			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(errors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: resourceName}, app))).To(BeTrue())
		})

		It("keeps an Application that is not opted in when the Serving is deleted", func() {
			By("creating an Argo CD Application out-of-band without the opt-in annotation")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap("oci://example.com/foreign", "sha256:foreign", nil),
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
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
			_, err := newServingReconciler(registry).Reconcile(ctx, reconcile.Request{NamespacedName: key})
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
			return k8sClient.Get(ctx, client.ObjectKey{Namespace: argoNamespace, Name: orderName}, app) == nil
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

			ns := &unstructured.Unstructured{}
			ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
			ns.SetName(argoNamespace)
			_ = k8sClient.Create(ctx, ns)

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
				app.SetName(orderName)
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

// newServingReconciler returns a ServingReconciler that verifies evidence in
// registry against testSigner.
func newServingReconciler(registry *oci.FakeClient) *ServingReconciler {
	return &ServingReconciler{
		Client:     k8sClient,
		Scheme:     k8sClient.Scheme(),
		Registries: fakeRegistries(registry),
		Signing:    testSigning,
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
