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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
)

// testContext is the context of the kubeconfigs of the tests, and its
// cluster.
const testContext = "spoke"

// testKubeconfig returns a self-contained kubeconfig of server with token.
func testKubeconfig(server, token string) []byte {
	config := clientcmdapi.NewConfig()
	config.Clusters[testContext] = &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: []byte("ca")}
	config.AuthInfos["deployer"] = &clientcmdapi.AuthInfo{Token: token}
	config.Contexts[testContext] = &clientcmdapi.Context{Cluster: testContext, AuthInfo: "deployer"}
	config.CurrentContext = testContext
	data, err := clientcmd.Write(*config)
	Expect(err).NotTo(HaveOccurred())
	return data
}

// kubeconfigCluster returns a Cluster name of namespace that reaches its
// cluster with the kubeconfig in the Secret secretName.
func kubeconfigCluster(namespace, name, secretName string) *deliveryv1alpha1.Cluster {
	return &deliveryv1alpha1.Cluster{
		Name: name, Namespace: namespace,
		Spec: deliveryv1alpha1.ClusterSpec{
			Connection: deliveryv1alpha1.ClusterConnectionKubeconfig,
			Kubeconfig: &deliveryv1alpha1.ClusterKubeconfig{SecretRef: deliveryv1alpha1.KubeconfigSecretReference{Name: secretName}},
		},
	}
}

var _ = Describe("Cluster Controller", Ordered, func() {
	const (
		spoke  = "cl-spoke"
		secret = "cl-spoke-kubeconfig"
		server = "https://spoke.example:6443"
	)
	ctx := context.Background()
	key := types.NamespacedName{Namespace: testWorkspace, Name: spoke}
	healthy := cluster.Health{Healthy: true, Message: "The control plane is ready", Version: "v1.37.0"}
	var (
		r      *ClusterReconciler
		prober *fakeProber
	)

	reconcileCluster := func(k types.NamespacedName) (reconcile.Result, *deliveryv1alpha1.Cluster) {
		result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: k})
		Expect(err).NotTo(HaveOccurred())
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, k, c)).To(Succeed())
		return result, c
	}
	ready := func(c *deliveryv1alpha1.Cluster) *metav1.Condition {
		return apimeta.FindStatusCondition(c.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}
	writeKubeconfig := func(namespace, name string, kubeconfig []byte) {
		s := &corev1.Secret{Namespace: namespace, Name: name}
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(s), s)
		s.Data = map[string][]byte{cluster.KubeconfigKey: kubeconfig}
		if apierrors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, s)).To(Succeed())
			return
		}
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
	}

	BeforeAll(func() {
		prober = &fakeProber{health: healthy}
		r = &ClusterReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(),
			Namespace: testKokumiNamespace, LocalConfig: cfg, clusters: prober,
		}
		Expect(k8sClient.Create(ctx, kubeconfigCluster(testWorkspace, spoke, secret))).To(Succeed())
	})

	AfterAll(func() {
		for _, namespace := range []string{testWorkspace, testKokumiNamespace} {
			clusters := &deliveryv1alpha1.ClusterList{}
			Expect(k8sClient.List(ctx, clusters, client.InNamespace(namespace))).To(Succeed())
			for i := range clusters.Items {
				c := &clusters.Items[i]
				c.SetFinalizers(nil)
				_ = k8sClient.Update(ctx, c)
				_ = k8sClient.Delete(ctx, c)
			}
			_ = k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(namespace))
		}
	})

	It("reports a missing kubeconfig Secret", func() {
		_, c := reconcileCluster(key)
		Expect(c.Finalizers).To(ContainElement(deliveryv1alpha1.Finalizer))
		Expect(ready(c)).To(HaveField("Reason", deliveryv1alpha1.ReasonSecretNotFound))
		Expect(ready(c).Message).To(ContainSubstring(secret))
	})

	It("reports a kubeconfig that is not self-contained", func() {
		config := clientcmdapi.NewConfig()
		config.Clusters[testContext] = &clientcmdapi.Cluster{Server: server}
		config.AuthInfos["deployer"] = &clientcmdapi.AuthInfo{Exec: &clientcmdapi.ExecConfig{Command: "aws"}}
		config.Contexts[testContext] = &clientcmdapi.Context{Cluster: testContext, AuthInfo: "deployer"}
		config.CurrentContext = testContext
		data, err := clientcmd.Write(*config)
		Expect(err).NotTo(HaveOccurred())
		writeKubeconfig(testWorkspace, secret, data)

		_, c := reconcileCluster(key)
		Expect(ready(c)).To(HaveField("Reason", deliveryv1alpha1.ReasonSecretInvalid))
	})

	It("reports the server, version and readiness of the cluster", func() {
		writeKubeconfig(testWorkspace, secret, testKubeconfig(server, "token"))

		result, c := reconcileCluster(key)
		Expect(result.RequeueAfter).To(BeNumerically("~", clusterProbeInterval, clusterProbeJitter*float64(clusterProbeInterval)),
			"Kokumi checks every Cluster again, spread out")
		Expect(c.Status.Server).To(Equal(server))
		Expect(c.Status.KubernetesVersion).To(Equal("v1.37.0"))
		Expect(ready(c)).To(HaveField("Status", metav1.ConditionTrue))
	})

	It("reports a control plane that rejects the credential", func() {
		prober.health = cluster.Health{Unauthorized: true, Message: "rejected"}
		DeferCleanup(func() {
			prober.health = healthy
		})

		_, c := reconcileCluster(key)
		Expect(ready(c)).To(HaveField("Reason", deliveryv1alpha1.ReasonUnauthorized))
		Expect(c.Status.KubernetesVersion).To(Equal("v1.37.0"), "the version it read before stays")
	})

	It("reports a control plane that cannot be reached", func() {
		prober.health = cluster.Health{Message: "The control plane is not ready or cannot be reached"}
		DeferCleanup(func() {
			prober.health = healthy
		})

		_, c := reconcileCluster(key)
		Expect(ready(c)).To(HaveField("Reason", deliveryv1alpha1.ReasonUnreachable))
	})

	It("removes the finalizer of a deleted Cluster", func() {
		c := &deliveryv1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, key, c)).To(Succeed())
		Expect(k8sClient.Delete(ctx, c)).To(Succeed())

		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, c))).To(BeTrue())
	})

	It("keeps the Cluster in-cluster in Kokumi's namespace, ready through Kokumi's own connection", func() {
		inCluster := types.NamespacedName{Namespace: testKokumiNamespace, Name: cluster.InCluster}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: inCluster})
		Expect(err).NotTo(HaveOccurred(), "the reconcile at start creates it")

		_, c := reconcileCluster(inCluster)
		Expect(c.Labels).To(HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true"))
		Expect(c.Status.Server).To(Equal(cluster.InClusterServer))
		Expect(ready(c)).To(HaveField("Status", metav1.ConditionTrue))

		c.Labels["stage"] = "edge"
		delete(c.Labels, deliveryv1alpha1.LabelInCluster)
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		_, c = reconcileCluster(inCluster)
		Expect(c.Labels).To(SatisfyAll(HaveKeyWithValue("stage", "edge"), Not(HaveKey(deliveryv1alpha1.LabelInCluster))),
			"Kokumi leaves the labels it is given")

		c.SetFinalizers(nil)
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		Expect(k8sClient.Delete(ctx, c)).To(Succeed())
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: inCluster})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, inCluster, c)).To(Succeed(), "a deleted Cluster in-cluster comes back")
		Expect(c.Labels).To(HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true"))
	})

	It("leaves the labels of a Cluster that connects in-cluster alone", func() {
		local := types.NamespacedName{Namespace: testWorkspace, Name: "cl-local"}
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Cluster{
			Name: local.Name, Namespace: local.Namespace,
			Spec: deliveryv1alpha1.ClusterSpec{Connection: deliveryv1alpha1.ClusterConnectionInCluster},
		})).To(Succeed())

		_, c := reconcileCluster(local)
		Expect(c.Labels).NotTo(HaveKey(deliveryv1alpha1.LabelInCluster), "only the Cluster Kokumi keeps is created with it")
		Expect(ready(c)).To(HaveField("Status", metav1.ConditionTrue))
	})
})

var _ = Describe("Cluster probes and mapping", func() {
	ctx := context.Background()

	It("probes a cluster at most once per half interval, and again when its kubeconfig changes", func() {
		var cache probeCache
		probes := 0
		probe := func() cluster.Health { probes++; return cluster.Health{Healthy: true, Message: fmt.Sprint(probes)} }
		start := time.Now()

		Expect(cache.probe("ns/a", "1", start, probe).Message).To(Equal("1"))
		Expect(cache.probe("ns/a", "1", start.Add(clusterProbeInterval/4), probe).Message).To(Equal("1"), "a reconcile for another reason")
		Expect(cache.probe("ns/a", "2", start.Add(clusterProbeInterval/4), probe).Message).To(Equal("2"), "a new kubeconfig")
		Expect(cache.probe("ns/a", "2", start.Add(clusterProbeInterval), probe).Message).To(Equal("3"), "the timer")
		Expect(cache.probe("ns/b", "1", start.Add(clusterProbeInterval), probe).Message).To(Equal("4"), "another cluster")

		cache.forget("ns/a")
		Expect(cache.probe("ns/a", "2", start.Add(clusterProbeInterval), probe).Message).To(Equal("5"))
	})

	It("maps a kubeconfig Secret to the Clusters of its namespace that name it", func() {
		r := &ClusterReconciler{Client: fake.NewClientBuilder().WithScheme(scheme.Scheme).
			WithIndex(&deliveryv1alpha1.Cluster{}, deliveryv1alpha1.FieldKubeconfigSecretName, clusterKubeconfigSecret).
			WithObjects(
				kubeconfigCluster(testWorkspace, "eu", "eu-kubeconfig"),
				kubeconfigCluster(testWorkspace, "us", "us-kubeconfig"),
				kubeconfigCluster(testKokumiNamespace, "eu", "eu-kubeconfig"),
			).Build()}
		secret := &corev1.Secret{Name: "eu-kubeconfig", Namespace: testWorkspace}

		Expect(r.clustersOfSecret(ctx, secret)).To(ConsistOf(HaveField("NamespacedName", types.NamespacedName{Namespace: testWorkspace, Name: "eu"})))
	})
})
