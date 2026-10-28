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
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/argocd"
	"github.com/kokumi-dev/kokumi/test/utils"
)

// InCluster is the Cluster of the cluster Kokumi runs in, as examples/local
// and Kokumi name it.
const InCluster = "in-cluster"

// ArgoCDApplicationCRD and ArgoCDAppProjectCRD are the CRDs of the Argo CD
// release "make argocd" installs that Kokumi uses: the Serving controller
// deploys through Applications, and the Environment controller checks
// AppProjects.
const (
	ArgoCDApplicationCRD = "https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/" +
		"manifests/crds/application-crd.yaml"
	ArgoCDAppProjectCRD = "https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/" +
		"manifests/crds/appproject-crd.yaml"
)

// ecrPublicDockerHub is where ECR Public mirrors the images of Docker Hub,
// such as the Redis image of Argo CD.
const ecrPublicDockerHub = "public.ecr.aws/docker/"

// RealArgoCD reports whether the suite runs a real Argo CD, E2E_ARGOCD=real,
// instead of only its Application CRD. By default SyncHealthy simulates Argo
// CD's sync and health reports, so that promotion gates run deterministically
// and fast.
func RealArgoCD() bool {
	return os.Getenv("E2E_ARGOCD") == "real"
}

// InstallArgoCD installs Argo CD with "make argocd" and waits until all of
// its workloads run.
//
// Argo CD pulls its Redis image from ECR Public, which rate-limits anonymous
// pulls, and CI runners share their IPs. Without Redis, Argo CD cannot
// generate manifests, and every Application stays Unknown. So the suite has
// Redis pull the same image from Docker Hub instead.
func InstallArgoCD() {
	_, err := utils.Run(exec.Command("make", "argocd"))
	Expect(err).NotTo(HaveOccurred(), "Failed to install Argo CD")

	image, err := Kubectl("get", "deployment/argocd-redis", "-n", ArgoCDNamespace,
		"-o", `jsonpath={.spec.template.spec.containers[?(@.name=="redis")].image}`)
	Expect(err).NotTo(HaveOccurred(), "Failed to read the Redis image of Argo CD")
	name, mirrored := strings.CutPrefix(strings.TrimSpace(image), ecrPublicDockerHub)
	Expect(mirrored).To(BeTrue(), "The Redis image of Argo CD, %s, is not a Docker Hub image on ECR Public", image)

	By("having the Redis of Argo CD pull its image from Docker Hub")
	_, err = Kubectl("set", "image", "deployment/argocd-redis", "-n", ArgoCDNamespace, "redis=docker.io/"+name)
	Expect(err).NotTo(HaveOccurred(), "Failed to set the Redis image of Argo CD")

	workloads, err := Kubectl("get", "deployments,statefulsets", "-n", ArgoCDNamespace, "-o", "name")
	Expect(err).NotTo(HaveOccurred(), "Failed to list the workloads of Argo CD")
	for _, workload := range utils.GetNonEmptyLines(workloads) {
		_, err = Kubectl("rollout", "status", workload, "-n", ArgoCDNamespace, "--timeout=5m")
		Expect(err).NotTo(HaveOccurred(), "Argo CD's %s did not become ready", workload)
	}
}

// ApplicationKey returns the key of the Argo CD Application through which
// the Order order in namespace deploys to cluster, as Kokumi names it.
func ApplicationKey(namespace, order, cluster string) client.ObjectKey {
	serving := &deliveryv1alpha1.Serving{Namespace: namespace, Spec: deliveryv1alpha1.ServingSpec{OrderName: order}}
	return argocd.Key(serving, cluster)
}

// SimulateArgoCD installs what Kokumi uses of Argo CD, without Argo CD: its
// Application and AppProject CRDs, and its namespace with the project
// default that every Argo CD creates, as "make argocd" configures it.
func SimulateArgoCD() {
	for _, crd := range []string{ArgoCDApplicationCRD, ArgoCDAppProjectCRD} {
		_, err := Kubectl("apply", "--server-side", "-f", crd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install %s", crd)
	}
	out, err := Kubectl("create", "ns", ArgoCDNamespace)
	if err != nil {
		Expect(out).To(ContainSubstring("AlreadyExists"))
	}
	ApplyTestdata("", "argocd/default-project.yaml", nil)
}

// RemoveSimulatedArgoCD removes what SimulateArgoCD installed.
func RemoveSimulatedArgoCD() {
	for _, crd := range []string{ArgoCDApplicationCRD, ArgoCDAppProjectCRD} {
		_, _ = Kubectl("delete", "--ignore-not-found", "-f", crd)
	}
}

// Application returns a function for Eventually that fetches the Argo CD
// Application through which the Order order in namespace deploys to the
// cluster Kokumi runs in.
func Application(namespace, order string) func() (*unstructured.Unstructured, error) {
	return ApplicationOn(namespace, order, InCluster)
}

// ApplicationOn returns a function for Eventually that fetches the Argo CD
// Application through which the Order order in namespace deploys to cluster.
func ApplicationOn(namespace, order, cluster string) func() (*unstructured.Unstructured, error) {
	return func() (*unstructured.Unstructured, error) {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
		err := Client.Get(context.Background(), ApplicationKey(namespace, order, cluster), app)
		return app, err
	}
}

// WaitForDeployment waits until the Order order in namespace deploys
// digest, a Preparation's, to the cluster Kokumi runs in through Argo CD.
func WaitForDeployment(namespace, order, digest string) {
	WaitForDeploymentOn(namespace, order, InCluster, digest)
}

// WaitForDeploymentOn waits until the Order order in namespace deploys
// digest, a Preparation's, to cluster through Argo CD.
func WaitForDeploymentOn(namespace, order, cluster, digest string) {
	Eventually(ApplicationOn(namespace, order, cluster), 2*time.Minute).Should(
		HaveField("Object", HaveKeyWithValue("spec", HaveKeyWithValue("source", HaveKeyWithValue("targetRevision", digest)))),
		"The Order %s/%s does not deploy %s to cluster %s through Argo CD", namespace, order, digest, cluster)
}

// ExpectNoApplication fails if the Order order in namespace deploys
// anything to the cluster Kokumi runs in through Argo CD.
func ExpectNoApplication(namespace, order string) {
	ExpectNoApplicationOn(namespace, order, InCluster)
}

// ExpectNoApplicationOn fails if the Order order in namespace deploys
// anything to cluster through Argo CD.
func ExpectNoApplicationOn(namespace, order, cluster string) {
	_, err := ApplicationOn(namespace, order, cluster)()
	Expect(apierrors.IsNotFound(err)).To(BeTrue(),
		"The Order %s/%s deploys to cluster %s through Argo CD: %v", namespace, order, cluster, err)
}

// simulateHealthy reports the Argo CD Application app synced to revision
// and healthy, as Argo CD's application controller would after comparing
// its current source. The Application CRD has no status subresource, so a
// patch writes the status.
func simulateHealthy(app client.ObjectKey, revision string) {
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
	Expect(Client.Get(context.Background(), app, current)).To(Succeed())
	source, _, _ := unstructured.NestedMap(current.Object, "spec", "source")
	destination, _, _ := unstructured.NestedMap(current.Object, "spec", "destination")
	comparedTo := map[string]any{"source": source, "destination": destination}
	const status = "status"
	patch, err := json.Marshal(map[string]any{status: map[string]any{
		"sync":   map[string]any{status: "Synced", "revision": revision, "comparedTo": comparedTo},
		"health": map[string]any{status: "Healthy"},
	}})
	Expect(err).NotTo(HaveOccurred())
	_, err = Kubectl("patch", "applications.argoproj.io", app.Name, "-n", app.Namespace,
		"--type=merge", "-p", string(patch))
	Expect(err).NotTo(HaveOccurred(), "Failed to simulate Argo CD reporting %s healthy", app.Name)
}

// SyncHealthy has Argo CD report the Application through which the Order
// order in namespace deploys to the cluster Kokumi runs in synced to
// revision and healthy.
func SyncHealthy(namespace, order, revision string) {
	SyncHealthyOn(namespace, order, InCluster, revision)
}

// SyncHealthyOn has Argo CD report the Application through which the Order
// order in namespace deploys to cluster synced to revision and healthy. The
// suite simulates the report by default; with E2E_ARGOCD=real it waits for
// Argo CD to deploy the revision for real.
func SyncHealthyOn(namespace, order, cluster, revision string) {
	key := ApplicationKey(namespace, order, cluster)
	if !RealArgoCD() {
		By("simulating Argo CD reporting " + key.Name + " synced and healthy")
		simulateHealthy(key, revision)
		return
	}
	By("waiting for Argo CD to report " + key.Name + " synced and healthy")
	Eventually(func(g Gomega) {
		app, err := ApplicationOn(namespace, order, cluster)()
		g.Expect(err).NotTo(HaveOccurred())
		sync, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
		synced, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
		health, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
		g.Expect([]string{sync, synced, health}).To(Equal([]string{"Synced", revision, "Healthy"}))
	}, 5*time.Minute, 2*time.Second).Should(Succeed(), "Argo CD did not deploy %s healthily", key.Name)
}
