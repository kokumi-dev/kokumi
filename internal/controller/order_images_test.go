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
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/afero"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/credential"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

const (
	imagesNginxName = "docker.io/library/nginx"
	imagesNginxRef  = "nginx:1.27"
	// imagesNginxLookup is the reference nginx:1.27 is looked up by.
	imagesNginxLookup = imagesNginxName + ":1.27"
	imagesNginxDigest = "sha256:abababababababababababababababababababababababababababababababab"
	imagesManifest    = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
        - name: helper
          image: ghcr.io/example/missing:1
`
)

// workloadClient pulls a manifest with images and resolves only nginx:1.27.
type workloadClient struct {
	*oci.FakeClient
	fs afero.Fs
	// onPull, when set, runs at each pull, while a render is in progress.
	onPull func()
}

func (c *workloadClient) Pull(_ context.Context, ref oci.Reference, targetDir string) (string, string, map[string]string, error) {
	c.Pulls = append(c.Pulls, ref)
	if c.onPull != nil {
		c.onPull()
	}
	if err := afero.WriteFile(c.fs, filepath.Join(targetDir, "manifest.yaml"), []byte(imagesManifest), 0600); err != nil {
		return "", "", nil, err
	}
	return "", oci.FakeDigest, nil, nil
}

// laggingCache reads like an informer cache that has not caught up with the
// last render: it serves the Order from a snapshot taken before, when one is
// set, and holds none of the Preparations.
type laggingCache struct {
	client.Client
	order *deliveryv1alpha1.Order
}

func (c *laggingCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch obj := obj.(type) {
	case *deliveryv1alpha1.Order:
		if c.order != nil && key == client.ObjectKeyFromObject(c.order) {
			c.order.DeepCopyInto(obj)
			return nil
		}
	case *deliveryv1alpha1.Preparation:
		return apierrors.NewNotFound(deliveryv1alpha1.GroupVersion.WithResource("preparations").GroupResource(), key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

// capturedEvent is an Event a capturingRecorder received.
type capturedEvent struct {
	regarding, related              runtime.Object
	eventType, reason, action, note string
}

// capturingRecorder records Events with the objects they name, which
// events.FakeRecorder reduces to a string.
type capturingRecorder struct {
	mu     sync.Mutex
	events []capturedEvent
}

var _ events.EventRecorder = (*capturingRecorder)(nil)

func (r *capturingRecorder) Eventf(regarding, related runtime.Object, eventType, reason, action, note string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, capturedEvent{
		regarding: regarding, related: related,
		eventType: eventType, reason: reason, action: action, note: fmt.Sprintf(note, args...),
	})
}

var _ = Describe("Order images", func() {
	ctx := context.Background()

	setupImages := func(productName, orderName string, images deliveryv1alpha1.ProductImages) {
		createTestProduct(ctx, productName, deliveryv1alpha1.OCISource{OCI: testOCIRef, Version: testVersion})
		DeferCleanup(deleteTestProduct, ctx, productName)

		product := getTestProduct(ctx, productName)
		product.Spec.Images = images
		Expect(k8sClient.Update(ctx, product)).To(Succeed())
		resolveTestProduct(ctx, productName, testOCIRef, testVersion)

		order := &deliveryv1alpha1.Order{
			Name:      orderName,
			Namespace: testNamespace,
			Spec:      deliveryv1alpha1.OrderSpec{ProductRef: deliveryv1alpha1.ProductRef{Name: productName}},
		}
		Expect(k8sClient.Create(ctx, order)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.DeleteAllOf(ctx, &deliveryv1alpha1.Preparation{}, client.InNamespace(testNamespace),
				client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})
			_ = k8sClient.Delete(ctx, order)
		})
	}

	newImagesReconciler := func(resolved map[string]oci.Descriptor) (*OrderReconciler, *workloadClient, *capturingRecorder) {
		fs := afero.NewMemMapFs()
		registry := &workloadClient{FakeClient: &oci.FakeClient{Resolved: resolved}, fs: fs}
		recorder := &capturingRecorder{}
		return &OrderReconciler{
			Client:           k8sClient,
			APIReader:        k8sClient,
			Scheme:           k8sClient.Scheme(),
			Pipeline:         artifact.NewPipeline(artifact.NewStore(registry, fs, "")),
			PantryResolver:   credential.NewKubeResolver(k8sClient),
			Recorder:         recorder,
			InstallNamespace: testNamespace,
			BuilderID:        testBuilderID,
		}, registry, recorder
	}

	// render reconciles the Order twice: once to add its finalizer, once to
	// render it.
	render := func(r *OrderReconciler, orderName string) error {
		key := types.NamespacedName{Namespace: testNamespace, Name: orderName}
		if _, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			return err
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		return err
	}

	reconcileImages := func(productName, orderName string, images deliveryv1alpha1.ProductImages) (*workloadClient, *capturingRecorder, error) {
		setupImages(productName, orderName, images)
		r, registry, recorder := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		})
		return registry, recorder, render(r, orderName)
	}

	preparationOf := func(orderName string) deliveryv1alpha1.Preparation {
		list := &deliveryv1alpha1.PreparationList{}
		Expect(k8sClient.List(ctx, list, client.InNamespace(testNamespace),
			client.MatchingLabels{deliveryv1alpha1.LabelOrder: orderName})).To(Succeed())
		Expect(list.Items).To(HaveLen(1))
		return list.Items[0]
	}

	It("records every image and warns about the ones it could not resolve", func() {
		registry, recorder, err := reconcileImages("images-record", "images-record", deliveryv1alpha1.ProductImages{})
		Expect(err).NotTo(HaveOccurred())

		prep := preparationOf("images-record")
		Expect(prep.Spec.Images).To(Equal([]deliveryv1alpha1.PreparationImage{
			{Ref: "ghcr.io/example/missing:1", Name: "ghcr.io/example/missing"},
			{Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		}))

		Expect(recorder.events).To(HaveLen(1), "one Event per render")
		event := recorder.events[0]
		Expect(event.eventType).To(Equal(corev1.EventTypeWarning))
		Expect(event.reason).To(Equal(deliveryv1alpha1.ReasonImageNotResolved))
		Expect(event.note).To(HavePrefix("Could not resolve 1 of 2 images: ghcr.io/example/missing:1: "))
		Expect(event.regarding.(*deliveryv1alpha1.Order).Name).To(Equal("images-record"))
		Expect(event.related).To(BeAssignableToTypeOf(&deliveryv1alpha1.Preparation{}))
		Expect(event.related.(*deliveryv1alpha1.Preparation).Name).To(Equal(prep.Name),
			"the Event names the Preparation, so client-go keeps the Events of two renders apart")

		pushed := registry.Pushes[len(registry.Pushes)-1]
		config, err := artifact.ParseConfig(pushed.Options.ConfigMediaType, pushed.Options.Config)
		Expect(err).NotTo(HaveOccurred())
		Expect(config.Images).To(HaveLen(2))
		Expect(config.Images[0].Error).To(ContainSubstring("not found"))
		Expect(string(config.Intent)).NotTo(ContainSubstring(`"images"`), "the default policy leaves the intent unchanged")
	})

	It("fails the render under Pin when an image cannot be resolved", func() {
		_, _, err := reconcileImages("images-pin", "images-pin", deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ghcr.io/example/missing:1"))

		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-pin"}, order)).To(Succeed())
		ready := meta.FindStatusCondition(order.Status.Conditions, deliveryv1alpha1.ConditionTypeReady)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Message).To(ContainSubstring("Pin image policy"))
	})

	It("fails the render when the pull Secret is missing", func() {
		_, _, err := reconcileImages("images-secret", "images-secret", deliveryv1alpha1.ProductImages{
			PullSecretRef: &corev1.LocalObjectReference{Name: "no-such-secret"},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("image pull Secret"))
	})

	It("puts the image policy into the intent", func() {
		registry, _, err := reconcileImages("images-intent", "images-intent", deliveryv1alpha1.ProductImages{
			FieldSpecs: []deliveryv1alpha1.ImageFieldSpec{{Kind: "Prometheus", Path: "spec/image"}},
		})
		Expect(err).NotTo(HaveOccurred())

		pushed := registry.Pushes[len(registry.Pushes)-1]
		config, err := artifact.ParseConfig(pushed.Options.ConfigMediaType, pushed.Options.Config)
		Expect(err).NotTo(HaveOccurred())

		var intent map[string]any
		Expect(json.Unmarshal(config.Intent, &intent)).To(Succeed())
		Expect(intent["images"]).To(Equal(map[string]any{
			"policy":     "Record",
			"fieldSpecs": []any{map[string]any{"kind": "Prometheus", "path": "spec/image"}},
		}))
	})

	It("keeps the digests of the latest Preparation when a Pin Order renders again", func() {
		const (
			movedDigest   = "sha256:cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
			missingDigest = "sha256:efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
		)
		setupImages("images-lock", "images-lock", deliveryv1alpha1.ProductImages{Policy: deliveryv1alpha1.ImagePolicyPin})
		r, registry, _ := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup:           {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
			"ghcr.io/example/missing:1": {Digest: missingDigest, MediaType: ocispec.MediaTypeImageManifest},
		})
		Expect(render(r, "images-lock")).To(Succeed())

		By("moving the tag upstream and changing the Order before the cache holds the first Preparation")
		registry.Resolved[imagesNginxLookup] = oci.Descriptor{Digest: movedDigest, MediaType: ocispec.MediaTypeImageIndex}
		order := &deliveryv1alpha1.Order{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-lock"}, order)).To(Succeed())
		first := order.Status.LatestPreparationName
		order.Spec.Edits = []deliveryv1alpha1.Patch{{
			Target: deliveryv1alpha1.PatchTarget{Kind: testDeploymentKind, Name: "web"},
			Set:    map[string]string{testReplicasPath: "3"},
		}}
		Expect(k8sClient.Update(ctx, order)).To(Succeed())
		registry.Resolves = nil
		lagging := *r
		lagging.Client = &laggingCache{Client: k8sClient}
		Expect(render(&lagging, "images-lock")).To(Succeed())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "images-lock"}, order)).To(Succeed())
		Expect(order.Status.LatestPreparationName).NotTo(Equal(first), "the edit renders a new Preparation")
		prep := &deliveryv1alpha1.Preparation{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: order.Status.LatestPreparationName}, prep)).To(Succeed())
		Expect(prep.Spec.Images).To(ContainElement(deliveryv1alpha1.PreparationImage{
			Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex,
		}), "the locked digest is kept, not the moved tag")
		Expect(registry.Resolves).To(BeEmpty(), "locked references are not looked up again")
	})

	It("does not render again while the cache still holds the Order as it was while rendering", func() {
		setupImages("images-stale", "images-stale", deliveryv1alpha1.ProductImages{})
		r, registry, _ := newImagesReconciler(map[string]oci.Descriptor{
			imagesNginxLookup: {Digest: imagesNginxDigest, MediaType: ocispec.MediaTypeImageIndex},
		})
		key := types.NamespacedName{Namespace: testNamespace, Name: "images-stale"}
		var rendering *deliveryv1alpha1.Order
		registry.onPull = func() {
			rendering = &deliveryv1alpha1.Order{}
			Expect(k8sClient.Get(ctx, key, rendering)).To(Succeed())
		}
		Expect(render(r, "images-stale")).To(Succeed())
		registry.onPull = nil
		Expect(registry.Pushes).To(HaveLen(1))
		Expect(rendering.Status.LatestPreparationName).To(BeEmpty(), "the snapshot predates the render's Ready write")

		By("reconciling with a cache that has not seen the render's Ready write")
		lagging := *r
		lagging.Client = &laggingCache{Client: k8sClient, order: rendering}
		_, err := lagging.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(registry.Pushes).To(HaveLen(1), "the Order is not rendered again")
		Expect(err).NotTo(HaveOccurred())
		preparationOf("images-stale")
	})
})

var _ = Describe("Image schema", func() {
	ctx := context.Background()

	It("accepts image digests with every algorithm the OCI image spec registers", func() {
		sha512 := "sha512:" + strings.Repeat("ab", 64)
		digest := "sha256:" + strings.Repeat("cd", 32)
		prep := &deliveryv1alpha1.Preparation{
			Name:      "images-sha512",
			Namespace: testNamespace,
			Spec: deliveryv1alpha1.PreparationSpec{
				OrderName: "images-sha512",
				Source:    deliveryv1alpha1.OrderSource{OCI: testOCIRef, BaseDigest: digest},
				Renderer: deliveryv1alpha1.Renderer{
					Type:    deliveryv1alpha1.RenderTypeManifest,
					Version: testRendererVersion,
					Builder: testBuilderID,
				},
				ConfigHash: testConfigHash,
				Artifact: deliveryv1alpha1.Artifact{
					OCIRef: "oci://registry.kokumi.svc.cluster.local:5000/images-sha512@" + digest,
					Digest: digest,
				},
				Images: []deliveryv1alpha1.PreparationImage{
					{Ref: "ghcr.io/example/app@" + sha512, Name: "ghcr.io/example/app", Digest: sha512},
					{Ref: imagesNginxRef, Name: imagesNginxName, Digest: imagesNginxDigest},
				},
			},
		}
		Expect(k8sClient.Create(ctx, prep)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, prep) })
	})

	It("requires mirror prefixes to start with a registry host", func() {
		kitchen := func(prefix string) *deliveryv1alpha1.Kitchen {
			return &deliveryv1alpha1.Kitchen{
				Name:      deliveryv1alpha1.DefaultKitchenName,
				Namespace: testNamespace,
				Spec: deliveryv1alpha1.KitchenSpec{Images: &deliveryv1alpha1.KitchenImages{
					Mirrors: []deliveryv1alpha1.ImageMirror{{Prefix: prefix, Location: "oci://harbor.example.com/hub"}},
				}},
			}
		}

		for _, prefix := range []string{"docker.io", "docker.io/library", "localhost:5000/app", "localhost", "registry:5000"} {
			Expect(k8sClient.Create(ctx, kitchen(prefix), client.DryRunAll)).To(Succeed(), prefix)
		}
		for _, prefix := range []string{"library/nginx", "nginx"} {
			Expect(k8sClient.Create(ctx, kitchen(prefix), client.DryRunAll)).To(
				MatchError(ContainSubstring("must start with a registry host")), prefix)
		}
	})
})

var _ = Describe("truncateNote", func() {
	It("keeps notes that fit", func() {
		Expect(truncateNote("short")).To(Equal("short"))
	})

	It("cuts long notes at a rune boundary to the limit of events.k8s.io", func() {
		note := truncateNote(strings.Repeat("ä", maxEventNote))
		Expect(len(note)).To(BeNumerically("<=", maxEventNote))
		Expect(utf8.ValidString(note)).To(BeTrue())
		Expect(note).To(HaveSuffix("…"))
	})
})
