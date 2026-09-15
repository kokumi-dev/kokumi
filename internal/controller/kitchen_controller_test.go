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
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Secret data keys expected by the server's authenticator builder.
const (
	secretName            = "kokumi-server-auth"
	secretKeyUsername     = "username"
	secretKeyPasswordHash = "password-hash"
	secretKeySigningKey   = "signing-key"
	tokenSecretName       = deliveryv1alpha1.DefaultTokenSigningKeySecretName
	userTokenSecretName   = "my-gitops-key"
)

var _ = Describe("Kitchen Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: testNamespace,
		}
		kitchen := &deliveryv1alpha1.Kitchen{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Kitchen")
			err := k8sClient.Get(ctx, typeNamespacedName, kitchen)
			if err != nil && errors.IsNotFound(err) {
				resource := &deliveryv1alpha1.Kitchen{
					Name:      resourceName,
					Namespace: testNamespace,
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &deliveryv1alpha1.Kitchen{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Kitchen")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &KitchenReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				Namespace: testNamespace,
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

func newKitchenReconciler() *KitchenReconciler {
	return &KitchenReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Namespace: testNamespace}
}

func reconcileSingleton(ctx context.Context) {
	_, err := newKitchenReconciler().Reconcile(ctx, reconcile.Request{
		Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
	})
	Expect(err).NotTo(HaveOccurred())
}

func getKitchen(ctx context.Context) *deliveryv1alpha1.Kitchen {
	k := &deliveryv1alpha1.Kitchen{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace}, k)).To(Succeed())
	return k
}

func readyCondition(k *deliveryv1alpha1.Kitchen) *metav1.Condition {
	for i := range k.Status.Conditions {
		if k.Status.Conditions[i].Type == deliveryv1alpha1.ConditionTypeReady {
			return &k.Status.Conditions[i]
		}
	}
	return nil
}

var _ = Describe("Kitchen adminUser secret validation", func() {
	ctx := context.Background()

	BeforeEach(func() {
		By("ensuring the install namespace exists")
		ns := &corev1.Namespace{Name: testNamespace}
		err := k8sClient.Create(ctx, ns)
		if err != nil && !errors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
	})

	AfterEach(func() {
		By("removing the singleton Kitchen and any auth Secret")
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
		})
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			Name: secretName, Namespace: testNamespace,
		})
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			Name: tokenSecretName, Namespace: testNamespace,
		})
	})

	It("reports Ready=False when the admin credentials Secret is missing", func() {
		By("creating a Kitchen with adminUser enabled and no Secret")
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())

		By("reconciling")
		reconcileSingleton(ctx)

		By("checking the Ready condition is False")
		cond := readyCondition(getKitchen(ctx))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(secretName))
	})

	It("recovers to Ready=True once the credentials Secret appears", func() {
		By("creating a Kitchen with adminUser enabled and no Secret")
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())
		reconcileSingleton(ctx)
		Expect(readyCondition(getKitchen(ctx)).Status).To(Equal(metav1.ConditionFalse))

		By("creating the complete credentials Secret")
		hash, err := bcrypt.GenerateFromPassword([]byte("s3cret-passw0rd"), bcrypt.MinCost)
		Expect(err).NotTo(HaveOccurred())
		secret := &corev1.Secret{
			Name: secretName, Namespace: testNamespace,
			Data: map[string][]byte{
				secretKeyUsername:     []byte("admin"),
				secretKeyPasswordHash: hash,
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		By("reconciling again (Secret watch triggers this in the running manager)")
		reconcileSingleton(ctx)

		By("checking the Ready condition is now True")
		cond := readyCondition(getKitchen(ctx))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("reports Ready=False when the Secret is incomplete", func() {
		By("creating the Kitchen and an incomplete Secret (missing password-hash)")
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					AdminUser: &deliveryv1alpha1.AdminUserConfig{},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())
		secret := &corev1.Secret{
			Name: secretName, Namespace: testNamespace,
			Data: map[string][]byte{
				secretKeyUsername: []byte("admin"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		By("reconciling")
		reconcileSingleton(ctx)

		By("checking the Ready condition is False")
		cond := readyCondition(getKitchen(ctx))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(secretName))
	})

	It("creates the token signing-key Secret and reports Ready=True for OIDC-only auth", func() {
		By("creating a Kitchen with OIDC only (no adminUser)")
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					OIDC: &deliveryv1alpha1.OIDCConfig{
						IssuerURL: "https://sso.example.com",
						ClientID:  "kokumi",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())

		By("creating the OIDC client Secret")
		oidcSecret := &corev1.Secret{
			Name: "kokumi-server-oidc", Namespace: testNamespace,
			Data: map[string][]byte{"client-secret": []byte("s3cret")},
		}
		Expect(k8sClient.Create(ctx, oidcSecret)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, oidcSecret)
		})

		By("reconciling")
		reconcileSingleton(ctx)

		By("checking the token signing-key Secret was created with a signing-key")
		tokenSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tokenSecretName, Namespace: testNamespace}, tokenSecret)).To(Succeed())
		Expect(tokenSecret.Data[secretKeySigningKey]).NotTo(BeEmpty())

		By("checking the Ready condition is True")
		cond := readyCondition(getKitchen(ctx))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
	})

	It("does not rotate the token signing-key Secret on re-reconcile", func() {
		By("creating a Kitchen with auth and reconciling twice")
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())
		reconcileSingleton(ctx)

		tokenSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tokenSecretName, Namespace: testNamespace}, tokenSecret)).To(Succeed())
		firstKey := string(tokenSecret.Data[secretKeySigningKey])
		Expect(firstKey).NotTo(BeEmpty())

		reconcileSingleton(ctx)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tokenSecretName, Namespace: testNamespace}, tokenSecret)).To(Succeed())
		Expect(string(tokenSecret.Data[secretKeySigningKey])).To(Equal(firstKey))
	})

	It("uses a user-supplied token signing-key Secret read-only", func() {
		By("creating a user-managed signing-key Secret and pointing the Kitchen at it")
		userSecret := &corev1.Secret{
			Name: userTokenSecretName, Namespace: testNamespace,
			Data: map[string][]byte{secretKeySigningKey: []byte("user-provided-key")},
		}
		Expect(k8sClient.Create(ctx, userSecret)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, userSecret)
		})
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					TokenSigningKeySecretRef: &corev1.LocalObjectReference{Name: userTokenSecretName},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())
		reconcileSingleton(ctx)

		By("checking the user Secret was not modified")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: userTokenSecretName, Namespace: testNamespace}, userSecret)).To(Succeed())
		Expect(string(userSecret.Data[secretKeySigningKey])).To(Equal("user-provided-key"))

		By("checking the default token Secret was not created")
		err := k8sClient.Get(ctx, types.NamespacedName{Name: tokenSecretName, Namespace: testNamespace}, &corev1.Secret{})
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("reports Ready=False when the user-supplied token Secret lacks signing-key", func() {
		By("creating an incomplete user-managed signing-key Secret")
		userSecret := &corev1.Secret{
			Name: userTokenSecretName, Namespace: testNamespace,
			Data: map[string][]byte{"other": []byte("value")},
		}
		Expect(k8sClient.Create(ctx, userSecret)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, userSecret)
		})
		kitchen := &deliveryv1alpha1.Kitchen{
			Name: deliveryv1alpha1.DefaultKitchenName, Namespace: testNamespace,
			Spec: deliveryv1alpha1.KitchenSpec{
				Auth: &deliveryv1alpha1.KitchenAuth{
					TokenSigningKeySecretRef: &corev1.LocalObjectReference{Name: userTokenSecretName},
				},
			},
		}
		Expect(k8sClient.Create(ctx, kitchen)).To(Succeed())
		reconcileSingleton(ctx)

		By("checking the Ready condition is False with a clear message")
		cond := readyCondition(getKitchen(ctx))
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Message).To(ContainSubstring(userTokenSecretName))
	})
})
