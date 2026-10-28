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
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/oras-project/oras-go/v3/registry/remote/errcode"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const testRegistryURL = "oci://ghcr.io/kokumi-test"

// testPullSecret is a kubernetes.io/dockerconfigjson Secret for host.
func testPullSecret(name, host string) *corev1.Secret {
	return &corev1.Secret{
		Name:      name,
		Namespace: testNamespace,
		Type:      corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(`{"auths":{"` + host + `":{"username":"bot","password":"secret"}}}`),
		},
	}
}

var _ = Describe("Registry Controller", func() {
	ctx := context.Background()

	var registryClient *oci.FakeClient

	reconcileRegistry := func(name string) (reconcile.Result, error) {
		registries := fakeRegistries(registryClient)
		return (&RegistryReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Registries: registries}).
			Reconcile(ctx, reconcile.Request{Namespace: testNamespace, Name: name})
	}

	readyOf := func(name string) *metav1.Condition {
		reg := &deliveryv1alpha1.Registry{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: name}, reg)).To(Succeed())
		return meta.FindStatusCondition(reg.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}

	createRegistry := func(name, secretName string) {
		reg := &deliveryv1alpha1.Registry{
			Name:      name,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.RegistrySpec{URL: testRegistryURL},
		}
		if secretName != "" {
			reg.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
		}
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, reg) })
	}

	createSecret := func(secret *corev1.Secret) {
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })
	}

	BeforeEach(func() {
		registryClient = &oci.FakeClient{}
	})

	It("is ready when the registry accepts its credentials", func() {
		createSecret(testPullSecret("registry-ok", "ghcr.io"))
		createRegistry("registry-ok", "registry-ok")

		result, err := reconcileRegistry("registry-ok")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(registryRecheckInterval))

		ready := readyOf("registry-ok")
		Expect(ready.Status).To(Equal(metav1.ConditionTrue))
		Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonRegistryConnected))
		Expect(ready.Message).To(ContainSubstring("credentials"))
		Expect(registryClient.Pings).To(Equal([]string{"ghcr.io"}))
	})

	It("reports a Secret without an entry for its url", func() {
		createSecret(testPullSecret("registry-quay", "quay.io"))
		createRegistry("registry-no-entry", "registry-quay")

		_, err := reconcileRegistry("registry-no-entry")
		Expect(err).NotTo(HaveOccurred())

		ready := readyOf("registry-no-entry")
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonSecretInvalid))
		Expect(registryClient.Pings).To(BeEmpty(), "an unusable Secret is not sent to the registry")
	})

	It("reports a missing Secret", func() {
		createRegistry("registry-missing-secret", "absent")

		_, err := reconcileRegistry("registry-missing-secret")
		Expect(err).NotTo(HaveOccurred())
		Expect(readyOf("registry-missing-secret").Reason).To(Equal(deliveryv1alpha1.ReasonSecretNotFound))
	})

	It("reports credentials the registry rejects", func() {
		createSecret(testPullSecret("registry-rejected", "ghcr.io"))
		createRegistry("registry-rejected", "registry-rejected")
		registryClient.PingErr = &errcode.ErrorResponse{Method: http.MethodGet, StatusCode: http.StatusUnauthorized}

		result, err := reconcileRegistry("registry-rejected")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(registryRecheckInterval))
		Expect(readyOf("registry-rejected").Reason).To(Equal(deliveryv1alpha1.ReasonUnauthorized))
	})

	It("retries an unreachable registry", func() {
		createRegistry("registry-unreachable", "")
		registryClient.PingErr = errors.New("dial tcp: connection refused")

		_, err := reconcileRegistry("registry-unreachable")
		Expect(err).To(HaveOccurred())

		ready := readyOf("registry-unreachable")
		Expect(ready.Reason).To(Equal(deliveryv1alpha1.ReasonUnreachable))
		Expect(ready.Message).To(ContainSubstring("connection refused"))
	})

	It("creates the Registry of the bundled registry once, and keeps changes to it", func() {
		r := &RegistryReconciler{Client: k8sClient, Namespace: testNamespace, BundledRegistryHost: artifact.DefaultRegistryHost}
		Expect(r.ensureBundledRegistry(k8sClient)(ctx)).To(Succeed())

		reg := &deliveryv1alpha1.Registry{}
		key := client.ObjectKey{Namespace: testNamespace, Name: deliveryv1alpha1.DefaultRegistryName}
		Expect(k8sClient.Get(ctx, key, reg)).To(Succeed())
		Expect(reg.Spec.URL).To(Equal("oci://" + artifact.DefaultRegistryHost))
		Expect(reg.Spec.Scheme).To(Equal(deliveryv1alpha1.RegistrySchemeHTTP))

		reg.Spec.URL = "oci://" + artifact.DefaultRegistryHost + "/team"
		Expect(k8sClient.Update(ctx, reg)).To(Succeed())
		Expect(r.ensureBundledRegistry(k8sClient)(ctx)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, reg)).To(Succeed())
		Expect(reg.Spec.URL).To(HaveSuffix("/team"), "an existing Registry is left as it is")
		Expect(k8sClient.Delete(ctx, reg)).To(Succeed())
	})

	It("checks an untrusted certificate again only periodically", func() {
		createRegistry("registry-untrusted", "")
		registryClient.PingErr = fmt.Errorf("ping: %w", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}})

		result, err := reconcileRegistry("registry-untrusted")
		Expect(err).NotTo(HaveOccurred(), "a configuration error is not retried with backoff")
		Expect(result.RequeueAfter).To(Equal(registryRecheckInterval))
		Expect(readyOf("registry-untrusted").Reason).To(Equal(deliveryv1alpha1.ReasonUnreachable))
	})
})

var _ = Describe("Registry field indexes", func() {
	It("indexes a Registry by the Secrets it names, each once", func() {
		const secretName = "registry-creds"
		reg := &deliveryv1alpha1.Registry{Spec: deliveryv1alpha1.RegistrySpec{
			SecretRef:     &corev1.LocalObjectReference{Name: secretName},
			CertSecretRef: &corev1.LocalObjectReference{Name: secretName},
		}}
		Expect(registrySecrets(reg)).To(Equal([]string{secretName}))
		Expect(registrySecrets(&deliveryv1alpha1.Registry{})).To(BeEmpty())
	})
})
