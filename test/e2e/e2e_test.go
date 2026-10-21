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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/test/e2e/framework"
	"github.com/kokumi-dev/kokumi/test/utils"
)

// namespace where the project is deployed in
const namespace = framework.InstallNamespace

// serviceAccountName created for the project
const serviceAccountName = "kokumi-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "kokumi-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "kokumi-metrics-binding"

var _ = Describe("Installation", Label("install"), Ordered, func() {
	var controllerPodName string

	AfterAll(func() {
		By("removing the curl-metrics Pod and its ClusterRoleBinding")
		_, _ = framework.Kubectl("delete", "pod", "curl-metrics", "-n", namespace, "--ignore-not-found")
		_, _ = framework.Kubectl("delete", "clusterrolebinding", metricsRoleBindingName, "--ignore-not-found")
	})

	It("runs the controller manager", func() {
		By("validating that the controller-manager pod is running as expected")
		verifyControllerUp := func(g Gomega) {
			// Get the name of the controller-manager pod
			cmd := exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)

			podOutput, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
			podNames := utils.GetNonEmptyLines(podOutput)
			g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
			controllerPodName = podNames[0]
			g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

			// Validate the pod's status
			cmd = exec.Command("kubectl", "get",
				"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
				"-n", namespace,
			)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
		}
		Eventually(verifyControllerUp).Should(Succeed())
	})

	It("serves metrics to a ServiceAccount allowed to read them", func() {
		By("creating a ClusterRoleBinding for the service account to allow access to metrics")
		cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
			"--clusterrole=kokumi-metrics-reader",
			fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
		)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

		By("validating that the metrics service is available")
		cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

		By("getting the service account token")
		token, err := serviceAccountToken()
		Expect(err).NotTo(HaveOccurred())
		Expect(token).NotTo(BeEmpty())

		By("ensuring the controller pod is ready")
		verifyControllerPodReady := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "Controller pod not ready")
		}
		Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

		By("verifying that the controller manager is serving the metrics server")
		verifyMetricsServerStarted := func(g Gomega) {
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(ContainSubstring("Serving metrics server"),
				"Metrics server not yet started")
		}
		Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

		// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

		By("creating the curl-metrics pod to access the metrics endpoint")
		curlMetrics := fmt.Sprintf("for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' "+
			"https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1",
			token, metricsServiceName, namespace)
		cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
			"--namespace", namespace,
			"--image=curlimages/curl:latest",
			"--overrides",
			fmt.Sprintf(`{
				"spec": {
					"containers": [{
						"name": "curl",
						"image": "curlimages/curl:latest",
						"command": ["/bin/sh", "-c"],
						"args": [%q],
						"securityContext": {
							"readOnlyRootFilesystem": true,
							"allowPrivilegeEscalation": false,
							"capabilities": {
								"drop": ["ALL"]
							},
							"runAsNonRoot": true,
							"runAsUser": 1000,
							"seccompProfile": {
								"type": "RuntimeDefault"
							}
						}
					}],
					"serviceAccountName": "%s"
				}
			}`, curlMetrics, serviceAccountName))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

		By("waiting for the curl-metrics pod to complete.")
		verifyCurlUp := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
				"-o", "jsonpath={.status.phase}",
				"-n", namespace)
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
		}
		Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

		By("getting the metrics by checking curl-metrics logs")
		verifyMetricsAvailable := func(g Gomega) {
			metricsOutput, err := getMetricsOutput()
			g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
			g.Expect(metricsOutput).NotTo(BeEmpty())
			g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
		}
		Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
	})

	It("serves the OCI referrers API from the bundled registry", func() {
		ctx := context.Background()
		client := framework.Registry()
		host := framework.RegistryHost()

		By("pushing an artifact with a config blob")
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("kind: ConfigMap\n"), 0o600)).To(Succeed())
		subject := framework.Ref(host + "/e2e/referrers:1.0.0")
		var err error
		subject.Digest, err = client.Push(ctx, subject, dir, oci.PushOptions{
			ArtifactType:    oci.PreparationArtifactType,
			Config:          []byte(`{"schemaVersion":1}`),
			ConfigMediaType: oci.PreparationConfigMediaType,
		})
		Expect(err).NotTo(HaveOccurred())

		By("attaching a referrer")
		referrerDigest, err := client.PushReferrer(ctx, subject, oci.ReferrerArtifact{
			ArtifactType: oci.SigstoreBundleMediaType,
			MediaType:    oci.SigstoreBundleMediaType,
			Payload:      []byte(`{}`),
		})
		Expect(err).NotTo(HaveOccurred())

		By("calling the referrers endpoint directly")
		resp, err := http.Get(fmt.Sprintf("http://%s/v2/e2e/referrers/referrers/%s", host, subject.Digest))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close() //nolint:errcheck
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var index struct {
			Manifests []struct {
				Digest       string `json:"digest"`
				ArtifactType string `json:"artifactType"`
			} `json:"manifests"`
		}
		Expect(json.NewDecoder(resp.Body).Decode(&index)).To(Succeed())
		Expect(index.Manifests).To(HaveLen(1))
		Expect(index.Manifests[0].Digest).To(Equal(referrerDigest))
		Expect(index.Manifests[0].ArtifactType).To(Equal(oci.SigstoreBundleMediaType))

		By("finding no referrers tag schema fallback tag")
		repository := subject
		repository.Tag, repository.Digest = "", ""
		tags, err := client.ListTags(ctx, repository)
		Expect(err).NotTo(HaveOccurred())
		Expect(tags).To(ConsistOf("1.0.0"))
	})

	// +kubebuilder:scaffold:e2e-webhooks-checks
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	tokenRequestFile := filepath.Join(GinkgoT().TempDir(), "token-request.json")
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
