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

package framework

import (
	"context"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	namespacesMu sync.Mutex
	// namespaces are the namespaces of the scenarios that run, which a
	// failed spec dumps.
	namespaces []string
)

// CreateNamespace creates a namespace of its own for the scenario that calls
// it, named e2e-<name>-<random suffix>, and deletes it when the scenario
// ends. Call it from BeforeAll or BeforeEach.
//
// The cleanup waits until the namespace is gone: Argo CD Applications are
// named after their Serving alone, so the next scenario's Serving of the same
// name must not meet this scenario's Application.
func CreateNamespace(name string) string {
	ctx := context.Background()
	ns := &corev1.Namespace{GenerateName: "e2e-" + name + "-"}
	Expect(Client.Create(ctx, ns)).To(Succeed(), "Failed to create a namespace")

	namespacesMu.Lock()
	namespaces = append(namespaces, ns.Name)
	namespacesMu.Unlock()

	DeferCleanup(func() {
		namespacesMu.Lock()
		namespaces = slices.DeleteFunc(namespaces, func(n string) bool { return n == ns.Name })
		namespacesMu.Unlock()

		Expect(client.IgnoreNotFound(Client.Delete(ctx, ns))).To(Succeed())
		Eventually(func() error {
			return Client.Get(ctx, client.ObjectKeyFromObject(ns), &corev1.Namespace{})
		}, 3*time.Minute).Should(Satisfy(apierrors.IsNotFound), "The namespace %s is not deleted", ns.Name)
	})
	return ns.Name
}

// scenarioNamespaces returns the namespaces of the scenarios that run.
func scenarioNamespaces() []string {
	namespacesMu.Lock()
	defer namespacesMu.Unlock()
	return slices.Clone(namespaces)
}
