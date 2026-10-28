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
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/internal/artifact"
	"github.com/kokumi-dev/kokumi/internal/cluster"
	"github.com/kokumi-dev/kokumi/internal/conditions"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/registry"
	"github.com/kokumi-dev/kokumi/internal/resources"
	"github.com/kokumi-dev/kokumi/internal/signing"
	"github.com/kokumi-dev/kokumi/internal/signing/signingtest"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client

	// testSigner signs the evidence of test artifacts; testSigning provides
	// it to the controllers under test.
	testSigner  *signing.Signer
	testSigning *signing.Provider
)

const (
	testOCIRef          = "oci://registry.kokumi.svc.cluster.local:5000/order/test-resource"
	testVersion         = "0.1.0"
	testNamespace       = "default"
	testIssuer          = "https://issuer.example"
	testApproverGroup   = "release-approvers"
	testIneligibleGroup = "other"
	testRendererVersion = "v1.0.0"
	testConfigHash      = "sha256:abc123"
	testProductRevision = "sha256:7777777777777777777777777777777777777777777777777777777777777777"
	testBuilderID       = "ghcr.io/kokumi-dev/kokumi@sha256:bb"
	testReadyReason     = "Ready"
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = deliveryv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("testdata"),
		},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if getFirstFoundEnvTestBinaryDir() != "" {
		testEnv.BinaryAssetsDirectory = getFirstFoundEnvTestBinaryDir()
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	testSigner = signingtest.NewSigner(GinkgoT())
	testSigning = signingtest.NewProvider(GinkgoT(), testSigner)

	for _, name := range []string{testWorkspace, testKokumiNamespace, argoNamespace} {
		Expect(k8sClient.Create(ctx, &corev1.Namespace{Name: name})).To(Succeed())
	}
	createDefaultProject(ctx)
	createLocalEnvironment(ctx, testNamespace)
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, time.Minute, time.Second).Should(Succeed())
})

// testWorkspace is the namespace of the Clusters and Environments of the
// tests of their controllers.
const testWorkspace = "workspace"

// testKokumiNamespace is Kokumi's own namespace in the tests, where it keeps
// the Cluster in-cluster and the Environment local.
const testKokumiNamespace = "kokumi-system"

// testEdge is the value of the label by which the tests put clusters into
// the Environment they deploy to.
const testEdge = "edge"

// testRetired is the value of that label for clusters the tests take out
// of it.
const testRetired = "retired"

// testFleet is the label by which the tests of mapping functions select
// Clusters.
const testFleet = "fleet"

// Keys of the Argo CD objects the tests write as Argo CD or its users would.
const (
	keyName      = "name"
	keyServer    = "server"
	keyNamespace = "namespace"
	keyRevision  = "revision"
)

// createDefaultProject creates Argo CD's AppProject default, which every
// Argo CD creates, admitting Applications of every namespace, as make argocd
// and the e2e suite configure it for development.
func createDefaultProject(ctx context.Context) {
	p := newAppProject()
	p.SetNamespace(argoNamespace)
	p.SetName(argocd.DefaultProject)
	Expect(unstructured.SetNestedSlice(p.Object, []any{map[string]any{keyServer: "*", keyNamespace: "*"}}, "spec", "destinations")).To(Succeed())
	Expect(unstructured.SetNestedStringSlice(p.Object, []string{"*"}, "spec", "sourceNamespaces")).To(Succeed())
	Expect(k8sClient.Create(ctx, p)).To(Succeed())
}

// createLocalEnvironment creates, in namespace, the Cluster in-cluster,
// Ready as its controller reports it, and the Environment local that
// selects it, as Kokumi keeps them in its own namespace: Orders in
// namespace then deploy to the cluster Kokumi runs in.
func createLocalEnvironment(ctx context.Context, namespace string) {
	c := resources.InClusterCluster(namespace)
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, c))).To(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
	c.Status.Server = cluster.InClusterServer
	conditions.MarkTrue(c, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady, "The control plane is ready")
	Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())

	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, resources.LocalEnvironment(namespace)))).To(Succeed())
}

// localEnvironment is the reference of an Order to the Environment local of
// its namespace.
var localEnvironment = deliveryv1alpha1.EnvironmentRef{Name: cluster.LocalEnvironment}

// fakeProber probes clusters without a network: every cluster has health.
type fakeProber struct {
	health cluster.Health
}

func (f *fakeProber) Probe(context.Context, *rest.Config) cluster.Health {
	return f.health
}

// fakeRegistries returns a Resolver that reads Registries with k8sClient
// and serves every connection with fake.
func fakeRegistries(fake oci.Client) *registry.Resolver {
	return testRegistries(func(oci.Options) oci.Client { return fake })
}

// testRegistries returns a Resolver that reads Registries with k8sClient
// and builds every client with newClient.
func testRegistries(newClient func(oci.Options) oci.Client) *registry.Resolver {
	return registry.NewResolver(registry.NewClients(artifact.DefaultRegistryHost).WithClientFactory(newClient), RegistryReader(k8sClient))
}

// getFirstFoundEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// Makefile targets, the 'BinaryAssetsDirectory' must be explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'make setup-envtest' beforehand.
func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
