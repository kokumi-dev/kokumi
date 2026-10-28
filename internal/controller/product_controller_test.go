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
	stderrors "errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/attestation"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
	signingsource "github.com/kokumi-dev/kokumi/internal/signing/source"
)

// unlistableClient is a FakeClient whose registry cannot list referrers.
type unlistableClient struct {
	*oci.FakeClient
}

func (unlistableClient) ListReferrers(context.Context, oci.Reference, string) ([]oci.Descriptor, error) {
	return nil, stderrors.New("registry unreachable")
}

// testKeyID is the key ID the fake source verifier reports.
const testKeyID = "key-id"

// fakeSourceVerifier records the policies it is asked to verify and answers
// with signer and verdicts, or err. Without a policy it verifies against the
// Kitchen keys as the real verifier does.
type fakeSourceVerifier struct {
	policies []signingsource.Policy
	signer   *signingsource.Signer
	verdicts []signingsource.Verdict
	err      error
}

func (f *fakeSourceVerifier) Verify(ctx context.Context, conn registry.Connection, ref oci.Reference, policy *signingsource.Policy, trust *signing.TrustSet) (*signingsource.Report, error) {
	if policy == nil {
		return signingsource.NewVerifier().Verify(ctx, conn, ref, nil, trust)
	}
	f.policies = append(f.policies, *policy)
	if f.err != nil {
		return nil, f.err
	}
	return &signingsource.Report{Signer: f.signer, Verdicts: f.verdicts}, nil
}

// unresolvableClient is a FakeClient whose registry cannot resolve tags.
type unresolvableClient struct {
	*oci.FakeClient
}

func (unresolvableClient) Resolve(context.Context, oci.Reference) (oci.Descriptor, error) {
	return oci.Descriptor{}, stderrors.New("registry unreachable")
}

var _ = Describe("Product Controller", func() {
	const ns = testNamespace

	ctx := context.Background()

	var (
		fake     *oci.FakeClient
		verifier *fakeSourceVerifier
		r        *ProductReconciler
	)

	BeforeEach(func() {
		fs := afero.NewMemMapFs()
		fake = oci.NewFakeClient(fs)
		verifier = &fakeSourceVerifier{}
		r = &ProductReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			Store:          artifact.NewStore(fake, fs, ""),
			Registries:     fakeRegistries(fake),
			Signing:        testSigning,
			SourceVerifier: verifier,
		}
	})

	reconcileProductResult := func(name string) (reconcile.Result, error) {
		return r.Reconcile(ctx, reconcile.Request{Namespace: ns, Name: name})
	}

	reconcileProduct := func(name string) error {
		_, err := reconcileProductResult(name)
		return err
	}

	getProduct := func(name string) *deliveryv1alpha1.Product {
		product := &deliveryv1alpha1.Product{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, product)).To(Succeed())
		return product
	}

	readyOf := func(name string) *metav1.Condition {
		return meta.FindStatusCondition(getProduct(name).Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
	}

	create := func(name string, spec deliveryv1alpha1.ProductSpec) {
		Expect(k8sClient.Create(ctx, &deliveryv1alpha1.Product{Name: name, Namespace: ns, Spec: spec})).To(Succeed())
		DeferCleanup(func() { deleteTestProduct(ctx, name) })
	}

	sourceVerifiedOf := func(name string) *metav1.Condition {
		return meta.FindStatusCondition(getProduct(name).Status.Conditions, deliveryv1alpha1.ConditionTypeSourceVerified)
	}

	// signSource signs the source at testOCIRef, resolved by the fake to
	// oci.FakeDigest, as a Kokumi installation with signer's key would.
	signSource := func(signer *signing.Signer) {
		ref, err := oci.Parse(testOCIRef + "@" + oci.FakeDigest)
		Expect(err).NotTo(HaveOccurred())
		_, err = attestation.Sign(ctx, fake, signer, ref, time.Date(2026, 10, 12, 9, 14, 3, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
	}

	It("allows Orders no changes unless overrides say otherwise", func() {
		const name = "product-defaults"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})

		overrides := getProduct(name).Spec.Overrides
		Expect(overrides.Values.Policy).To(Equal(deliveryv1alpha1.OverridePolicyNone))
		Expect(overrides.Patches.Policy).To(Equal(deliveryv1alpha1.OverridePolicyNone))

		invalid := func(overrides deliveryv1alpha1.Overrides) error {
			return k8sClient.Create(ctx, &deliveryv1alpha1.Product{
				Name: "product-invalid-overrides", Namespace: ns,
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}, Overrides: overrides},
			})
		}
		Expect(invalid(deliveryv1alpha1.Overrides{
			Values: deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyListed},
		})).To(MatchError(ContainSubstring("paths must be set when policy is Listed")))
		Expect(invalid(deliveryv1alpha1.Overrides{
			Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyAll, Targets: []deliveryv1alpha1.AllowedPatchTarget{{
				Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName}, Paths: []string{testReplicasPath},
			}}},
		})).To(MatchError(ContainSubstring("targets must be set when policy is Listed, and only then")))
	})

	It("publishes the upstream source and protects the Product", func() {
		const name = "product-upstream"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})

		Expect(reconcileProduct(name)).To(Succeed())

		product := getProduct(name)
		Expect(product.Finalizers).To(ContainElement(deliveryv1alpha1.FinalizerProductProtection))
		Expect(product.Status.Source.URL).To(Equal(testOCIRef))
		Expect(product.Status.Source.Version).To(Equal(testVersion))
		Expect(product.Status.Source.Digest).To(Equal(oci.FakeDigest), "Orders render exactly this artifact")
		Expect(product.Status.ConfigHash).To(HavePrefix("sha256:"))
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionTrue))
		Expect(readyOf(name).Reason).To(Equal("Resolved"))
		Expect(fake.Copies).To(BeEmpty())
		Expect(product.Status.Source.Verified).To(BeNil(), "an unsigned source is not verified")
		Expect(sourceVerifiedOf(name)).To(BeNil(), "and need not be")
	})

	It("verifies a source signed by a key the Kitchen trusts", func() {
		const name = "product-signed-by-kokumi"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		signSource(testSigner)

		Expect(reconcileProduct(name)).To(Succeed())

		Expect(getProduct(name).Status.Source.Verified).To(Equal(&deliveryv1alpha1.SourceVerification{
			Provider: deliveryv1alpha1.VerificationProviderCosign, KeyID: testSigner.KeyID(),
		}))
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionTrue))
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionTrue))
		Expect(verifier.policies).To(BeEmpty(), "cosign is only used for spec.source.verify")
	})

	It("records the verdict of every signature in the graph of the source", func() {
		const name = "product-signatures"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		signSource(testSigner)
		source, err := oci.Parse(testOCIRef + "@" + oci.FakeDigest)
		Expect(err).NotTo(HaveOccurred())
		sbom, err := fake.PushReferrer(ctx, source, oci.ReferrerArtifact{ArtifactType: "application/spdx+json", MediaType: "application/spdx+json", Payload: []byte("{}")})
		Expect(err).NotTo(HaveOccurred())
		sbomRef := source
		sbomRef.Digest = sbom
		sbomSignature, err := attestation.Sign(ctx, fake, testSigner, sbomRef, time.Date(2026, 10, 12, 9, 15, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())

		Expect(reconcileProduct(name)).To(Succeed())

		published := getProduct(name).Status.Source
		Expect(published.Signatures).To(HaveLen(2), "the SBOM is not a signature, its signature is")
		signer := &deliveryv1alpha1.SourceVerification{Provider: deliveryv1alpha1.VerificationProviderCosign, KeyID: testSigner.KeyID()}
		Expect(published.Signatures).To(ContainElement(deliveryv1alpha1.SignatureVerdict{
			Digest: sbomSignature, Subject: sbom, Result: deliveryv1alpha1.SignatureVerified, Signer: signer,
		}))
		Expect(published.SignaturesCheckedTime).NotTo(BeNil())

		By("keeping the check time, and writing no status, while nothing changed")
		before := getProduct(name)
		Expect(reconcileProduct(name)).To(Succeed())
		after := getProduct(name)
		Expect(after.Status.Source.SignaturesCheckedTime).To(Equal(before.Status.Source.SignaturesCheckedTime))
		Expect(after.ResourceVersion).To(Equal(before.ResourceVersion), "a status write would trigger the next reconcile")
	})

	It("does not verify a source signed by an untrusted key", func() {
		const name = "product-signed-by-stranger"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		signSource(signingtest.NewSigner(GinkgoT()))

		Expect(reconcileProduct(name)).To(Succeed())

		Expect(getProduct(name).Status.Source.Verified).To(BeNil())
		Expect(sourceVerifiedOf(name)).To(BeNil())
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionTrue))
		Expect(getProduct(name).Status.Source.Signatures).To(ConsistOf(HaveField("Result", deliveryv1alpha1.SignatureUntrustedSigner)))
	})

	It("renders nothing from an unverified source when the Kitchen requires verification", func() {
		const name = "product-must-verify"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		state, err := testSigning.State(ctx)
		Expect(err).NotTo(HaveOccurred())
		state.RequireSourceVerification = true
		r.Signing = signing.NewStaticProvider(state)

		result, err := reconcileProductResult(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0), "a source may be signed later")

		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonVerificationRequired))
		Expect(sourceVerifiedOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonVerificationRequired))
		Expect(getProduct(name).Status.Source).To(BeNil(), "an unverified source is never published")
		Expect(resolve.ProductReady(getProduct(name))).To(BeFalse())

		By("publishing it once a trusted key signed it")
		signSource(testSigner)
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(resolve.ProductReady(getProduct(name))).To(BeTrue())
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionTrue))
	})

	It("verifies spec.source.verify with every public key of the Secret", func() {
		const (
			name       = "product-verify-keys"
			secretName = "product-verify-keys"
		)
		secret := &corev1.Secret{Name: secretName, Namespace: ns, Data: map[string][]byte{
			"cosign.pub":  testSigner.PublicKeyPEM(),
			"release.pub": testSigner.PublicKeyPEM(),
			"README":      []byte("not a key"),
		}}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{
			URL: testOCIRef, Version: testVersion,
			Verify: &deliveryv1alpha1.OCISourceVerification{SecretRef: &corev1.LocalObjectReference{Name: secretName}},
		}})
		verifier.signer = &signingsource.Signer{KeyID: testKeyID}

		Expect(reconcileProduct(name)).To(Succeed())

		Expect(verifier.policies).To(HaveLen(1))
		Expect(verifier.policies[0].Keys).To(HaveLen(2))
		Expect(verifier.policies[0].Keys[0].Name).To(Equal("cosign.pub"))
		Expect(getProduct(name).Status.Source.Verified).To(Equal(&deliveryv1alpha1.SourceVerification{
			Provider: deliveryv1alpha1.VerificationProviderCosign, KeyID: testKeyID,
		}))
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionTrue))
	})

	It("records the keyless signer and does not publish a source that fails to verify", func() {
		const name = "product-verify-keyless"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{
				URL: testOCIRef, Version: testVersion,
				Verify: &deliveryv1alpha1.OCISourceVerification{MatchOIDCIdentity: []deliveryv1alpha1.OIDCIdentityMatch{{
					Issuer: "^https://token\\.actions\\.githubusercontent\\.com$", Subject: "^https://github\\.com/example/.*$",
				}}},
			},
			Vendor: &deliveryv1alpha1.OCIDestination{},
		})
		verifier.err = stderrors.New("no matching signatures")

		result, err := reconcileProductResult(name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(readyOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonVerificationError))
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(getProduct(name).Status.Source).To(BeNil())
		Expect(fake.Copies).To(BeEmpty(), "an unverified source is not vendored")

		By("vendoring it with its signatures once it verifies")
		verifier.err = nil
		verifier.signer = &signingsource.Signer{Issuer: "https://token.actions.githubusercontent.com", Subject: "https://github.com/example/release.yml@refs/tags/v1"}
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Verified.Subject).To(Equal(verifier.signer.Subject))
		Expect(fake.Copies).To(HaveLen(1))
		Expect(fake.Copies[0].Options.IncludeReferrers).To(BeTrue())
		Expect(fake.Copies[0].Source.Digest).To(Equal(oci.FakeDigest), "the verified digest is copied, not the tag")
	})

	It("records the verdicts of a source that no longer verifies", func() {
		const (
			name       = "product-verify-withdrawn"
			secretName = "product-verify-withdrawn"
		)
		secret := &corev1.Secret{Name: secretName, Namespace: ns, Data: map[string][]byte{"cosign.pub": testSigner.PublicKeyPEM()}}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{
			URL: testOCIRef, Version: testVersion,
			Verify: &deliveryv1alpha1.OCISourceVerification{SecretRef: &corev1.LocalObjectReference{Name: secretName}},
		}})
		signature := "sha256:" + strings.Repeat("5", 64)
		verifier.signer = &signingsource.Signer{KeyID: testKeyID}
		verifier.verdicts = []signingsource.Verdict{{Digest: signature, Subject: oci.FakeDigest, Result: signingsource.Verified, Signer: verifier.signer}}
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Signatures[0].Result).To(Equal(deliveryv1alpha1.SignatureVerified))

		By("rotating the key away")
		verifier.signer = nil
		verifier.verdicts = []signingsource.Verdict{{Digest: signature, Subject: oci.FakeDigest, Result: signingsource.UntrustedSigner, Message: "signed by key key-id"}}
		Expect(reconcileProduct(name)).To(Succeed())

		published := getProduct(name).Status.Source
		Expect(published.Verified).To(BeNil())
		Expect(published.Signatures).To(Equal([]deliveryv1alpha1.SignatureVerdict{{
			Digest: signature, Subject: oci.FakeDigest, Result: deliveryv1alpha1.SignatureUntrustedSigner, Message: "signed by key key-id",
		}}))
		Expect(sourceVerifiedOf(name).Message).To(ContainSubstring("UntrustedSigner: signed by key key-id"))
	})

	It("keeps the recorded verification while the registry cannot be read", func() {
		const name = "product-signed-registry-down"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		signSource(testSigner)
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Verified).NotTo(BeNil())

		r.Registries = fakeRegistries(unlistableClient{fake})
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Verified.KeyID).To(Equal(testSigner.KeyID()))
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionTrue))

		By("retrying instead when the Kitchen requires verification")
		state, err := testSigning.State(ctx)
		Expect(err).NotTo(HaveOccurred())
		state.RequireSourceVerification = true
		r.Signing = signing.NewStaticProvider(state)
		Expect(reconcileProduct(name)).To(MatchError(ContainSubstring("registry unreachable")))
		Expect(sourceVerifiedOf(name).Status).To(Equal(metav1.ConditionTrue), "the status is left alone")
	})

	It("withdraws the verification when the trust no longer holds", func() {
		const name = "product-trust-withdrawn"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		other := signingtest.NewSigner(GinkgoT())
		signSource(other)
		r.Signing = signingtest.NewProvider(GinkgoT(), testSigner, other)

		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Verified.KeyID).To(Equal(other.KeyID()))

		By("removing the key from the trusted keys")
		r.Signing = testSigning
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.Verified).To(BeNil())
		Expect(sourceVerifiedOf(name)).To(BeNil())
	})

	It("vendors the source and publishes the copy", func() {
		const (
			name      = "product-vendored"
			vendorURL = "oci://registry.kokumi.svc.cluster.local:5000/vendor/product"
		)
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
			Vendor: &deliveryv1alpha1.OCIDestination{URL: vendorURL},
		})

		Expect(reconcileProduct(name)).To(Succeed())

		source := getProduct(name).Status.Source
		Expect(source.URL).To(Equal(vendorURL))
		Expect(source.Version).To(Equal(testVersion))
		Expect(source.Digest).To(HavePrefix("sha256:"))
		Expect(readyOf(name).Reason).To(Equal("Vendored"))
		Expect(fake.Copies).To(HaveLen(1))

		By("skipping the copy while nothing changed")
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(fake.Copies).To(HaveLen(1))
	})

	It("fails while the source digest cannot be resolved", func() {
		const name = "product-unresolvable"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})

		fs := afero.NewMemMapFs()
		unresolvable := unresolvableClient{oci.NewFakeClient(fs)}
		r.Store = artifact.NewStore(unresolvable, fs, "")
		r.Registries = fakeRegistries(unresolvable)

		Expect(reconcileProduct(name)).NotTo(Succeed())
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Message).To(ContainSubstring("failed to resolve source digest"))
		Expect(getProduct(name).Status.Source).To(BeNil())
	})

	It("marks a spec change that keeps the source ready without vendoring again", func() {
		const name = "product-patched"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
			Vendor: &deliveryv1alpha1.OCIDestination{},
		})
		Expect(reconcileProduct(name)).To(Succeed())
		first := getProduct(name).Status

		product := getProduct(name)
		product.Spec.Patches = []deliveryv1alpha1.Patch{{
			Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: testDeploymentName},
			Set:    map[string]string{testReplicasPath: "2"},
		}}
		product.Spec.Overrides.Values.Policy = deliveryv1alpha1.OverridePolicyAll
		Expect(k8sClient.Update(ctx, product)).To(Succeed())

		Expect(reconcileProduct(name)).To(Succeed())
		product = getProduct(name)
		Expect(fake.Copies).To(HaveLen(1), "the source is not vendored again")
		Expect(product.Status.ConfigHash).To(Equal(first.ConfigHash))
		Expect(product.Status.Source).To(Equal(first.Source))
		Expect(readyOf(name).ObservedGeneration).To(Equal(product.Generation))
		Expect(resolve.ProductReady(product)).To(BeTrue())
	})

	It("does not mark a spec change ready that it did not resolve", func() {
		const name = "product-changed-while-resolving"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		resolving := getProduct(name)

		By("changing the version while the old one is being resolved")
		changed := resolving.DeepCopy()
		changed.Spec.Source.Version = "2.0.0"
		Expect(k8sClient.Update(ctx, changed)).To(Succeed())

		By("recording the resolved old version")
		before := resolving.Status.DeepCopy()
		markPublished(resolving, "sha256:old", &deliveryv1alpha1.ProductSourceStatus{URL: testOCIRef, Version: testVersion, Digest: oci.FakeDigest})
		result, err := r.patchStatus(ctx, resolving, before, reconcile.Result{}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0), "the status write is locked to the version that was resolved")

		product := getProduct(name)
		Expect(product.Status.Source).To(BeNil())
		Expect(resolve.ProductReady(product)).To(BeFalse(), "Orders must not render the new spec over the old source")
	})

	It("vendors into the in-cluster registry when no destination is set", func() {
		const name = "product-vendored-default"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
			Vendor: &deliveryv1alpha1.OCIDestination{},
		})

		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.URL).To(Equal(artifact.DefaultVendorDestination(ns, name)))
	})

	It("resolves again when the source URL changes", func() {
		const (
			name      = "product-source-url"
			secondURL = "oci://registry.kokumi.svc.cluster.local:5000/charts/product-v2"
		)
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})

		Expect(reconcileProduct(name)).To(Succeed())
		first := getProduct(name).Status

		product := getProduct(name)
		product.Spec.Source.URL = secondURL
		Expect(k8sClient.Update(ctx, product)).To(Succeed())

		Expect(reconcileProduct(name)).To(Succeed())
		second := getProduct(name).Status
		Expect(second.Source.URL).To(Equal(secondURL))
		Expect(second.ConfigHash).NotTo(Equal(first.ConfigHash))
	})

	It("records where it connects and through which Registry", func() {
		const (
			name      = "product-registry-access"
			vendorURL = "oci://ghcr.io/kokumi-test/mirror/app"
		)
		reg := &deliveryv1alpha1.Registry{
			Name: name, Namespace: ns,
			Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://ghcr.io/kokumi-test"},
		}
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, reg) })
		create(name, deliveryv1alpha1.ProductSpec{
			Source:       deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
			Vendor:       &deliveryv1alpha1.OCIDestination{URL: vendorURL},
			RegistryRefs: []corev1.LocalObjectReference{{Name: name}},
		})

		Expect(reconcileProduct(name)).To(Succeed())

		Expect(getProduct(name).Status.RegistryAccess).To(Equal([]deliveryv1alpha1.RegistryAccess{
			{URL: testOCIRef, Operation: deliveryv1alpha1.RegistryOperationPull, Client: deliveryv1alpha1.RegistryClientKokumi},
			{URL: vendorURL, Operation: deliveryv1alpha1.RegistryOperationPush, Client: deliveryv1alpha1.RegistryClientKokumi, Registry: name},
		}))
	})

	It("waits while a Registry it lists is missing", func() {
		const name = "product-missing-registry"
		create(name, deliveryv1alpha1.ProductSpec{
			Source:       deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion},
			RegistryRefs: []corev1.LocalObjectReference{{Name: "does-not-exist"}},
		})

		Expect(reconcileProduct(name)).To(Succeed(), "the Registry watch reconciles it again")
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonRegistryNotFound))
		Expect(getProduct(name).Status.Source).To(BeNil())
	})

	It("does not vendor a private source to a location without credentials", func() {
		const name = "product-private-anonymous-vendor"
		secret := testPullSecret(name, "ghcr.io")
		reg := &deliveryv1alpha1.Registry{
			Name: name, Namespace: ns,
			Spec: deliveryv1alpha1.RegistrySpec{URL: "oci://ghcr.io/kokumi-test", SecretRef: &corev1.LocalObjectReference{Name: name}},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		Expect(k8sClient.Create(ctx, reg)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, reg)
			_ = k8sClient.Delete(ctx, secret)
		})
		denied := &oci.FakeClient{Resolved: map[string]oci.Descriptor{}}
		r.Registries = testRegistries(func(opts oci.Options) oci.Client {
			if opts.Credential.Username == "" {
				return denied
			}
			return fake
		})
		create(name, deliveryv1alpha1.ProductSpec{
			Source:       deliveryv1alpha1.OCISource{URL: "oci://ghcr.io/kokumi-test/charts/app", Version: testVersion},
			Vendor:       &deliveryv1alpha1.OCIDestination{},
			RegistryRefs: []corev1.LocalObjectReference{{Name: name}},
		})

		Expect(reconcileProduct(name)).To(Succeed())

		Expect(readyOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonCredentialsRequired))
		Expect(readyOf(name).Message).To(ContainSubstring(artifact.DefaultVendorDestination(ns, name)))
		Expect(fake.Copies).To(BeEmpty())
		Expect(denied.Copies).To(BeEmpty())
	})

	It("is only deleted once no Order references it", func() {
		const (
			name      = "product-in-use"
			orderName = "order-of-product-in-use"
		)
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{URL: testOCIRef, Version: testVersion}})
		order := &deliveryv1alpha1.Order{
			Name: orderName, Namespace: ns,
			Spec: deliveryv1alpha1.OrderSpec{EnvironmentRef: localEnvironment, ProductRef: deliveryv1alpha1.ProductRef{Name: name}},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, order) })

		Expect(reconcileProduct(name)).To(Succeed())
		Expect(k8sClient.Delete(ctx, getProduct(name))).To(Succeed())

		By("waiting for the Order")
		Expect(reconcileProduct(name)).To(Succeed())
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Reason).To(Equal(deliveryv1alpha1.ReasonInUse))
		Expect(readyOf(name).Message).To(ContainSubstring(orderName))
		Expect(getProduct(name).Finalizers).To(ContainElement(deliveryv1alpha1.FinalizerProductProtection))

		By("releasing the Product once the Order is gone")
		Expect(k8sClient.Delete(ctx, order)).To(Succeed())
		Expect(reconcileProduct(name)).To(Succeed())
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &deliveryv1alpha1.Product{})
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})
})

var _ = Describe("Product field indexes", func() {
	secret := func(name string) *corev1.LocalObjectReference { return &corev1.LocalObjectReference{Name: name} }

	It("indexes a Product by every Registry it lists", func() {
		const upstream, mirror = "product-upstream", "product-mirror"
		product := &deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{
			RegistryRefs: []corev1.LocalObjectReference{{Name: upstream}, {Name: mirror}},
		}}
		Expect(productRegistryNames(product)).To(Equal([]string{upstream, mirror}))
		Expect(productRegistryNames(&deliveryv1alpha1.Product{})).To(BeEmpty())
	})

	DescribeTable("indexes a Product by the Secrets its source verification reads",
		func(verify *deliveryv1alpha1.OCISourceVerification, want []string) {
			product := &deliveryv1alpha1.Product{Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{Verify: verify}}}
			Expect(productVerifySecrets(product)).To(Equal(want))
		},
		Entry("no verification", nil, nil),
		Entry("keys and trusted root",
			&deliveryv1alpha1.OCISourceVerification{SecretRef: secret("keys"), TrustedRootSecretRef: secret("sigstore-root")},
			[]string{"keys", "sigstore-root"}),
		Entry("the same Secret once",
			&deliveryv1alpha1.OCISourceVerification{SecretRef: secret("trust"), TrustedRootSecretRef: secret("trust")},
			[]string{"trust"}),
	)
})
