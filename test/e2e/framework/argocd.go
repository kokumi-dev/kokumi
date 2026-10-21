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
	"fmt"
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

	"github.com/kokumi-dev/kokumi/test/utils"
)

// ArgoCDApplicationCRD is the Application CRD of the Argo CD release
// "make argocd" installs. The Serving controller watches Applications and
// does not start without it.
const ArgoCDApplicationCRD = "https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/" +
	"manifests/crds/application-crd.yaml"

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

// Application returns a function for Eventually that fetches the Argo CD
// Application of the Serving name.
func Application(name string) func() (*unstructured.Unstructured, error) {
	return func() (*unstructured.Unstructured, error) {
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"})
		err := Client.Get(context.Background(), client.ObjectKey{Namespace: ArgoCDNamespace, Name: name}, app)
		return app, err
	}
}

// WaitForDeployment waits until the Argo CD Application of the Serving name
// deploys digest, a Preparation's.
func WaitForDeployment(name, digest string) {
	Eventually(Application(name), 2*time.Minute).Should(
		HaveField("Object", HaveKeyWithValue("spec", HaveKeyWithValue("source", HaveKeyWithValue("targetRevision", digest)))),
		"The Serving %s does not deploy %s through Argo CD", name, digest)
}

// ExpectNoApplication fails if the Serving name deploys anything through
// Argo CD.
func ExpectNoApplication(name string) {
	_, err := Application(name)()
	Expect(apierrors.IsNotFound(err)).To(BeTrue(), "The Serving %s deploys through Argo CD: %v", name, err)
}

// simulateHealthy reports the Argo CD Application of the Serving name synced
// to revision and healthy, as Argo CD's application controller would. The
// Application CRD has no status subresource, so a patch writes the status.
func simulateHealthy(name, revision string) {
	_, err := Kubectl("patch", "applications.argoproj.io", name, "-n", ArgoCDNamespace, "--type=merge", "-p",
		fmt.Sprintf(`{"status":{"sync":{"status":"Synced","revision":%q},"health":{"status":"Healthy"}}}`, revision))
	Expect(err).NotTo(HaveOccurred(), "Failed to simulate Argo CD reporting %s healthy", name)
}

// SyncHealthy has Argo CD report the Application of the Serving name synced
// to revision and healthy. The suite simulates the report by default; with
// E2E_ARGOCD=real it waits for Argo CD to deploy the revision for real.
func SyncHealthy(name, revision string) {
	if !RealArgoCD() {
		By("simulating Argo CD reporting " + name + " synced and healthy")
		simulateHealthy(name, revision)
		return
	}
	By("waiting for Argo CD to report " + name + " synced and healthy")
	Eventually(func(g Gomega) {
		app, err := Application(name)()
		g.Expect(err).NotTo(HaveOccurred())
		sync, _, _ := unstructured.NestedString(app.Object, "status", "sync", "status")
		synced, _, _ := unstructured.NestedString(app.Object, "status", "sync", "revision")
		health, _, _ := unstructured.NestedString(app.Object, "status", "health", "status")
		g.Expect([]string{sync, synced, health}).To(Equal([]string{"Synced", revision, "Healthy"}))
	}, 5*time.Minute, 2*time.Second).Should(Succeed(), "Argo CD did not deploy %s healthily", name)
}
