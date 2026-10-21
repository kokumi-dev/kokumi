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
			framework.WaitForDeployment("podinfo", preparation.Spec.Artifact.Digest)
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
				_, err := framework.Application("podinfo")()
				return err
			}).Should(Satisfy(apierrors.IsNotFound))
		})
	})

	Context("of an Order named like an Argo CD Application kokumi did not create", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("foreign-app")
			framework.ApplyTestdata(framework.ArgoCDNamespace, "lifecycle/foreign-application.yaml",
				map[string]string{"NAMESPACE": ns})
			DeferCleanup(func() {
				_, _ = framework.Kubectl("delete", "applications.argoproj.io", "podinfo",
					"-n", framework.ArgoCDNamespace, "--ignore-not-found")
			})

			framework.ApplyExample(ns, "getting-started")
			framework.Promote(ns, "podinfo", framework.LatestPreparation(ns, "podinfo").Name)
		})

		It("refuses to take the Application over", func() {
			Eventually(framework.Serving(ns, "podinfo")).Should(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonDeploymentFailed))
			Expect(framework.Application("podinfo")()).To(HaveField("Object", HaveKeyWithValue("spec",
				HaveKeyWithValue("source", HaveKeyWithValue("repoURL", "https://github.com/stefanprodan/podinfo.git")))))
		})

		It("leaves the Application in place when the Order is deleted", func() {
			_, err := framework.Kubectl("delete", "orders.delivery.kokumi.dev", "podinfo", "-n", ns)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				_, err := framework.Serving(ns, "podinfo")()
				return err
			}).Should(Satisfy(apierrors.IsNotFound))

			Consistently(framework.Application("podinfo"), 10*time.Second).Should(Not(BeNil()), "the Application survives")
		})
	})
})
