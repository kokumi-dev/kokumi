//go:build e2e

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

package e2e

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Clusters", Label("clusters"), func() {
	Context("the cluster Kokumi runs in", func() {
		It("is kept as the Cluster in-cluster in Kokumi's namespace, ready, with its server and version", func() {
			inCluster := framework.Cluster(framework.InstallNamespace, cluster.InCluster)
			Eventually(inCluster, framework.ClusterTimeout).Should(SatisfyAll(
				HaveField("Labels", HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true")),
				framework.HaveReady(metav1.ConditionTrue, deliveryv1alpha1.ReasonReady),
				HaveField("Status.Server", cluster.InClusterServer),
				HaveField("Status.KubernetesVersion", HavePrefix("v")),
			))
		})
	})

	Context("who may name a Secret in a Cluster", Ordered, func() {
		var ns string
		clusterFile := func() string { return framework.TestdataFile("clusters/cluster.yaml", nil) }

		BeforeAll(func() {
			ns = framework.CreateNamespace("cluster-secrets")
			framework.ApplyTestdata(ns, "clusters/kubeconfig-secret.yaml", nil)
		})

		It("refuses the admin, who may not read the Secrets of the namespace, a Cluster that names one", func() {
			Eventually(func(g Gomega) {
				out, err := framework.KubectlAs("kokumi-admin", "apply", "-n", ns, "-f", clusterFile())
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error() + out).To(ContainSubstring("A Cluster may only name a Secret its author may get"))
			}).Should(Succeed())
		})

		It("lets the admin create it once the operator grants it kokumi-cluster-credentials in the namespace", func() {
			framework.GrantClusterCredentials(ns)
			Eventually(func() error {
				_, err := framework.KubectlAs("kokumi-admin", "apply", "-n", ns, "-f", clusterFile())
				return err
			}).Should(Succeed(), "RBAC grants the Secret access once the RoleBinding is in")
			Eventually(framework.Cluster(ns, "spoke"), framework.ClusterTimeout).Should(SatisfyAll(
				HaveField("Status.Server", "https://spoke.example:6443"),
				framework.HaveReady(metav1.ConditionFalse, deliveryv1alpha1.ReasonUnreachable),
			), "Kokumi reads the kubeconfig and finds nothing at its server")
		})

		It("lets the admin relabel the Cluster once it may no longer read its Secret", func() {
			_, err := framework.Kubectl("delete", "rolebinding", "kokumi-cluster-credentials", "-n", ns)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() string {
				out, _ := framework.KubectlAs("kokumi-admin", "auth", "can-i", "get", "secrets/spoke-kubeconfig", "-n", ns)
				return strings.TrimSpace(out)
			}).Should(Equal("no"), "RBAC no longer grants the Secret")

			_, err = framework.KubectlAs("kokumi-admin",
				"label", "clusters.delivery.kokumi.dev", "spoke", "-n", ns, "stage=prod", "--overwrite")
			Expect(err).NotTo(HaveOccurred(), "an update that keeps the Secret needs no access to it")

			out, err := framework.KubectlAs("kokumi-admin", "patch", "clusters.delivery.kokumi.dev", "spoke", "-n", ns,
				"--type=merge", "-p", `{"spec":{"kubeconfig":{"secretRef":{"key":"admin"}}}}`)
			Expect(err).To(HaveOccurred(), "another key of the Secret may hold another credential")
			Expect(out + err.Error()).To(ContainSubstring("A Cluster may only name a Secret its author may get"))
		})
	})
})
