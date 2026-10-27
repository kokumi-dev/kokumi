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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Order deletion", Label("lifecycle"), func() {
	Context("of an Order whose promoted Preparation Argo CD deploys", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("deletion")
			framework.ApplyExample(ns, "getting-started")
			preparation := framework.LatestPreparation(ns, "podinfo")
			framework.Promote(ns, "podinfo", preparation.Name)
			framework.WaitForDeployment(ns, "podinfo", preparation.Spec.Artifact.Digest)
		})

		It("deletes the Preparations and the Serving the Order owns", func() {
			_, err := framework.Kubectl("delete", "orders.delivery.kokumi.dev", "podinfo", "-n", ns)
			Expect(err).NotTo(HaveOccurred())

			Eventually(framework.Preparations(ns, "podinfo")).Should(BeEmpty())
			Eventually(func() error {
				_, err := framework.Serving(ns, "podinfo")()
				return err
			}).Should(Satisfy(apierrors.IsNotFound))
		})

		It("deletes the Argo CD Application of the Serving", func() {
			Eventually(func() error {
				_, err := framework.Application(ns, "podinfo")()
				return err
			}).Should(Satisfy(apierrors.IsNotFound))
		})
	})

	Context("of an Order whose Application someone else created by hand", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("foreign-app")
			name := framework.ApplicationKey(ns, "podinfo").Name
			framework.ApplyTestdata(framework.ArgoCDNamespace, "lifecycle/foreign-application.yaml",
				map[string]string{"NAMESPACE": ns, "NAME": name})
			DeferCleanup(func() {
				_, _ = framework.Kubectl("delete", "applications.argoproj.io", name,
					"-n", framework.ArgoCDNamespace, "--ignore-not-found")
			})

			framework.ApplyExample(ns, "getting-started")
			framework.Promote(ns, "podinfo", framework.LatestPreparation(ns, "podinfo").Name)
		})

		It("refuses to take the Application over", func() {
			Eventually(framework.Serving(ns, "podinfo")).Should(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploymentFailed))
			Expect(framework.Application(ns, "podinfo")()).To(HaveField("Object", HaveKeyWithValue("spec",
				HaveKeyWithValue("source", HaveKeyWithValue("repoURL", "https://github.com/stefanprodan/podinfo.git")))))
		})

		It("leaves the Application in place when the Order is deleted", func() {
			_, err := framework.Kubectl("delete", "orders.delivery.kokumi.dev", "podinfo", "-n", ns)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := framework.Serving(ns, "podinfo")()
				return err
			}).Should(Satisfy(apierrors.IsNotFound))

			Consistently(framework.Application(ns, "podinfo"), 10*time.Second).Should(Not(BeNil()), "the Application survives")
		})
	})
})

var _ = Describe("Manager restart", Label("lifecycle"), Serial, func() {
	Context("of a manager whose Servings deploy", Ordered, func() {
		var ns string
		var uid types.UID

		BeforeAll(func() {
			ns = framework.CreateNamespace("restart")
			framework.ApplyExample(ns, "getting-started")
			preparation := framework.LatestPreparation(ns, "podinfo")
			framework.Promote(ns, "podinfo", preparation.Name)
			framework.WaitForDeployment(ns, "podinfo", preparation.Spec.Artifact.Digest)
			app, err := framework.Application(ns, "podinfo")()
			Expect(err).NotTo(HaveOccurred())
			uid = app.GetUID()
		})

		It("keeps the Applications, while its watches of Argo CD's kinds start again", func() {
			const manager = "deployment/kokumi-controller-manager"
			_, err := framework.Kubectl("rollout", "restart", manager, "-n", framework.InstallNamespace)
			Expect(err).NotTo(HaveOccurred())
			_, err = framework.Kubectl("rollout", "status", manager, "-n", framework.InstallNamespace, "--timeout=3m")
			Expect(err).NotTo(HaveOccurred())

			Consistently(func() (types.UID, error) {
				app, err := framework.Application(ns, "podinfo")()
				if err != nil {
					return "", err
				}
				return app.GetUID(), nil
			}, 45*time.Second, time.Second).Should(Equal(uid), "the Application is neither deleted nor replaced")
			Expect(framework.Serving(ns, "podinfo")()).NotTo(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonArgoCDNotInstalled))
		})
	})
})
