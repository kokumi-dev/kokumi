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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Clusters", Label("clusters"), Serial, func() {
	var (
		fleet string
		spoke *framework.Spoke
	)

	Context("the multi-cluster example: a spoke Kind cluster and the cluster Kokumi runs in, in the Environment edge",
		Ordered, func() {
			const order = "podinfo-edge"
			// targetNamespace is the namespace the example's Product deploys
			// into on every cluster.
			const targetNamespace = "podinfo"
			var digest string

			BeforeAll(func() {
				// Registered before the namespace, so that it runs after the
				// namespace is gone with the Applications that deploy there:
				// Argo CD would create the namespace again.
				DeferCleanup(func() {
					if fleet == "" || spoke == nil {
						return
					}
					By("deleting the target namespace " + targetNamespace + " on both clusters")
					framework.WaitForNamespaceDeleted(fleet)
					namespace := &corev1.Namespace{Name: targetNamespace}
					Expect(client.IgnoreNotFound(framework.Client.Delete(context.Background(), namespace))).To(Succeed())
					Expect(client.IgnoreNotFound(spoke.Client.Delete(context.Background(), namespace))).To(Succeed())
				})
				spoke = framework.SpokeCluster()
				fleet = framework.CreateNamespace("fleet")
				framework.RegisterCluster(fleet, "spoke", spoke)
				framework.ApplyPlatform(fleet, "multi-cluster")
				framework.ApplyExample(fleet, "multi-cluster")
			})

			It("reports the spoke ready, with its server and Kubernetes version", func() {
				Eventually(framework.Cluster(fleet, "spoke"), framework.ClusterTimeout).Should(SatisfyAll(
					framework.HaveReady(metav1.ConditionTrue, deliveryv1alpha1.ReasonReady),
					HaveField("Status.Server", spoke.Server),
					HaveField("Status.KubernetesVersion", spoke.Version),
				))
			})

			It("keeps the Cluster of the cluster it runs in, in its own namespace", func() {
				Eventually(framework.Cluster(framework.InstallNamespace, "in-cluster"), framework.ClusterTimeout).Should(SatisfyAll(
					HaveField("Labels", HaveKeyWithValue(deliveryv1alpha1.LabelInCluster, "true")),
					framework.HaveReady(metav1.ConditionTrue, deliveryv1alpha1.ReasonReady),
				))
				Eventually(framework.Environment(framework.InstallNamespace, "local")).Should(SatisfyAll(
					framework.BeReady(), framework.HaveSelectedClusters(1, 1)))
			})

			It("registers the spoke with Argo CD, with a credential", func() {
				secret := &corev1.Secret{}
				Eventually(func() error {
					return framework.Client.Get(context.Background(), framework.ClusterSecretKey(spoke.Server), secret)
				}).Should(Succeed(), "Kokumi registers the spoke in a cluster Secret of Argo CD, named after its server")
				Expect(secret.Labels).To(SatisfyAll(
					HaveKeyWithValue("argocd.argoproj.io/secret-type", "cluster"),
					HaveKeyWithValue(deliveryv1alpha1.LabelCluster, "spoke"),
					HaveKeyWithValue(deliveryv1alpha1.LabelClusterNamespace, fleet),
				))
				Expect(string(secret.Data["server"])).To(Equal(spoke.Server))
				Expect(string(secret.Data["config"])).To(
					SatisfyAll(ContainSubstring(`"bearerToken":"`), ContainSubstring(`"caData":"`)),
					"the config holds a token and the CA")
			})

			It("counts the spoke, ready, in the Environment edge, which is ready", func() {
				Eventually(framework.Environment(fleet, "edge")).Should(SatisfyAll(
					framework.BeReady(), framework.HaveSelectedClusters(1, 1)))
			})

			It("deploys podinfo-edge to the spoke through an Application of its own, into the target namespace", func() {
				digest = framework.LatestPreparation(fleet, order).Spec.Artifact.Digest
				framework.WaitForDeploymentOn(fleet, order, "spoke", digest)
				app, err := framework.ApplicationOn(fleet, order, "spoke")()
				Expect(err).NotTo(HaveOccurred())
				Expect(app.Object).To(HaveKeyWithValue("spec", SatisfyAll(
					HaveKeyWithValue("project", "default"),
					HaveKeyWithValue("destination", map[string]any{
						framework.KeyServer: spoke.Server, framework.KeyNamespace: targetNamespace,
					}))))
				framework.ExpectNoApplication(fleet, order)
				Eventually(framework.Serving(fleet, order)).Should(HaveField("Status.Clusters.Selected", BeEquivalentTo(1)))
			})

			It("is deployed once the spoke runs it healthily", func() {
				framework.SyncHealthyOn(fleet, order, "spoke", digest)
				Eventually(framework.Serving(fleet, order), time.Minute).Should(SatisfyAll(
					framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed),
					framework.HaveDeploymentClusters(1, 1), framework.HaveTargets(),
					HaveField("Status.History", ContainElement(HaveField("DeploymentRecord", HavePrefix("sha256:")))),
				))
			})

			It("runs podinfo on the spoke", func() {
				if !framework.RealArgoCD() {
					Skip("only Argo CD deploying for real runs it: E2E_ARGOCD=real")
				}
				Eventually(func(g Gomega) {
					deployment := &appsv1.Deployment{}
					key := client.ObjectKey{Namespace: targetNamespace, Name: order}
					g.Expect(spoke.Client.Get(context.Background(), key, deployment)).To(Succeed())
					g.Expect(deployment.Status.AvailableReplicas).To(BeNumerically(">=", 1))
				}, 5*time.Minute).Should(Succeed(), "Argo CD runs podinfo on the spoke cluster")
			})

			It("signs a deployment record that names the spoke", func() {
				preparation := framework.LatestPreparation(fleet, order)
				predicates := framework.CosignVerifiedPredicates(
					framework.KitchenPublicKey(), framework.PreparationRef(preparation),
					"https://kokumi.dev/attestations/deployment/v1")
				Expect(predicates).To(ContainElement(HaveKeyWithValue("targets", ContainElement(SatisfyAll(
					HaveKeyWithValue("cluster", "spoke"),
					HaveKeyWithValue(framework.KeyServer, spoke.Server),
					HaveKeyWithValue(framework.KeyNamespace, targetNamespace))))))
			})

			It("deploys to the cluster Kokumi runs in, too, once the admin labels it into the Environment", func() {
				framework.LabelCluster(fleet, "in-cluster", "fleet", "edge")
				framework.WaitForDeployment(fleet, order, digest)
				Eventually(framework.Serving(fleet, order)).Should(HaveField("Status.Clusters.Selected", BeEquivalentTo(2)))

				framework.SyncHealthy(fleet, order, digest)
				Eventually(framework.Serving(fleet, order), time.Minute).Should(SatisfyAll(
					framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed),
					HaveField("Status.Conditions", ContainElement(HaveField("Message", ContainSubstring("to in-cluster, spoke")))),
					framework.HaveDeploymentClusters(2, 2), framework.HaveTargets(),
				))
			})

			It("stops deploying to a cluster that leaves the Environment, and keeps what runs there", func() {
				framework.LabelCluster(fleet, "in-cluster", "fleet", "")
				Eventually(func() error {
					_, err := framework.Application(fleet, order)()
					return err
				}).Should(Satisfy(apierrors.IsNotFound), "the Application of the cluster Kokumi runs in goes")
				Eventually(framework.Serving(fleet, order)).Should(framework.HaveDeploymentClusters(1, 1))
				_, err := framework.ApplicationOn(fleet, order, "spoke")()
				Expect(err).NotTo(HaveOccurred(), "the spoke keeps its Application")

				if framework.RealArgoCD() {
					Consistently(func() error {
						key := client.ObjectKey{Namespace: targetNamespace, Name: order}
						return framework.Client.Get(context.Background(), key, &appsv1.Deployment{})
					}, 20*time.Second).Should(Succeed(), "what runs on the cluster that left keeps running")
				}
			})

			It("tells a second namespace that registers the spoke again that this one registered its server", func() {
				ns := framework.CreateNamespace("second")
				framework.RegisterCluster(ns, "spoke", spoke)
				framework.ApplyPlatform(ns, "multi-cluster")

				registeredFirst := ContainSubstring("the Cluster " + fleet + "/spoke")
				Eventually(framework.Cluster(ns, "spoke"), framework.ClusterTimeout).Should(SatisfyAll(
					framework.HaveReady(metav1.ConditionFalse, deliveryv1alpha1.ReasonClusterConflict),
					HaveField("Status.Conditions", ContainElement(HaveField("Message", registeredFirst))),
				))
				secret := &corev1.Secret{}
				Expect(framework.Client.Get(context.Background(), framework.ClusterSecretKey(spoke.Server), secret)).To(Succeed())
				Expect(secret.Labels).To(HaveKeyWithValue(deliveryv1alpha1.LabelClusterNamespace, fleet),
					"Argo CD holds one credential per server, the first namespace's")
			})

			It("reports a spoke that rejects its credential, and keeps delivering what Argo CD can", func() {
				framework.RevokeCluster(spoke)
				Eventually(framework.Cluster(fleet, "spoke"), framework.ClusterTimeout).Should(
					framework.HaveReady(metav1.ConditionFalse, deliveryv1alpha1.ReasonUnauthorized))
				_, err := framework.ApplicationOn(fleet, order, "spoke")()
				Expect(err).NotTo(HaveOccurred(), "Kokumi keeps delivering what Argo CD can still deliver")
			})
		})
})
