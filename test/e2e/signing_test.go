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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Signing", Label("signing"), func() {
	Context("a Preparation of the getting-started example", Ordered, func() {
		var ns, ref, key string

		BeforeAll(func() {
			ns = framework.CreateNamespace("signing")
			framework.ApplyExample(ns, "getting-started")
			key = framework.KitchenPublicKey()
		})

		It("is signed, and has its provenance attached, before it is Ready", func() {
			preparation := framework.LatestPreparation(ns, "podinfo")
			Expect(preparation).To(SatisfyAll(
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeSigned, metav1.ConditionTrue),
				framework.HaveCondition(deliveryv1alpha1.ConditionTypeProvenanceAttached, metav1.ConditionTrue),
			))
			ref = framework.PreparationRef(preparation)
		})

		It("has a signature that stock cosign verifies against the public key the Kitchen publishes", func() {
			Expect(framework.CosignVerify(key, ref)).To(Succeed())
		})

		It("has SLSA provenance that stock cosign verifies", func() {
			Expect(framework.CosignVerifyAttestation(key, ref, "slsaprovenance1")).To(Succeed())
		})

		It("does not verify against a key kokumi does not hold", func() {
			Expect(framework.CosignVerify(framework.GenerateCosignKey(), ref)).NotTo(Succeed())
		})
	})
})
