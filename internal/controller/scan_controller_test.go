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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/sbom"
	"github.com/kokumi-dev/kokumi/internal/scanner"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
)

const (
	scanNamespace        = "scan-test"
	scanInstallNamespace = "scan-system"
	scanProductName      = "scanned"
	scanOrderName        = "scanned-prod"
	scanPreparationName  = "scanned-prod-0123456789ab"
	scanImageDigest      = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	scanAMD64Digest      = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	scanARM64Digest      = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	scanVendoredRepo     = "kokumi-registry.kokumi.svc.cluster.local:5000/scan-test/vendor/scanned/images/docker.io/library/nginx"
	scanPlatform         = "linux/arm64"
	scanGeneratedSBOM    = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[{"name":"curl"}]}`
)

// fakeImageScanner records scan requests and answers them with the report
// of the vulnerability package's tests.
type fakeImageScanner struct {
	mu       sync.Mutex
	requests []scanner.Request
	report   []byte
	err      error
	built    time.Time
}

func (f *fakeImageScanner) Scan(_ context.Context, req scanner.Request) (*scanner.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	resp := &scanner.Response{
		Scanner:  scanner.ScannerInfo{Name: "trivy", Version: "0.71.0"},
		Database: scanner.DatabaseInfo{Ref: req.Database.Ref, UpdatedAt: f.built},
	}
	// Like Trivy's, each report records when it was scanned.
	var report map[string]any
	if err := json.Unmarshal(f.report, &report); err != nil {
		return nil, err
	}
	report["metadata"] = map[string]any{"scanStartedOn": time.Date(2026, 10, 19, 12, len(f.requests), 0, 0, time.UTC)}
	scanned, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	for _, img := range req.Images {
		result := scanner.Result{Ref: img.Ref, Report: scanned}
		if len(img.SBOM) == 0 {
			result.SBOM = json.RawMessage(scanGeneratedSBOM)
		}
		resp.Images = append(resp.Images, result)
	}
	return resp, nil
}

// laggingProductCache serves product in place of the Product of the same name, as
// a cache that has not seen its latest version yet.
type laggingProductCache struct {
	client.Client
	product *deliveryv1alpha1.Product
}

func (c laggingProductCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if p, ok := obj.(*deliveryv1alpha1.Product); ok && key == client.ObjectKeyFromObject(c.product) {
		c.product.DeepCopyInto(p)
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

var _ = Describe("Scan Controller", func() {
	var (
		registry   *oci.FakeClient
		imageScans *fakeImageScanner
		now        time.Time
		reconciler *ScanReconciler
	)
	productKey := types.NamespacedName{Namespace: scanNamespace, Name: scanProductName}

	setScanning := func(scanning *deliveryv1alpha1.KitchenScanning) {
		kitchen := &deliveryv1alpha1.Kitchen{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: scanInstallNamespace, Name: deliveryv1alpha1.DefaultKitchenName}, kitchen)).To(Succeed())
		kitchen.Spec.Scanning = scanning
		Expect(k8sClient.Update(ctx, kitchen)).To(Succeed())
	}

	reconcileProduct := func() ctrl.Result {
		result, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: productKey})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	getProduct := func() *deliveryv1alpha1.Product {
		product := &deliveryv1alpha1.Product{}
		Expect(k8sClient.Get(ctx, productKey, product)).To(Succeed())
		return product
	}

	BeforeEach(func() {
		for _, ns := range []string{scanNamespace, scanInstallNamespace} {
			if err := k8sClient.Create(ctx, &corev1.Namespace{Name: ns}); err != nil && !apierrors.IsAlreadyExists(err) {
				Expect(err).NotTo(HaveOccurred())
			}
		}

		report, err := os.ReadFile(filepath.Join("..", "vulnerability", "testdata", "report.json"))
		Expect(err).NotTo(HaveOccurred())

		now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
		registry = oci.NewFakeClient(nil)
		registry.Manifests = map[string]oci.Manifest{
			scanImageDigest: {
				Descriptor: oci.Descriptor{MediaType: ocispec.MediaTypeImageIndex, Digest: scanImageDigest},
				Manifests: []oci.Descriptor{
					{MediaType: ocispec.MediaTypeImageManifest, Digest: scanAMD64Digest, Platform: "linux/amd64"},
					{MediaType: ocispec.MediaTypeImageManifest, Digest: scanARM64Digest, Platform: scanPlatform},
				},
			},
		}
		imageScans = &fakeImageScanner{report: report, built: now.Add(-time.Hour)}
		reconciler = &ScanReconciler{
			Client:           k8sClient,
			APIReader:        k8sClient,
			Scheme:           k8sClient.Scheme(),
			Registries:       fakeRegistries(registry),
			Signing:          testSigning,
			Scanner:          imageScans,
			InstallNamespace: scanInstallNamespace,
			Platform:         scanPlatform,
			now:              func() time.Time { return now },
		}

		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Kitchen{
			Name:      deliveryv1alpha1.DefaultKitchenName,
			Namespace: scanInstallNamespace,
			Spec:      deliveryv1alpha1.KitchenSpec{Scanning: &deliveryv1alpha1.KitchenScanning{}},
		})).To(Succeed())

		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Product{
			Name:      scanProductName,
			Namespace: scanNamespace,
			Spec: deliveryv1alpha1.ProductSpec{
				Source: deliveryv1alpha1.OCISource{URL: "oci://ghcr.io/org/app", Version: testVersion},
				Vendor: &deliveryv1alpha1.OCIDestination{},
				Images: deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyVendor},
			},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Order{
			Name:      scanOrderName,
			Namespace: scanNamespace,
			Spec:      deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: scanProductName}},
		})).To(Succeed())

		prep := &deliveryv1alpha1.Preparation{
			Name:      scanPreparationName,
			Namespace: scanNamespace,
			Spec: deliveryv1alpha1.PreparationSpec{
				TargetNamespace: testNamespace,
				OrderName:       scanOrderName,
				Source:          deliveryv1alpha1.OrderSource{URL: "oci://ghcr.io/org/app", BaseDigest: oci.FakeDigest},
				Renderer:        deliveryv1alpha1.Renderer{Type: deliveryv1alpha1.RenderTypeManifest, Version: testRendererVersion, Builder: testBuilderID},
				ConfigHash:      testConfigHash,
				ProductRevision: testProductRevision,
				Artifact: deliveryv1alpha1.Artifact{
					OCIRef: "oci://kokumi-registry.kokumi.svc.cluster.local:5000/scan-test/scanned-prod@" + oci.FakeDigest,
					Digest: oci.FakeDigest,
				},
				Images: []deliveryv1alpha1.PreparationImage{{
					Ref: "nginx:1.27", Name: "docker.io/library/nginx", Digest: scanImageDigest,
					MediaType: ocispec.MediaTypeImageIndex,
					Vendored:  scanVendoredRepo + ":1.27@" + scanImageDigest,
				}},
			},
		}
		Expect(k8sClient.Create(ctx, prep)).To(Succeed())
		apimeta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason,
		})
		Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())
	})

	AfterEach(func() {
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Preparation{Name: scanPreparationName, Namespace: scanNamespace})
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Order{Name: scanOrderName, Namespace: scanNamespace})
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Product{Name: scanProductName, Namespace: scanNamespace})
		_ = k8sClient.Delete(ctx, &deliveryv1alpha1.Kitchen{Name: deliveryv1alpha1.DefaultKitchenName, Namespace: scanInstallNamespace})
	})

	It("scans each vendored image in use once, and attaches the signed results to it", func() {
		result := reconcileProduct()

		Expect(imageScans.requests).To(HaveLen(1))
		req := imageScans.requests[0]
		Expect(req.Database.Ref).To(Equal("oci://mirror.gcr.io/aquasec/trivy-db@"+oci.FakeDigest), "the default database, by digest")
		Expect(req.JavaDatabase).NotTo(BeNil())
		Expect(req.JavaDatabase.Ref).To(Equal("oci://mirror.gcr.io/aquasec/trivy-java-db@" + oci.FakeDigest))
		Expect(req.Registry.PlainHTTP).To(BeTrue(), "the bundled registry")
		Expect(req.Images).To(Equal([]scanner.Image{{Ref: "oci://" + scanVendoredRepo + "@" + scanImageDigest, Platform: scanPlatform}}),
			"no SBOM to reuse, so the scanner generates one")

		product := getProduct()
		Expect(product.Status.Vulnerabilities).NotTo(BeNil())
		Expect(product.Status.Vulnerabilities.Scanner.Name).To(Equal("trivy"))
		Expect(product.Status.Vulnerabilities.Scanner.Database).To(Equal(req.Database.Ref))
		Expect(product.Status.Vulnerabilities.Images).To(HaveLen(1))
		scan := product.Status.Vulnerabilities.Images[0]
		Expect(scan.Digest).To(Equal(scanImageDigest))
		Expect(scan.Repository).To(Equal("oci://" + scanVendoredRepo))
		Expect(scan.Platform).To(Equal(scanPlatform))
		Expect(scan.ScannedTime.Time).To(BeTemporally("==", now))
		Expect(scan.SBOM.Origin).To(Equal(deliveryv1alpha1.SBOMOriginGenerated))
		Expect(scan.Summary).To(Equal(deliveryv1alpha1.VulnerabilitySummary{Critical: 1, High: 2, Medium: 1, Unknown: 1, Fixable: 3}))
		Expect(apimeta.IsStatusConditionTrue(product.Status.Conditions, deliveryv1alpha1.ConditionTypeScanned)).To(BeTrue())

		By("attaching the SBOM and the report to the vendored image, signed")
		Expect(registry.Referrers).To(HaveLen(2))
		Expect(registry.Referrers[0].Digest).To(Equal(scan.SBOM.Digest))
		Expect(registry.Referrers[0].Subject.Digest).To(Equal(scanImageDigest))
		Expect(registry.Referrers[0].Artifact.Annotations).To(HaveKeyWithValue(sbom.AnnotationPlatform, scanPlatform))
		Expect(registry.Referrers[1].Digest).To(Equal(scan.Report))
		Expect(registry.Referrers[1].Artifact.Annotations).To(HaveKeyWithValue(oci.AnnotationBundlePredicateType, vulnerability.PredicateType))
		Expect(registry.Referrers[1].Artifact.Annotations).To(HaveKeyWithValue(sbom.AnnotationSBOMOrigin, "generated"))

		By("recording in the signed report the SBOM and the database it was computed with")
		subject, err := oci.Parse("oci://" + scanVendoredRepo + "@" + scanImageDigest)
		Expect(err).NotTo(HaveOccurred())
		evidence, err := attestationEvidence(registry, subject, scan.Report, vulnerability.PredicateType)
		Expect(err).NotTo(HaveOccurred())
		var report vulnerability.Report
		Expect(evidence.Predicate(&report)).To(Succeed())
		Expect(report.Scanner.DB.URI).To(Equal(req.Database.Ref))
		params, err := json.Marshal(report.Invocation.Parameters)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(params)).To(MatchJSON(`{"sbom":{"digest":"` + scan.SBOM.Digest + `","origin":"Generated"},"platform":"` + scanPlatform + `"}`))

		Expect(result.RequeueAfter).To(Equal(24*time.Hour), "the next scan is due after the Kitchen's interval")

		By("not scanning again before the interval")
		now = now.Add(time.Hour)
		Expect(reconcileProduct().RequeueAfter).To(Equal(23 * time.Hour))
		Expect(imageScans.requests).To(HaveLen(1))

		By("scanning again after the interval, from the SBOM it attached")
		now = now.Add(24 * time.Hour)
		reconcileProduct()
		Expect(imageScans.requests).To(HaveLen(2))
		Expect(imageScans.requests[1].Images[0].SBOM).To(MatchJSON(scanGeneratedSBOM), "Kokumi's own signed SBOM is reused")
		Expect(registry.Referrers).To(HaveLen(3), "only a new report is attached")
		rescanned := getProduct().Status.Vulnerabilities.Images[0]
		Expect(rescanned.SBOM).To(Equal(scan.SBOM))
		Expect(rescanned.ScannedTime.Time).To(BeTemporally("==", now))
	})

	It("keeps the recent Preparations of an Order scanned, for a rollback", func() {
		const newerDigest = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
		registry.Manifests[newerDigest] = oci.Manifest{MediaType: ocispec.MediaTypeImageManifest, Digest: newerDigest}
		older := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: scanNamespace, Name: scanPreparationName}, older)).To(Succeed())
		newer := &deliveryv1alpha1.Preparation{
			Name:      "scanned-prod-newer0000000",
			Namespace: scanNamespace,
			Spec:      *older.Spec.DeepCopy(),
		}
		newer.Spec.Images[0].Digest = newerDigest
		newer.Spec.Images[0].Vendored = scanVendoredRepo + ":1.28@" + newerDigest
		Expect(k8sClient.Create(ctx, newer)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, newer) })
		apimeta.SetStatusCondition(&newer.Status.Conditions, metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionTrue, Reason: testReadyReason,
		})
		Expect(k8sClient.Status().Update(ctx, newer)).To(Succeed())

		reconcileProduct()
		images := getProduct().Status.Vulnerabilities.Images
		digests := make([]string, 0, len(images))
		for _, img := range images {
			digests = append(digests, img.Digest)
		}
		Expect(digests).To(ConsistOf(scanImageDigest, newerDigest), "the older Preparation is a rollback target")
	})

	It("does not scan while the cache lags behind the Product", func() {
		reconcileProduct()
		Expect(imageScans.requests).To(HaveLen(1))
		scanned := getProduct()

		// The cache still holds the Product from before the scan, without
		// its record, while a Preparation reconciles it.
		lagging := scanned.DeepCopy()
		lagging.Status.Vulnerabilities = nil
		lagging.ResourceVersion = "1"
		reconciler.Client = laggingProductCache{Client: k8sClient, product: lagging}
		now = now.Add(time.Minute)

		Expect(reconcileProduct()).To(Equal(ctrl.Result{RequeueAfter: scanCacheRetryInterval}))
		Expect(imageScans.requests).To(HaveLen(1), "the image was scanned a minute ago")
		Expect(getProduct().Status.Vulnerabilities).To(Equal(scanned.Status.Vulnerabilities))
	})

	It("reports a scanner it cannot reach and tries again", func() {
		imageScans.err = &scanner.UnavailableError{RetryAfter: 30 * time.Second, Err: errors.New("busy")}

		result := reconcileProduct()
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		cond := apimeta.FindStatusCondition(getProduct().Status.Conditions, deliveryv1alpha1.ConditionTypeScanned)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonScannerUnavailable))
		Expect(registry.Referrers).To(BeEmpty())
	})

	It("reports a database older than the Kitchen allows", func() {
		imageScans.built = now.Add(-30 * 24 * time.Hour)

		reconcileProduct()
		cond := apimeta.FindStatusCondition(getProduct().Status.Conditions, deliveryv1alpha1.ConditionTypeScanned)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(deliveryv1alpha1.ReasonDatabaseStale))
		Expect(getProduct().Status.Vulnerabilities.Images).To(HaveLen(1), "the reports are still recorded")
	})

	It("clears the scans when the Kitchen disables scanning", func() {
		reconcileProduct()
		Expect(getProduct().Status.Vulnerabilities).NotTo(BeNil())

		setScanning(nil)
		reconcileProduct()
		product := getProduct()
		Expect(product.Status.Vulnerabilities).To(BeNil())
		Expect(apimeta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeScanned)).To(BeNil())
		Expect(imageScans.requests).To(HaveLen(1))
	})

	It("scans only the images it vendors", func() {
		product := getProduct()
		product.Spec.Images.Policy = deliveryv1alpha1.ImagePolicyPin
		Expect(k8sClient.Update(ctx, product)).To(Succeed())

		reconcileProduct()
		Expect(imageScans.requests).To(BeEmpty())
		Expect(getProduct().Status.Vulnerabilities).To(BeNil())
	})

	It("drops images no Preparation in use runs any more", func() {
		reconcileProduct()
		Expect(getProduct().Status.Vulnerabilities.Images).To(HaveLen(1))

		prep := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: scanNamespace, Name: scanPreparationName}, prep)).To(Succeed())
		apimeta.SetStatusCondition(&prep.Status.Conditions, metav1.Condition{
			Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: "Archived",
		})
		Expect(k8sClient.Status().Update(ctx, prep)).To(Succeed())

		reconcileProduct()
		product := getProduct()
		Expect(product.Status.Vulnerabilities.Images).To(BeEmpty())
		Expect(apimeta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeScanned).Message).To(Equal("No vendored images in use"))
	})
})

// attestationEvidence verifies the referrer with digest of subject, signed by
// the test signer, and returns its evidence.
func attestationEvidence(registry *oci.FakeClient, subject oci.Reference, digest, predicateType string) (*attestation.Evidence, error) {
	return attestation.VerifyReferrer(ctx, registry, testSigner.TrustSet(), subject, digest, predicateType)
}
