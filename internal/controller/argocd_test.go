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
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/promotion"
)

// withoutArgoCD returns a client that answers every read of an Argo CD kind
// as the API server does while Argo CD's CRDs are not installed.
func withoutArgoCD() client.Client {
	noMatch := func(obj any) error {
		if _, ok := obj.(*unstructured.Unstructured); ok {
			return &meta.NoKindMatchError{GroupKind: argocd.GroupVersionKind.GroupKind()}
		}
		if _, ok := obj.(*unstructured.UnstructuredList); ok {
			return &meta.NoKindMatchError{GroupKind: argocd.GroupVersionKind.GroupKind()}
		}
		return nil
	}
	return fake.NewClientBuilder().WithScheme(scheme.Scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if err := noMatch(obj); err != nil {
				return err
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if err := noMatch(list); err != nil {
				return err
			}
			return c.List(ctx, list, opts...)
		},
	}).Build()
}

var _ = Describe("Argo CD engine", func() {
	ctx := context.Background()
	env := &deliveryv1alpha1.Environment{
		Name: testEdge, Namespace: testWorkspace,
		Spec: deliveryv1alpha1.EnvironmentSpec{Deployer: deliveryv1alpha1.Deployer{
			Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: argocd.DefaultProject},
		}},
	}
	serving := &deliveryv1alpha1.Serving{Name: "argo-web", Namespace: testWorkspace, Spec: deliveryv1alpha1.ServingSpec{OrderName: "argo-web"}}

	It("admits nothing, deploys nothing and has nothing to remove while Argo CD is not installed", func() {
		engine := &argoCD{Client: withoutArgoCD(), namespace: testKokumiNamespace}

		admitted, err := engine.Admit(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		Expect(admitted.reason).To(Equal(deliveryv1alpha1.ReasonArgoCDNotInstalled))

		state, err := engine.Apply(ctx, env, serving, &promotion.Entry{Preparation: "argo-web-p1", OCIRef: testOCIRef + "@" + testProductRevision},
			deployTarget{cluster: "in-cluster", server: "https://kubernetes.default.svc", namespace: testWorkspace})
		Expect(err).NotTo(HaveOccurred())
		Expect(state.held).To(Equal(deliveryv1alpha1.ReasonArgoCDNotInstalled))

		removed, err := engine.Remove(ctx, serving, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
	})

	It("starts an optional watch once the CRD of its kind is installed, and says so", func() {
		mapper := meta.NewDefaultRESTMapper(nil)
		w := &optionalWatch{obj: argocd.New(), gvk: argocd.GroupVersionKind, mapper: mapper,
			interval: 10 * time.Millisecond, started: make(chan event.GenericEvent, 1)}
		var watching atomic.Bool
		done := make(chan error, 1)
		watchCtx, stop := context.WithCancel(ctx)
		DeferCleanup(stop)
		go func() { done <- w.wait(watchCtx, func() error { watching.Store(true); return nil }) }()

		Consistently(watching.Load, 50*time.Millisecond).Should(BeFalse(), "no watch without the CRD")
		mapper.Add(argocd.GroupVersionKind, meta.RESTScopeNamespace)
		Eventually(w.Started()).Should(Receive())
		Expect(watching.Load()).To(BeTrue())
		Eventually(done).Should(Receive(BeNil()))
	})
})
