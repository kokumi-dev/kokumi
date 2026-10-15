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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/resolve"
	"github.com/kokumi-dev/kokumi/internal/status"
)

// unresolvableClient is a FakeClient whose registry cannot resolve tags.
type unresolvableClient struct {
	*oci.FakeClient
}

func (unresolvableClient) Resolve(context.Context, oci.Reference) (string, error) {
	return "", stderrors.New("registry unreachable")
}

var _ = Describe("Product Controller", func() {
	const ns = testNamespace

	ctx := context.Background()

	var (
		fake *oci.FakeClient
		r    *ProductReconciler
	)

	BeforeEach(func() {
		fs := afero.NewMemMapFs()
		fake = oci.NewFakeClient(fs)
		r = &ProductReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			Store:          artifact.NewStore(fake, fs, ""),
			PantryResolver: credential.NewKubeResolver(k8sClient),
		}
	})

	reconcileProduct := func(name string) error {
		_, err := r.Reconcile(ctx, reconcile.Request{Namespace: ns, Name: name})
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

	It("allows Orders no changes unless overrides say otherwise", func() {
		const name = "product-defaults"
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}})

		overrides := getProduct(name).Spec.Overrides
		Expect(overrides.Values.Policy).To(Equal(deliveryv1alpha1.OverridePolicyNone))
		Expect(overrides.Patches.Policy).To(Equal(deliveryv1alpha1.OverridePolicyNone))

		invalid := func(overrides deliveryv1alpha1.Overrides) error {
			return k8sClient.Create(ctx, &deliveryv1alpha1.Product{
				Name: "product-invalid-overrides", Namespace: ns,
				Spec: deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}, Overrides: overrides},
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
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}})

		Expect(reconcileProduct(name)).To(Succeed())

		product := getProduct(name)
		Expect(product.Finalizers).To(ContainElement(deliveryv1alpha1.FinalizerProductProtection))
		Expect(product.Status.Source.OCI).To(Equal(testOCIRef))
		Expect(product.Status.Source.Version).To(Equal(testVersion))
		Expect(product.Status.Source.Digest).To(Equal(oci.FakeDigest), "Orders render exactly this artifact")
		Expect(product.Status.ConfigHash).To(HavePrefix("sha256:"))
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionTrue))
		Expect(readyOf(name).Reason).To(Equal("Resolved"))
		Expect(fake.Copies).To(BeEmpty())
	})

	It("vendors the source and publishes the copy", func() {
		const (
			name      = "product-vendored"
			vendorURL = "oci://registry.kokumi.svc.cluster.local:5000/vendor/product"
		)
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion},
			Vendor: &deliveryv1alpha1.OCIDestination{OCI: vendorURL},
		})

		Expect(reconcileProduct(name)).To(Succeed())

		source := getProduct(name).Status.Source
		Expect(source.OCI).To(Equal(vendorURL))
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
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}})

		fs := afero.NewMemMapFs()
		r.Store = artifact.NewStore(unresolvableClient{oci.NewFakeClient(fs)}, fs, "")

		Expect(reconcileProduct(name)).NotTo(Succeed())
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Message).To(ContainSubstring("failed to resolve source digest"))
		Expect(getProduct(name).Status.Source).To(BeNil())
	})

	It("marks a spec change that keeps the source ready without vendoring again", func() {
		const name = "product-patched"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion},
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
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}})
		resolving := getProduct(name)

		By("changing the version while the old one is being resolved")
		changed := resolving.DeepCopy()
		changed.Spec.Source.Version = "2.0.0"
		Expect(k8sClient.Update(ctx, changed)).To(Succeed())

		source := &deliveryv1alpha1.ProductSourceStatus{OCI: testOCIRef, Version: testVersion, Digest: oci.FakeDigest}
		Expect(status.NewProductUpdater(k8sClient).Ready(ctx, resolving, "sha256:old", source)).To(Succeed())

		product := getProduct(name)
		Expect(product.Status.Source.Version).To(Equal(testVersion))
		Expect(product.Status.ObservedGeneration).To(Equal(resolving.Generation))
		Expect(resolve.ProductReady(product)).To(BeFalse(), "Orders must not render the new spec over the old source")
	})

	It("vendors into the in-cluster registry when no destination is set", func() {
		const name = "product-vendored-default"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion},
			Vendor: &deliveryv1alpha1.OCIDestination{},
		})

		Expect(reconcileProduct(name)).To(Succeed())
		Expect(getProduct(name).Status.Source.OCI).To(Equal(artifact.DefaultVendorDestination(ns, name)))
	})

	It("resolves again when the source Pantry URL changes", func() {
		const (
			name       = "product-pantry"
			pantryName = "product-pantry-src"
			firstURL   = "oci://registry.kokumi.svc.cluster.local:5000/charts/product"
			secondURL  = "oci://registry.kokumi.svc.cluster.local:5000/charts/product-v2"
		)
		pantry := &deliveryv1alpha1.Pantry{Name: pantryName, Namespace: ns, Spec: deliveryv1alpha1.PantrySpec{URL: firstURL}}
		Expect(k8sClient.Create(ctx, pantry)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pantry) })
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: pantryName}, Version: testVersion},
		})

		Expect(reconcileProduct(name)).To(Succeed())
		first := getProduct(name).Status
		Expect(first.Source.OCI).To(Equal(firstURL))
		Expect(first.Source.PantryRef.Name).To(Equal(pantryName))

		pantry.Spec.URL = secondURL
		Expect(k8sClient.Update(ctx, pantry)).To(Succeed())

		Expect(reconcileProduct(name)).To(Succeed())
		second := getProduct(name).Status
		Expect(second.Source.OCI).To(Equal(secondURL))
		Expect(second.ConfigHash).NotTo(Equal(first.ConfigHash))
	})

	It("fails while the source Pantry is missing", func() {
		const name = "product-missing-pantry"
		create(name, deliveryv1alpha1.ProductSpec{
			Source: deliveryv1alpha1.OCISource{PantryRef: &deliveryv1alpha1.PantryRef{Name: "does-not-exist"}, Version: testVersion},
		})

		Expect(reconcileProduct(name)).NotTo(Succeed())
		Expect(readyOf(name).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(name).Reason).To(Equal("Failed"))
		Expect(getProduct(name).Status.Source).To(BeNil())
	})

	It("is only deleted once no Order references it", func() {
		const (
			name      = "product-in-use"
			orderName = "order-of-product-in-use"
		)
		create(name, deliveryv1alpha1.ProductSpec{Source: deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion}})
		order := &deliveryv1alpha1.Order{
			Name: orderName, Namespace: ns,
			Spec: deliveryv1alpha1.OrderSpec{ProductRef: deliveryv1alpha1.ProductRef{Name: name}},
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
