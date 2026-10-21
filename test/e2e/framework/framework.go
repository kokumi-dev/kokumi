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

// Package framework holds the mechanics the e2e specs share: the typed
// client, namespaces per scenario, examples and fixtures, the bundled
// registry, cosign, the simulated Argo CD and the diagnostics of a failed
// spec. It keeps the specs free to read as scenarios.
package framework

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	"sigs.k8s.io/controller-runtime/pkg/log"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/utils"
)

const (
	// InstallNamespace is the namespace "make deploy" installs kokumi into.
	InstallNamespace = "kokumi"

	// InClusterRegistry is the bundled registry as the controller and the
	// nodes reach it.
	InClusterRegistry = "kokumi-registry.kokumi.svc.cluster.local:5000"

	// ArgoCDNamespace holds the Argo CD Applications of all Servings.
	ArgoCDNamespace = "argocd"
)

// Timeouts for Eventually, by what a spec waits for. The default, for
// anything else, is set by the suite.
const (
	// RenderTimeout covers pulling a source and pushing a Preparation.
	RenderTimeout = 5 * time.Minute
	// ScanTimeout covers downloading the vulnerability database and scanning.
	ScanTimeout = 15 * time.Minute
)

var (
	// Client reads and writes the cluster with kokumi's types.
	Client client.Client

	// restConfig is the config of Client, which port-forwards dial with.
	restConfig *rest.Config

	k komega.Komega
)

// Connect creates the Client from the current kubeconfig context.
func Connect() {
	log.SetLogger(GinkgoLogr)

	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(deliveryv1alpha1.AddToScheme(scheme)).To(Succeed())

	cfg, err := config.GetConfig()
	Expect(err).NotTo(HaveOccurred(), "Failed to read the kubeconfig")
	restConfig = cfg
	Client, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred(), "Failed to create the client")
	k = komega.New(Client).WithContext(context.Background())
}

// Object returns a function for Eventually that fetches name in namespace
// as a T, e.g. Eventually(Object[deliveryv1alpha1.Serving](ns, "prod")).
func Object[T any, PT interface {
	*T
	client.Object
}](namespace, name string) func() (PT, error) {
	return func() (PT, error) {
		obj := PT(new(T))
		obj.SetNamespace(namespace)
		obj.SetName(name)
		err := k.Get(obj)()
		return obj, err
	}
}

// Product returns a function for Eventually that fetches the Product name.
func Product(namespace, name string) func() (*deliveryv1alpha1.Product, error) {
	return Object[deliveryv1alpha1.Product](namespace, name)
}

// Order returns a function for Eventually that fetches the Order name.
func Order(namespace, name string) func() (*deliveryv1alpha1.Order, error) {
	return Object[deliveryv1alpha1.Order](namespace, name)
}

// Preparation returns a function for Eventually that fetches the Preparation
// name.
func Preparation(namespace, name string) func() (*deliveryv1alpha1.Preparation, error) {
	return Object[deliveryv1alpha1.Preparation](namespace, name)
}

// Serving returns a function for Eventually that fetches the Serving name.
func Serving(namespace, name string) func() (*deliveryv1alpha1.Serving, error) {
	return Object[deliveryv1alpha1.Serving](namespace, name)
}

// Update fetches name in namespace as a T, applies mutate to it and writes it
// back, retrying on conflicts.
func Update[T any, PT interface {
	*T
	client.Object
}](namespace, name string, mutate func(PT)) {
	obj := PT(new(T))
	obj.SetNamespace(namespace)
	obj.SetName(name)
	Eventually(k.Update(obj, func() { mutate(obj) })).Should(Succeed())
}

// Preparations returns a function for Eventually that lists the Preparations
// in namespace, of the Order order when it is not empty.
func Preparations(namespace, order string) func() ([]deliveryv1alpha1.Preparation, error) {
	return func() ([]deliveryv1alpha1.Preparation, error) {
		opts := []client.ListOption{client.InNamespace(namespace)}
		if order != "" {
			opts = append(opts, client.MatchingLabels{deliveryv1alpha1.LabelOrder: order})
		}
		var list deliveryv1alpha1.PreparationList
		err := k.List(&list, opts...)()
		return list.Items, err
	}
}

// Kubectl runs kubectl with args and returns its combined output.
func Kubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", args...))
}

// KubectlAs runs kubectl with args as the ServiceAccount serviceAccount of
// the install namespace.
func KubectlAs(serviceAccount string, args ...string) (string, error) {
	return Kubectl(append(args, "--as=system:serviceaccount:"+InstallNamespace+":"+serviceAccount)...)
}

// ProjectDir returns the root of the repository.
func ProjectDir() string {
	dir, err := utils.GetProjectDir()
	Expect(err).NotTo(HaveOccurred())
	return dir
}

// TestdataKey returns the path of the testdata signing key, private or
// public, in hack/testdata.
func TestdataKey(file string) string {
	return filepath.Join(ProjectDir(), "hack", "testdata", file)
}
