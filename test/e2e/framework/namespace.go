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
	// deleted are the namespaces of the scenarios that ended, which may
	// still be terminating.
	deleted []string
)

// CreateNamespace creates a namespace of its own for the scenario that calls
// it, named e2e-<name>-<random suffix>, with the Cluster and the Environment
// of examples/local, so that its Orders deploy to the cluster Kokumi runs
// in, and deletes it when the scenario ends. Call it from BeforeAll or
// BeforeEach.
//
// The next scenario does not wait for the namespace to go: names never
// repeat, and the Argo CD Applications of a namespace are named after it.
// WaitForDeletedNamespaces waits for all of them before Kokumi is undeployed.
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
		deleted = append(deleted, ns.Name)
		namespacesMu.Unlock()

		Expect(client.IgnoreNotFound(Client.Delete(ctx, ns))).To(Succeed())
	})
	ApplyExample(ns.Name, "local")
	return ns.Name
}

// WaitForDeletedNamespaces waits until the namespaces of the scenarios that
// ended are gone. Their Kokumi objects carry finalizers that only a running
// Kokumi releases: Servings delete their Argo CD Applications, Clusters
// their registrations. Call it before Kokumi is undeployed.
func WaitForDeletedNamespaces() {
	namespacesMu.Lock()
	pending := slices.Clone(deleted)
	namespacesMu.Unlock()

	for _, name := range pending {
		WaitForNamespaceDeleted(name)
	}
}

// WaitForNamespaceDeleted waits until the namespace name is gone, for a
// scenario whose cleanup must run after the cleanup of its namespace.
func WaitForNamespaceDeleted(name string) {
	Eventually(func() error {
		return Client.Get(context.Background(), client.ObjectKey{Name: name}, &corev1.Namespace{})
	}, 3*time.Minute).Should(Satisfy(apierrors.IsNotFound), "The namespace %s is not deleted", name)
}

// scenarioNamespaces returns the namespaces of the scenarios that run.
func scenarioNamespaces() []string {
	namespacesMu.Lock()
	defer namespacesMu.Unlock()
	return slices.Clone(namespaces)
}
