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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

var _ = Describe("Repository Controller", func() {
	ctx := context.Background()

	reconcileRepository := func(name string) *metav1.Condition {
		_, err := (&RepositoryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}).
			Reconcile(ctx, reconcile.Request{Namespace: testNamespace, Name: name})
		Expect(err).NotTo(HaveOccurred())

		repo := &deliveryv1alpha1.Repository{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: name}, repo)).To(Succeed())
		return meta.FindStatusCondition(repo.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}

	createRepository := func(name, url, registryName string) {
		repo := &deliveryv1alpha1.Repository{
			Name:      name,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.RepositorySpec{URL: url},
		}
		if registryName != "" {
			repo.Spec.RegistryRef = &corev1.LocalObjectReference{Name: registryName}
		}
		Expect(k8sClient.Create(ctx, repo)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, repo) })
	}

	BeforeEach(func() {
		reg := &deliveryv1alpha1.Registry{
			Name:      "repository-test",
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.RegistrySpec{URL: testRegistryURL},
		}
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, reg) })
	})

	It("is ready without a Registry", func() {
		createRepository("repo-anonymous", "oci://ghcr.io/stefanprodan/charts/podinfo", "")
		Expect(reconcileRepository("repo-anonymous").Status).To(Equal(metav1.ConditionTrue))
	})

	It("is ready when its Registry covers it", func() {
		createRepository("repo-covered", testRegistryURL+"/charts/app", "repository-test")
		ready := reconcileRepository("repo-covered")
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonRepositoryListed))
	})

	It("reports a Registry that does not cover it", func() {
		createRepository("repo-not-covered", "oci://ghcr.io/kokumi-testing/app", "repository-test")
		Expect(reconcileRepository("repo-not-covered").Reason).To(Equal(deliveryv1alpha1.ReasonRegistryNotCovered))
	})

	It("reports a missing Registry", func() {
		createRepository("repo-missing-registry", testRegistryURL+"/app", "absent")
		Expect(reconcileRepository("repo-missing-registry").Reason).To(Equal(deliveryv1alpha1.ReasonRegistryNotFound))
	})
})
