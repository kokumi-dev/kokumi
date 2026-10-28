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
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

// spokeRequest is the request of the UI that registers a cluster reached at
// a server with a token, as the Cluster spoke of namespace.
func spokeRequest(namespace string) map[string]any {
	return map[string]any{
		framework.KeyNamespace: namespace, framework.KeyName: "spoke",
		framework.KeyServer: "https://spoke.example:6443", "token": "token",
	}
}

// The API server acts on behalf of the signed-in user: it impersonates the
// ServiceAccount the user's groups map to, so the UI can do no more than
// kubectl as that ServiceAccount.
var _ = Describe("API server", Label("server"), Serial, func() {
	Context("users who sign in through OIDC as members of kokumi-viewer and kokumi-editor", Ordered, func() {
		var ns string
		var server *framework.Server
		var viewer, editor, admin string

		order := func(name string) map[string]any {
			return map[string]any{
				framework.KeyNamespace: ns, framework.KeyName: name, "productRef": map[string]string{framework.KeyName: "podinfo"},
				"environmentRef": map[string]string{framework.KeyName: "local"}, "mode": "Manual",
			}
		}

		BeforeAll(func() {
			By("signing users in through Dex, as make oidc sets up for local development")
			framework.EnableOIDC()
			server = framework.ForwardServer()
			viewer = server.LoginOIDC("viewer@kokumi.dev", "password")
			editor = server.LoginOIDC("editor@kokumi.dev", "password")
			admin = server.LoginOIDC("admin@kokumi.dev", "password")

			ns = framework.CreateNamespace("server")
			framework.ApplyExample(ns, "getting-started")
		})

		It("lets the viewer read an Order", func() {
			resp, body := server.Do(http.MethodGet, "/api/v1/orders/"+ns+"/podinfo", viewer, nil)
			Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))
		})

		It("refuses the viewer to create an Order, as RBAC refuses the kokumi-viewer ServiceAccount", func() {
			resp, body := server.Do(http.MethodPost, "/api/v1/orders", viewer, order("from-viewer"))
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden), string(body))
		})

		It("lets the editor create an Order, which Kokumi renders", func() {
			resp, body := server.Do(http.MethodPost, "/api/v1/orders", editor, order("from-editor"))
			Expect(resp.StatusCode).To(Equal(http.StatusCreated), string(body))
			framework.LatestPreparation(ns, "from-editor")
		})

		It("registers a Cluster of the cluster Kokumi runs in for the admin, as labeled, and lets it delete it", func() {
			const name = "hub"
			labels := map[string]string{"fleet": name}
			hub := map[string]any{
				framework.KeyNamespace: ns, framework.KeyName: name, "connection": "InCluster", "labels": labels,
			}
			resp, body := server.Do(http.MethodPost, "/api/v1/clusters", editor, hub)
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden), "only the admin role registers clusters: %s", body)

			resp, body = server.Do(http.MethodPost, "/api/v1/clusters", admin, hub)
			Expect(resp.StatusCode).To(Equal(http.StatusCreated), string(body))
			By("reporting it ready, labeled as given: the Environment local of examples/local does not select it")
			Eventually(framework.Cluster(ns, name), framework.ClusterTimeout).Should(SatisfyAll(
				HaveField("Labels", Equal(labels)),
				framework.HaveReady(metav1.ConditionTrue, deliveryv1alpha1.ReasonReady),
			))

			resp, body = server.Do(http.MethodDelete, "/api/v1/clusters/"+ns+"/"+name, admin, nil)
			Expect(resp.StatusCode).To(Equal(http.StatusNoContent), string(body))
		})

		It("refuses the admin to store a kubeconfig where RBAC grants it no Secret access", func() {
			resp, body := server.Do(http.MethodPost, "/api/v1/clusters", admin, spokeRequest(ns))
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden), string(body))
		})

		It("stores the kubeconfig once the operator grants the admin kokumi-cluster-credentials in the namespace", func() {
			framework.GrantClusterCredentials(ns)
			Eventually(func(g Gomega) {
				resp, body := server.Do(http.MethodPost, "/api/v1/clusters", admin, spokeRequest(ns))
				g.Expect(resp.StatusCode).To(Equal(http.StatusCreated), string(body))
			}).Should(Succeed(), "RBAC grants the Secret access once the RoleBinding is in")
			Eventually(framework.Cluster(ns, "spoke")).Should(SatisfyAll(
				HaveField("Spec.Connection", deliveryv1alpha1.ClusterConnectionKubeconfig),
				HaveField("Spec.Kubeconfig.SecretRef.Name", "spoke-kubeconfig"),
			))
			secret := &corev1.Secret{}
			key := client.ObjectKey{Namespace: ns, Name: "spoke-kubeconfig"}
			Expect(framework.Client.Get(context.Background(), key, secret)).
				To(Succeed(), "the API server stores the kubeconfig next to the Cluster")
			Expect(secret.OwnerReferences).To(ContainElement(HaveField("Name", "spoke")), "the Secret goes with its Cluster")
		})
	})
})
