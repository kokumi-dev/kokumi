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
)

var argoAppGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
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
			Namespace: "default",
		}
		serving := &deliveryv1alpha1.Serving{}

		BeforeEach(func() {
			By("creating the Preparation referenced by the Serving")
			preparation := &deliveryv1alpha1.Preparation{}
			preparationKey := types.NamespacedName{Name: preparationName, Namespace: "default"}
			err := k8sClient.Get(ctx, preparationKey, preparation)
			if err != nil && errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Preparation{
					ObjectMeta: metav1.ObjectMeta{
						Name:      preparationName,
						Namespace: "default",
					},
					Spec: deliveryv1alpha1.PreparationSpec{
						OrderName: orderName,
						Source: deliveryv1alpha1.OrderSource{
							OCI:        "oci://registry.kokumi.svc.cluster.local:5000/order/test-resource",
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
			ns.SetName("argocd")
			_ = k8sClient.Create(ctx, ns)

			By("creating the custom resource for the Kind Serving")
			err = k8sClient.Get(ctx, typeNamespacedName, serving)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Serving{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
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
			preparationKey := types.NamespacedName{Name: preparationName, Namespace: "default"}
			if err := k8sClient.Get(ctx, preparationKey, preparation); err == nil {
				Expect(k8sClient.Delete(ctx, preparation)).To(Succeed())
			}

			By("Cleanup any Argo CD Application created during the test")
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			app.SetNamespace("argocd")
			app.SetName(resourceName)
			_ = k8sClient.Delete(ctx, app)
		})

		newReconciler := func() *ServingReconciler {
			return &ServingReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
		}

		getApp := func() *unstructured.Unstructured {
			app := &unstructured.Unstructured{}
			app.SetGroupVersionKind(argoAppGVK)
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "argocd", Name: resourceName}, app)).To(Succeed())
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

			s := getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
		})

		It("refuses to update a pre-existing Argo CD Application that is missing the opt-in annotation", func() {
			By("creating an Argo CD Application out-of-band without the opt-in annotation")
			preExisting := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]any{
						"name":      resourceName,
						"namespace": "argocd",
					},
					"spec": map[string]any{
						"project": "default",
						"source": map[string]any{
							"repoURL":        "oci://example.com/foreign",
							"targetRevision": "sha256:foreign",
							"path":           ".",
						},
						"destination": map[string]any{
							"server":    "https://kubernetes.default.svc",
							"namespace": "default",
						},
					},
				},
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
			Expect(cond.Message).To(ContainSubstring("opt-in annotation"))

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
				Object: map[string]any{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]any{
						"name":      resourceName,
						"namespace": "argocd",
						"annotations": map[string]any{
							deliveryv1alpha1.AnnotationAllowedOrder: "some-other-order",
						},
					},
					"spec": map[string]any{
						"project": "default",
						"source": map[string]any{
							"repoURL":        "oci://example.com/foreign",
							"targetRevision": "sha256:foreign",
							"path":           ".",
						},
						"destination": map[string]any{
							"server":    "https://kubernetes.default.svc",
							"namespace": "default",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred(), "opt-in denial must not return an error (would cause requeue/flapping)")
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, "some-other-order"))

			s := getServing()
			cond := apimeta.FindStatusCondition(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("DeploymentFailed"))
			Expect(cond.Message).To(ContainSubstring("opt-in annotation"))
			Expect(cond.Message).To(ContainSubstring("some-other-order"))
		})

		It("updates an Application whose allowed-order annotation matches the Order name", func() {
			By("creating an Argo CD Application annotated with the matching opt-in")
			preExisting := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "Application",
					"metadata": map[string]any{
						"name":      resourceName,
						"namespace": "argocd",
						"annotations": map[string]any{
							deliveryv1alpha1.AnnotationAllowedOrder: orderName,
						},
					},
					"spec": map[string]any{
						"project": "default",
						"source": map[string]any{
							"repoURL":        "oci://example.com/stale",
							"targetRevision": "sha256:stale",
							"path":           ".",
						},
						"destination": map[string]any{
							"server":    "https://kubernetes.default.svc",
							"namespace": "default",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, preExisting)).To(Succeed())

			_, err := newReconciler().Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())

			app := getApp()
			spec, _, _ := unstructured.NestedMap(app.Object, "spec")
			source, _ := spec["source"].(map[string]any)
			Expect(source["targetRevision"]).To(Equal(fakeDigest))
			Expect(app.GetAnnotations()).To(HaveKeyWithValue(deliveryv1alpha1.AnnotationAllowedOrder, orderName))

			s := getServing()
			Expect(apimeta.IsStatusConditionTrue(s.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)).To(BeTrue())
		})
	})
})
