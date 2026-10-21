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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

var _ = Describe("Images", Label("images"), func() {
	Context("a chart whose image is copied into the bundled registry: the image-vendoring example", Ordered, func() {
		var ns, vendored string
		var preparation *deliveryv1alpha1.Preparation

		BeforeAll(func() {
			ns = framework.CreateNamespace("vendoring")
			framework.ApplyExample(ns, "image-vendoring")
			preparation = framework.LatestPreparation(ns, "podinfo-vendored")
		})

		It("records the image with its upstream reference, its digest and its vendored reference", func() {
			Expect(preparation.Spec.Images).To(HaveExactElements(SatisfyAll(
				HaveField("Ref", "ghcr.io/stefanprodan/podinfo:6.14.1"),
				HaveField("Digest", HavePrefix("sha256:")),
			)))
			vendored = fmt.Sprintf("%s/%s/vendor/podinfo-vendored/images/ghcr.io/stefanprodan/podinfo:6.14.1@%s",
				framework.InClusterRegistry, ns, preparation.Spec.Images[0].Digest)
			Expect(preparation.Spec.Images[0].Vendored).To(Equal(vendored),
				"the image keeps its name and tag, in a repository of its own below the Product's vendor location")
		})

		It("copies the image, with all its platforms, under its tag", func() {
			ctx := context.Background()
			copied := framework.Ref(framework.Local(strings.Split(vendored, "@")[0]))
			desc, err := framework.Registry().Resolve(ctx, copied)
			Expect(err).NotTo(HaveOccurred())
			Expect(desc.Digest).To(Equal(preparation.Spec.Images[0].Digest))

			index, err := framework.Registry().FetchManifest(ctx, copied)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(index.Manifests)).To(BeNumerically(">", 1), "an image index keeps all platforms")
		})

		It("runs the vendored reference in the rendered manifests", func() {
			deployment := framework.RenderedObject(framework.PullPreparation(preparation), "Deployment", "podinfo-vendored")
			Expect(framework.Field(deployment, "spec", "template", "spec", "containers")).To(
				ContainElement(HaveKeyWithValue("image", vendored)))
		})

		It("lets the nodes pull the vendored image from the bundled registry", func() {
			framework.ApplyTestdata(ns, "images/vendored-pod.yaml", map[string]string{"IMAGE": vendored})
			_, err := framework.Kubectl("wait", "pod/vendored", "-n", ns, "--for=condition=Ready", "--timeout=3m")
			Expect(err).NotTo(HaveOccurred())
		})

		It("keeps the vendored digest when a Product change renders the Order again", func() {
			framework.Update(ns, "podinfo-vendored", func(p *deliveryv1alpha1.Product) {
				p.Spec.Render.Helm.Values = framework.Values(`{"ui":{"message":"changed"}}`)
			})
			next := framework.LatestPreparation(ns, "podinfo-vendored", preparation.Name)
			Expect(next.Spec.Images).To(HaveExactElements(HaveField("Vendored", vendored)))
		})
	})
})
