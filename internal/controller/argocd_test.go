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
	"sigs.k8s.io/controller-runtime/pkg/event"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
)

var _ = Describe("Argo CD engine", func() {
	ctx := context.Background()
	env := &deliveryv1alpha1.Environment{
		Name: testEdge, Namespace: testWorkspace,
		Spec: deliveryv1alpha1.EnvironmentSpec{Deployer: deliveryv1alpha1.Deployer{
			Type: deliveryv1alpha1.DeployerTypeArgoCD, ArgoCD: &deliveryv1alpha1.ArgoCDDeployer{Project: argocd.DefaultProject},
		}},
	}

	It("admits nothing while Argo CD is not installed", func() {
		missing := &atomic.Bool{}
		missing.Store(true)
		engine := &argoCD{Client: withoutArgoCD{Client: k8sClient, missing: missing}, namespace: testKokumiNamespace}

		admitted, err := engine.Admit(ctx, env)
		Expect(err).NotTo(HaveOccurred())
		Expect(admitted.reason).To(Equal(deliveryv1alpha1.ReasonArgoCDNotInstalled))
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
