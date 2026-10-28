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
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/test/utils"
)

// ClusterTimeout covers what Kokumi checks of a cluster once a minute, such
// as whether it accepts its credential.
const ClusterTimeout = 3 * time.Minute

// Spoke is a Kind cluster of its own that Kokumi deploys to, besides the
// cluster it runs in.
type Spoke struct {
	// Name is the name of the Kind cluster.
	Name string
	// Server is its API server as Pods in the cluster Kokumi runs in reach
	// it: both clusters share Docker's kind network.
	Server string
	// Version is its Kubernetes version.
	Version string
	// Client reads and writes it.
	Client client.Client
}

// SpokeCluster returns the spoke Kind cluster, $KIND_CLUSTER-spoke,
// creating it on first use so that only the specs that need it pay for it.
// A failed run keeps it, as it keeps the cluster Kokumi runs in, and make
// cleanup-test-e2e deletes both.
func SpokeCluster() *Spoke {
	kind := cmp.Or(os.Getenv("KIND"), "kind")
	name := cmp.Or(os.Getenv("KIND_CLUSTER"), "kokumi-test-e2e") + "-spoke"
	kubeconfig := filepath.Join(ProjectDir(), "bin", name+".kubeconfig")

	clusters, err := utils.Run(exec.Command(kind, "get", "clusters"))
	Expect(err).NotTo(HaveOccurred(), "Failed to list the Kind clusters")
	if !slices.Contains(utils.GetNonEmptyLines(clusters), name) {
		By("creating the spoke Kind cluster " + name)
		create := exec.Command(kind, "create", "cluster", "--name", name, "--kubeconfig", kubeconfig, "--wait", "3m")
		_, err := utils.Run(create)
		Expect(err).NotTo(HaveOccurred(), "Failed to create the spoke Kind cluster")
	}
	_, err = utils.Run(exec.Command(kind, "export", "kubeconfig", "--name", name, "--kubeconfig", kubeconfig))
	Expect(err).NotTo(HaveOccurred(), "Failed to export the kubeconfig of the spoke Kind cluster")

	internal, err := utils.Run(exec.Command(kind, "get", "kubeconfig", "--internal", "--name", name))
	Expect(err).NotTo(HaveOccurred(), "Failed to read the internal kubeconfig of the spoke Kind cluster")
	internalConfig, err := clientcmd.Load([]byte(internal))
	Expect(err).NotTo(HaveOccurred())
	server := internalConfig.Clusters[internalConfig.Contexts[internalConfig.CurrentContext].Cluster].Server

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred())
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	version, err := discovery.NewDiscoveryClientForConfigOrDie(cfg).ServerVersion()
	Expect(err).NotTo(HaveOccurred())

	return &Spoke{Name: name, Server: server, Version: version.GitVersion, Client: c}
}

// spokeAccount is the ServiceAccount on a spoke cluster that Kokumi and
// Argo CD act as there, as hack/kind/spoke-cluster.sh creates it.
const spokeAccount = "kokumi"

// RegisterCluster registers spoke with Kokumi under name in namespace with
// hack/kind/spoke-cluster.sh, which make kind-spoke and the local demo run:
// a ServiceAccount with a token on the spoke, as the docs tell admins to
// create it, and the Secret <name>-kubeconfig in namespace with a kubeconfig
// of it. The Cluster itself comes from an example.
func RegisterCluster(namespace, name string, spoke *Spoke) {
	By("registering the spoke cluster as " + name + " of namespace " + namespace + " with hack/kind/spoke-cluster.sh")
	script := filepath.Join(ProjectDir(), "hack", "kind", "spoke-cluster.sh")
	_, err := utils.Run(exec.Command(script, spoke.Name, name, namespace))
	Expect(err).NotTo(HaveOccurred(), "Failed to register the spoke cluster")
	key := client.ObjectKey{Namespace: namespace, Name: name + "-kubeconfig"}
	Expect(Client.Get(context.Background(), key, &corev1.Secret{})).
		To(Succeed(), "The script stores the kubeconfig in the Secret %s-kubeconfig", name)
}

// GrantClusterCredentials binds the ClusterRole kokumi-cluster-credentials
// in namespace to the admin identity, as the operator guide tells operators
// to for each workspace whose Clusters the admin registers with a
// kubeconfig. It goes with the namespace.
func GrantClusterCredentials(namespace string) {
	By("binding kokumi-cluster-credentials to the admin identity in namespace " + namespace)
	_, err := Kubectl("create", "rolebinding", "kokumi-cluster-credentials", "-n", namespace,
		"--clusterrole=kokumi-cluster-credentials", "--serviceaccount="+InstallNamespace+":kokumi-admin")
	Expect(err).NotTo(HaveOccurred(), "Failed to bind kokumi-cluster-credentials in %s", namespace)
}

// RevokeCluster deletes the token of Kokumi's ServiceAccount on spoke, so
// that the spoke rejects the credential Kokumi holds.
func RevokeCluster(spoke *Spoke) {
	By("deleting the token of " + spokeAccount + " on the spoke cluster")
	Expect(spoke.Client.Delete(context.Background(), spokeTokenSecret())).To(Succeed())
}

// spokeTokenSecret is the token Secret of Kokumi's ServiceAccount on a
// spoke cluster.
func spokeTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		Name:        spokeAccount + "-token",
		Namespace:   metav1.NamespaceSystem,
		Annotations: map[string]string{corev1.ServiceAccountNameKey: spokeAccount},
		Type:        corev1.SecretTypeServiceAccountToken,
	}
}

// ApplyPlatform applies what an admin applies for the example name to
// namespace: kubectl apply -n <namespace> -k examples/<name>/platform/. It
// goes with the namespace.
func ApplyPlatform(namespace, name string) {
	dir := filepath.Join(ProjectDir(), "examples", name, "platform")
	By("applying what an admin applies for the " + name + " example: " +
		"kubectl apply -n " + namespace + " -k examples/" + name + "/platform/")
	_, err := Kubectl("apply", "-n", namespace, "-k", dir)
	Expect(err).NotTo(HaveOccurred(), "Failed to apply the platform of the %s example", name)
}

// Cluster returns a function for Eventually that fetches the Cluster name
// of namespace.
func Cluster(namespace, name string) func() (*deliveryv1alpha1.Cluster, error) {
	return Object[deliveryv1alpha1.Cluster](namespace, name)
}

// Environment returns a function for Eventually that fetches the
// Environment name of namespace.
func Environment(namespace, name string) func() (*deliveryv1alpha1.Environment, error) {
	return Object[deliveryv1alpha1.Environment](namespace, name)
}

// LabelCluster sets the label key of the Cluster name of namespace to
// value, or removes it when value is "", as an admin does to move a cluster
// into an Environment or out of it. The label is removed again when the
// scenario ends.
func LabelCluster(namespace, name, key, value string) {
	label := key + "=" + value
	if value == "" {
		label = key + "-"
	}
	By("labeling Cluster " + namespace + "/" + name + " " + label)
	_, err := Kubectl("label", "clusters.delivery.kokumi.dev", name, "-n", namespace, label, "--overwrite")
	Expect(err).NotTo(HaveOccurred(), "Failed to label Cluster %s", name)
	DeferCleanup(func() {
		_, _ = Kubectl("label", "clusters.delivery.kokumi.dev", name, "-n", namespace, key+"-")
	})
}

// HaveReady succeeds for an object, such as a Cluster or an Environment,
// whose Ready condition has status and reason.
func HaveReady(status metav1.ConditionStatus, reason string) OmegaMatcher {
	return SatisfyAll(HaveCondition(deliveryv1alpha1.ConditionTypeReady, status),
		HaveConditionReason(deliveryv1alpha1.ConditionTypeReady, reason))
}

// HaveSelectedClusters succeeds for an Environment that selects selected
// Clusters, ready of them Ready.
func HaveSelectedClusters(selected, ready int32) OmegaMatcher {
	return SatisfyAll(HaveField("Status.Clusters.Selected", selected), HaveField("Status.Clusters.Ready", ready))
}

// HaveDeploymentClusters succeeds for a Serving that deploys to selected
// clusters, healthy of which run the deployed Preparation healthily.
func HaveDeploymentClusters(selected, healthy int32) OmegaMatcher {
	return SatisfyAll(HaveField("Status.Clusters.Selected", selected), HaveField("Status.Clusters.Healthy", healthy))
}

// HaveTargets succeeds for a Serving whose status lists exactly clusters, in
// that order: the clusters that do not run the deployed Preparation
// healthily. A healthy cluster is counted, not listed.
func HaveTargets(clusters ...string) OmegaMatcher {
	matchers := make([]any, 0, len(clusters))
	for _, c := range clusters {
		matchers = append(matchers, HaveField("Cluster", c))
	}
	return HaveField("Status.Targets", HaveExactElements(matchers...))
}
