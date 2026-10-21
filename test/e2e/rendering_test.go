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
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Rendering", Label("rendering"), func() {
	Context("a Helm chart: the getting-started example", Ordered, func() {
		var ns string
		var first *deliveryv1alpha1.Preparation

		BeforeAll(func() {
			ns = framework.CreateNamespace("helm")
			framework.ApplyExample(ns, "getting-started")
		})

		It("renders a Ready Preparation of the Order", func() {
			first = framework.LatestPreparation(ns, "podinfo")
			Eventually(framework.Order(ns, "podinfo")).Should(framework.BeReady())
		})

		It("pushes the chart, rendered with the Order's values over the Product's, as one manifest.yaml", func() {
			dir := framework.PullPreparation(first)
			Expect(fileNames(dir)).To(ConsistOf("manifest.yaml"))

			deployment := framework.RenderedObject(dir, "Deployment", "podinfo")
			Expect(deployment.GetNamespace()).To(Equal(ns), "the release namespace defaults to the Order's")
			Expect(framework.Field(deployment, "spec", "replicas")).To(BeEquivalentTo(2), "the Order's replicaCount")
			Expect(framework.ContainerEnv(deployment, "PODINFO_UI_MESSAGE")).To(Equal("Hello from Kokumi"),
				"the Order's ui.message")
			Expect(framework.ContainerEnv(deployment, "PODINFO_UI_COLOR")).To(Equal("#EF6461"), "the Product's ui.color")
		})

		It("renders a new Preparation, which names the previous one as its parent, when the Order changes", func() {
			framework.Update(ns, "podinfo", func(o *deliveryv1alpha1.Order) {
				o.Spec.Values = framework.Values(`{"ui":{"message":"Hello again"},"replicaCount":2}`)
			})
			second := framework.LatestPreparation(ns, "podinfo", first.Name)
			Expect(second.Spec.ParentDigest).To(Equal(first.Spec.Artifact.Digest))
			Expect(second.Spec.Artifact.Digest).NotTo(Equal(first.Spec.Artifact.Digest))

			deployment := framework.RenderedObject(framework.PullPreparation(second), "Deployment", "podinfo")
			Expect(framework.ContainerEnv(deployment, "PODINFO_UI_MESSAGE")).To(Equal("Hello again"))
		})
	})

	Context("pre-rendered manifests: the manifest-bundle example", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("manifests")
			framework.ApplyExample(ns, "manifest-bundle")
		})

		It("pushes the manifests, patched by the Order, as one manifest.yaml", func() {
			preparation := framework.LatestPreparation(ns, "external-secrets")
			dir := framework.PullPreparation(preparation)
			Expect(fileNames(dir)).To(ConsistOf("manifest.yaml"))

			webhook := framework.RenderedObject(dir, "Deployment", "external-secrets-webhook")
			Expect(framework.Field(webhook, "spec", "replicas")).To(BeEquivalentTo(2))
		})
	})

	Context("manifests in several files: the multi-file-manifests example", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("multi-file")
			framework.ApplyExample(ns, "multi-file-manifests")
		})

		It("keeps the files apart, and patches the Deployment in the file that holds it", func() {
			preparation := framework.LatestPreparation(ns, "external-dns")
			dir := framework.PullPreparation(preparation)
			Expect(fileNames(dir)).To(ContainElement("namespace.yaml"))
			Expect(fileNames(dir)).NotTo(ContainElement("manifest.yaml"))

			deployment := framework.RenderedObject(dir, "Deployment", "external-dns")
			Expect(framework.Field(deployment, "spec", "replicas")).To(BeEquivalentTo(2))
		})
	})

	Context("a Kustomize source: the kustomize example", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("kustomize")
			framework.ApplyExample(ns, "kustomize")
		})

		It("keeps the kustomization for Argo CD to build, and patches the Deployment", func() {
			preparation := framework.LatestPreparation(ns, "podinfo-kustomize")
			dir := framework.PullPreparation(preparation)
			Expect(fileNames(dir)).To(ContainElement("kustomization.yaml"))

			deployment := framework.RenderedObject(dir, "Deployment", "podinfo")
			Expect(framework.Field(deployment, "spec", "replicas")).To(BeEquivalentTo(3))
		})
	})
})

// fileNames returns the names of the files at the root of dir.
func fileNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	Expect(err).NotTo(HaveOccurred())
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, filepath.Base(entry.Name()))
		}
	}
	return names
}
