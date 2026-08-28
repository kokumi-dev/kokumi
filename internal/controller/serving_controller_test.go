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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/deployer"
)

var argoAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// argoNamespace is the namespace the ArgoCDDeployer places Applications in.
const argoNamespace = "argocd"

// buildArgoAppMap returns a map for an unstructured Argo CD Application.
func buildArgoAppMap(name, repoURL, targetRevision string, annotations map[string]any) map[string]any {
	meta := map[string]any{
		"name":      name,
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

		BeforeEach(func() {
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
							OCI:        testOCIRef,
							BaseDigest: fakeDigest,
						},
						Renderer: deliveryv1alpha1.Renderer{
							Version:    "v1.0.0",
							Digest:     fakeDigest,
							RenderType: deliveryv1alpha1.RenderTypeManifest,
						},
						ConfigHash: "sha256:abc123",
						Artifact: deliveryv1alpha1.Artifact{
							OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/preparation/test-resource@" + fakeDigest,
							Digest: fakeDigest,
						},
					},
				})).To(Succeed())
			}

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
						PreparationPolicy: deliveryv1alpha1.PreparationPolicy{
							Type: deliveryv1alpha1.PreparationPolicyManual,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
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
			return &ServingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Deployer: deployer.NewArgoCD(k8sClient),
			}
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

		It("does not report Deployed while the Application is healthy at an older revision", func() {
			const v2Digest = "sha256:aaaaaaaaaabbbbbbbbbbccccccccccddddddddddeeeeeeeeeeffffffffffffff"

			By("deploying and syncing to the desired revision")
			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			setAppStatus("Synced", fakeDigest, "Healthy", "")
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(getServing().Status.DeployedDigest).To(Equal(fakeDigest))

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
						OCI:        testOCIRef,
						BaseDigest: fakeDigest,
					},
					Renderer: deliveryv1alpha1.Renderer{
						Version:    "v1.0.0",
						Digest:     fakeDigest,
						RenderType: deliveryv1alpha1.RenderTypeManifest,
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

			By("reconciling while the Application is still healthy at the old revision")
			_, err = newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			s = getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Reason).To(Equal("Deploying"),
				"must not report Deployed while the Application is healthy at an older revision")
			Expect(s.Status.ObservedPreparationName).To(Equal(preparationName + "-v2"))
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

		It("refuses to update a pre-existing Argo CD Application that is missing the opt-in annotation", func() {
			By("creating an Argo CD Application out-of-band without the opt-in annotation")
			preExisting := &unstructured.Unstructured{
				Object: buildArgoAppMap(resourceName, "oci://example.com/foreign", "sha256:foreign", nil),
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
				Object: buildArgoAppMap(resourceName, "oci://example.com/foreign", "sha256:foreign", map[string]any{
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
				Object: buildArgoAppMap(resourceName, "oci://example.com/stale", "sha256:stale", map[string]any{
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
	})
})
