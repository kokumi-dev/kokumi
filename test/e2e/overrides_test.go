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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Overrides", Label("overrides"), func() {
	Context("the getting-started Product, which lets Orders set ui.message and replicaCount only", Ordered, func() {
		var ns string
		var first *deliveryv1alpha1.Preparation

		BeforeAll(func() {
			ns = framework.CreateNamespace("overrides")
			framework.ApplyExample(ns, "getting-started")
			first = framework.LatestPreparation(ns, "podinfo")
		})

		It("refuses to render an Order that sets another value, and names the value", func() {
			framework.Update(ns, "podinfo", func(o *deliveryv1alpha1.Order) {
				o.Spec.Values = framework.Values(`{"ui":{"message":"Hello","color":"#000000"}}`)
			})
			Eventually(framework.Order(ns, "podinfo")).Should(SatisfyAll(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessingFailed),
				HaveField("Status.Conditions", ContainElement(HaveField("Message", ContainSubstring("ui.color")))),
			))
			Expect(framework.Preparations(ns, "podinfo")()).To(HaveLen(1), "nothing new is rendered")
		})

		It("renders again once the Order keeps to the allowed values", func() {
			framework.Update(ns, "podinfo", func(o *deliveryv1alpha1.Order) {
				o.Spec.Values = framework.Values(`{"ui":{"message":"Hello"}}`)
			})
			framework.LatestPreparation(ns, "podinfo", first.Name)
		})
	})

	Context("the manifest-bundle Product, which lets Orders scale the webhook only", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("overrides")
			framework.ApplyExample(ns, "manifest-bundle")
			framework.LatestPreparation(ns, "external-secrets")
		})

		It("refuses to render an Order that patches another field", func() {
			framework.Update(ns, "external-secrets", func(o *deliveryv1alpha1.Order) {
				o.Spec.Patches = append(o.Spec.Patches, deliveryv1alpha1.Patch{
					Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "external-secrets-webhook"},
					Set:    map[string]string{".spec.template.spec.hostNetwork": "true"},
				})
			})
			Eventually(framework.Order(ns, "external-secrets")).Should(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessingFailed))
		})
	})
})
