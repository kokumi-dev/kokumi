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
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Target namespace", Label("target-namespace"), func() {
	Context("the target-namespace example: a Product that deploys into storefront, and an Order that picks its own",
		Ordered, func() {
			const (
				// review is the Order that picks its own target namespace.
				review = "storefront-review"
				// moved is the target namespace review moves to.
				moved = "storefront-moved"
			)
			// orders are the example's Orders and the namespaces they deploy
			// into: the Product's, and the one the Order picks.
			orders := []struct{ name, target string }{
				{name: "storefront", target: "storefront"},
				{name: review, target: review},
			}
			// deployment names the Deployment podinfo's chart renders for the
			// release of order: the release name, followed by the chart's.
			deployment := func(order string) string { return order + "-podinfo" }
			var ns string

			BeforeAll(func() {
				// Registered before the namespace, so that it runs after the
				// namespace is gone with the Applications that deploy there:
				// Argo CD would create the target namespaces again.
				DeferCleanup(func() {
					if ns == "" {
						return
					}
					framework.WaitForNamespaceDeleted(ns)
					for _, name := range []string{orders[0].target, orders[1].target, moved} {
						namespace := &corev1.Namespace{Name: name}
						Expect(client.IgnoreNotFound(framework.Client.Delete(context.Background(), namespace))).To(Succeed())
					}
				})
				ns = framework.CreateNamespace("target-namespace")
				framework.ApplyExample(ns, "target-namespace")
			})

			It("renders each Order into its target namespace, the Product's or its own", func() {
				for _, o := range orders {
					preparation := framework.LatestPreparation(ns, o.name)
					Expect(preparation.Spec.TargetNamespace).To(Equal(o.target), o.name)

					rendered := framework.RenderedObject(framework.PullPreparation(preparation), "Deployment", deployment(o.name))
					Expect(rendered.GetNamespace()).To(Equal(o.target),
						"Helm renders the release of %s into its target namespace", o.name)
				}
			})

			It("deploys each Order through an Application into its target namespace", func() {
				for _, o := range orders {
					digest := framework.LatestPreparation(ns, o.name).Spec.Artifact.Digest
					framework.WaitForDeployment(ns, o.name, digest)

					app, err := framework.Application(ns, o.name)()
					Expect(err).NotTo(HaveOccurred())
					Expect(app.Object["spec"]).To(HaveKeyWithValue("destination", HaveKeyWithValue("namespace", o.target)), o.name)

					framework.SyncHealthy(ns, o.name, digest)
					Eventually(framework.Serving(ns, o.name)).Should(
						framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeployed))
				}
			})

			It("runs each workload in its target namespace", func() {
				if !framework.RealArgoCD() {
					Skip("only Argo CD deploying for real runs it: E2E_ARGOCD=real")
				}
				for _, o := range orders {
					Eventually(func(g Gomega) {
						d := &appsv1.Deployment{}
						key := client.ObjectKey{Namespace: o.target, Name: deployment(o.name)}
						g.Expect(framework.Client.Get(context.Background(), key, d)).To(Succeed())
						g.Expect(d.Status.AvailableReplicas).To(BeNumerically(">=", 1))
					}, 5*time.Minute).Should(Succeed(), "Argo CD runs %s in %s", o.name, o.target)
				}
			})

			It("renders a new Preparation for a changed target namespace, and moves the workload there", func() {
				first := framework.LatestPreparation(ns, review)
				framework.Update(ns, review, func(o *deliveryv1alpha1.Order) { o.Spec.TargetNamespace = moved })

				second := framework.LatestPreparation(ns, review, first.Name)
				Expect(second.Spec.TargetNamespace).To(Equal(moved))
				framework.WaitForDeployment(ns, review, second.Spec.Artifact.Digest)
				app, err := framework.Application(ns, review)()
				Expect(err).NotTo(HaveOccurred())
				Expect(app.Object["spec"]).To(HaveKeyWithValue("destination", HaveKeyWithValue("namespace", moved)))

				if !framework.RealArgoCD() {
					return
				}
				key := func(namespace string) client.ObjectKey {
					return client.ObjectKey{Namespace: namespace, Name: deployment(review)}
				}
				Eventually(func() error {
					return framework.Client.Get(context.Background(), key(moved), &appsv1.Deployment{})
				}, 5*time.Minute).Should(Succeed(), "Argo CD deploys into the new target namespace")
				Eventually(func() error {
					return framework.Client.Get(context.Background(), key(review), &appsv1.Deployment{})
				}, 5*time.Minute).Should(Satisfy(apierrors.IsNotFound), "Argo CD prunes the workload in the old target namespace")
			})
		})
})
