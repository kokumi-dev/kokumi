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
	"runtime"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
)

// vulnerableImage is alpine 3.14.0, pinned: its apk-tools and OpenSSL have
// known Critical vulnerabilities that later releases fix
// (CVE-2021-36159, CVE-2021-3711). It has Linux platforms only. The specs
// copy only the one of the architecture they run on: mirror.gcr.io caches
// Docker Hub, and the layers of the platforms hardly anyone pulls are the
// likeliest to fail to download.
const vulnerableImage = "mirror.gcr.io/library/alpine@" +
	"sha256:adab3844f497ab9171f070d4cae4114b5aec565ac772e2f2579405b78be67c96"

// The specs scan for real: the scanner downloads the Trivy database from
// mirror.gcr.io, and findings change as the database does. They assert what
// a known-vulnerable image always has, and of the example only that its
// findings are reported and checked.
var _ = Describe("Vulnerability scanning", Label("scanning", "slow"), Serial, Ordered, func() {
	BeforeAll(func() {
		By("enabling scanning, as a cluster admin does with the vulnerability-scanning example's Kitchen patch")
		framework.PatchKitchen("examples/vulnerability-scanning/kitchen-patch.yaml")
	})

	Context("the vulnerability-scanning example", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("scanning")
			framework.ApplyExample(ns, "vulnerability-scanning")
		})

		It("scans the vendored image, and reports the findings on the Product", func() {
			Eventually(framework.Product(ns, "podinfo-scanned"), framework.ScanTimeout, 5*time.Second).Should(SatisfyAll(
				framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeScanned, deliveryv1alpha1.ReasonScanned),
				HaveField("Status.Vulnerabilities.Images", HaveExactElements(HaveField("Report", HavePrefix("sha256:")))),
			))
		})

		It("checks the findings before the Order deploys", func() {
			Eventually(framework.Serving(ns, "podinfo-scanned")).Should(HaveField("Status.Conditions", ContainElement(SatisfyAll(
				HaveField("Type", deliveryv1alpha1.ConditionTypeChecksPassed),
				HaveField("Reason", BeElementOf(deliveryv1alpha1.ReasonPassed, deliveryv1alpha1.ReasonVulnerabilitiesFound)),
			))))
		})
	})

	Context("two Orders of a Product that vendors an image with known Critical vulnerabilities, "+
		"one with a vulnerability check", Ordered, func() {
		var ns, imageDigest string
		var scan deliveryv1alpha1.ImageScan

		BeforeAll(func() {
			ctx := context.Background()
			ns = framework.CreateNamespace("vulnerable")
			upstream := fmt.Sprintf("e2e/%s/upstream/alpine:3.14.0", ns)

			By("copying the image to the bundled registry, with an unsigned SBOM that lists no packages")
			platform := &v1.Platform{OS: "linux", Architecture: runtime.GOARCH}
			Eventually(func() error {
				return crane.Copy(vulnerableImage, framework.RegistryHost()+"/"+upstream,
					crane.Insecure, crane.WithPlatform(platform))
			}, time.Minute, 10*time.Second).Should(Succeed(), "Failed to copy %s from the mirror", vulnerableImage)
			upstreamRef := framework.Ref(framework.RegistryHost() + "/" + upstream)
			desc, err := framework.Registry().Resolve(ctx, upstreamRef)
			Expect(err).NotTo(HaveOccurred())
			upstreamRef.Tag, upstreamRef.Digest = "", desc.Digest
			_, err = framework.Registry().PushReferrer(ctx, upstreamRef, oci.ReferrerArtifact{
				ArtifactType: "application/vnd.cyclonedx+json",
				MediaType:    "application/vnd.cyclonedx+json",
				Payload:      []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","components":[]}`),
			})
			Expect(err).NotTo(HaveOccurred())

			By("pushing a source whose Pod runs the image, and applying the Product and its two Orders")
			source := fmt.Sprintf("e2e/%s/source", ns)
			framework.PushSource(source, "scanning/source.yaml",
				map[string]string{"IMAGE": framework.InClusterRegistry + "/" + upstream})
			framework.ApplyTestdata(ns, "scanning/vulnerable-product.yaml", nil)
		})

		It("renders a Ready Preparation per Order, both running the same vendored image", func() {
			unchecked := framework.LatestPreparation(ns, "unchecked")
			checked := framework.LatestPreparation(ns, "checked")
			Expect(unchecked.Spec.Images).To(HaveLen(1))
			imageDigest = unchecked.Spec.Images[0].Digest
			Expect(checked.Spec.Images).To(HaveExactElements(HaveField("Digest", imageDigest)))
		})

		It("scans the image once, from an SBOM it generates instead of the unsigned one it found", func() {
			Eventually(func(g Gomega) {
				product, err := framework.Product(ns, "vulnerable")()
				g.Expect(err).NotTo(HaveOccurred())
				// Scanned also holds while no vendored image is counted as in
				// use yet, without status.vulnerabilities: match the path,
				// which tolerates the nil pointer, before reading it.
				g.Expect(product).To(SatisfyAll(
					framework.HaveConditionReason(deliveryv1alpha1.ConditionTypeScanned, deliveryv1alpha1.ReasonScanned),
					HaveField("Status.Vulnerabilities.Images", HaveExactElements(SatisfyAll(
						HaveField("Digest", imageDigest),
						HaveField("SBOM.Origin", deliveryv1alpha1.SBOMOriginGenerated),
						HaveField("Summary.Critical", BeNumerically(">", 0)),
					))),
				), "one scan for both Orders finds the known Critical vulnerabilities")
				scan = product.Status.Vulnerabilities.Images[0]
			}, framework.ScanTimeout, 5*time.Second).Should(Succeed())
		})

		It("attaches the report to the image as an attestation that stock cosign verifies", func() {
			vendored := framework.Ref(framework.Local(scan.Repository) + "@" + imageDigest)
			referrers, err := framework.Registry().ListReferrers(context.Background(), vendored, oci.SigstoreBundleMediaType)
			Expect(err).NotTo(HaveOccurred())
			var reports []string
			for _, r := range referrers {
				if r.Annotations[oci.AnnotationBundlePredicateType] == vulnerability.PredicateType {
					reports = append(reports, r.Digest)
				}
			}
			Expect(reports).To(ConsistOf(scan.Report), "two Orders, one report")
			Expect(framework.CosignVerifyAttestation(framework.KitchenPublicKey(), vendored.String(), "vuln")).To(Succeed())
		})

		It("holds the Preparation of the Order with the check, and deploys nothing", func() {
			Eventually(framework.Serving(ns, "checked"), 5*time.Minute).Should(SatisfyAll(
				framework.HaveConditionReason(
					deliveryv1alpha1.ConditionTypeChecksPassed, deliveryv1alpha1.ReasonVulnerabilitiesFound),
				HaveField("Status.History", BeEmpty()),
			))
			framework.ExpectNoApplication(ns, "checked")
		})
	})

	Context("the kokumi-scanner component", Ordered, func() {
		var ns string

		BeforeAll(func() {
			ns = framework.CreateNamespace("scanner")
		})

		It("refuses connections from Pods other than the controller", func() {
			framework.ApplyTestdata(ns, "scanning/curl-scanner.yaml", nil)
			Eventually(func(g Gomega) {
				out, err := framework.Kubectl("get", "pod", "curl-scanner", "-n", ns, "-o", "jsonpath={.status.phase}")
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Failed"), "the NetworkPolicy drops the connection")
			}, 2*time.Minute).Should(Succeed())
		})
	})
})
