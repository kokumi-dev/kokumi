//go:build e2e

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

package e2e

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kokumi-dev/kokumi/test/e2e/framework"
	"github.com/kokumi-dev/kokumi/test/utils"
)

// managerImage is the manager image to be built and loaded for testing.
var managerImage = "example.com/kokumi:v0.0.1"

// TestE2E runs the e2e test suite to validate the solution in an isolated
// environment: a Kind cluster of its own. See README.md.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting kokumi e2e test suite\n")
	RunSpecs(t, "e2e suite")
}

// BeforeSuite installs kokumi once for all specs, as "make deploy" installs
// it, with the image built from this checkout.
var _ = BeforeSuite(func() {
	By("building the manager image")
	_, err := utils.Run(exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", managerImage)))
	Expect(err).NotTo(HaveOccurred(), "Failed to build the manager image")

	By("loading the manager image on Kind")
	Expect(utils.LoadImageToKindClusterWithName(managerImage)).To(Succeed(), "Failed to load the manager image into Kind")

	By("creating the install namespace, which enforces the restricted Pod security standard")
	out, err := framework.Kubectl("create", "ns", framework.InstallNamespace)
	if err != nil {
		Expect(out).To(ContainSubstring("AlreadyExists"))
	}
	_, err = framework.Kubectl("label", "--overwrite", "ns", framework.InstallNamespace,
		"pod-security.kubernetes.io/enforce=restricted")
	Expect(err).NotTo(HaveOccurred())

	By("installing CRDs")
	_, err = utils.Run(exec.Command("make", "install"))
	Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

	if framework.RealArgoCD() {
		By("installing Argo CD")
		_, err = utils.Run(exec.Command("make", "argocd"))
		Expect(err).NotTo(HaveOccurred(), "Failed to install Argo CD")
		_, err = framework.Kubectl("rollout", "status", "statefulset/argocd-application-controller",
			"-n", framework.ArgoCDNamespace, "--timeout=5m")
		Expect(err).NotTo(HaveOccurred(), "Argo CD did not become ready")
	} else {
		By("installing only the Argo CD Application CRD; specs simulate Argo CD")
		_, err = framework.Kubectl("apply", "--server-side", "-f", framework.ArgoCDApplicationCRD)
		Expect(err).NotTo(HaveOccurred(), "Failed to install the Argo CD Application CRD")
		out, err = framework.Kubectl("create", "ns", framework.ArgoCDNamespace)
		if err != nil {
			Expect(out).To(ContainSubstring("AlreadyExists"))
		}
	}

	By("deploying kokumi")
	_, err = utils.Run(exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage)))
	Expect(err).NotTo(HaveOccurred(), "Failed to deploy kokumi")
	components := []string{"kokumi-controller-manager", "kokumi-server", "kokumi-registry", "kokumi-scanner"}
	for _, deployment := range components {
		_, err = framework.Kubectl("rollout", "status", "deployment/"+deployment,
			"-n", framework.InstallNamespace, "--timeout=5m")
		Expect(err).NotTo(HaveOccurred(), "%s did not become ready", deployment)
	}

	framework.Connect()
	framework.ForwardRegistry()
})

// AfterSuite removes what BeforeSuite installed, so that the suite can run
// again on the same cluster. After a failed spec it keeps Kokumi installed,
// so that its Pods are still there for "kind export logs" and for a look at
// the cluster, which make test-e2e keeps then.
var _ = AfterSuite(func() {
	if framework.SpecFailed() {
		By("keeping kokumi installed for inspection, since a spec failed")
		return
	}

	By("undeploying kokumi")
	_, _ = utils.Run(exec.Command("make", "undeploy"))

	By("uninstalling CRDs")
	_, _ = utils.Run(exec.Command("make", "uninstall"))
	if !framework.RealArgoCD() {
		_, _ = framework.Kubectl("delete", "--ignore-not-found", "-f", framework.ArgoCDApplicationCRD)
	}

	By("removing the install namespace")
	_, _ = framework.Kubectl("delete", "ns", framework.InstallNamespace, "--ignore-not-found")
})

// Every spec that fails writes the state of the cluster to $ARTIFACTS before
// its scenario cleans up.
var _ = AfterEach(func() {
	framework.DumpOnFailure()
})
