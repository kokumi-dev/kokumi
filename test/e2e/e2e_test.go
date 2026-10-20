//go:build e2e
// +build e2e

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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
	"github.com/kokumi-dev/kokumi/internal/vulnerability"
	"github.com/kokumi-dev/kokumi/test/utils"
)

// namespace where the project is deployed in
const namespace = "kokumi"

// serviceAccountName created for the project
const serviceAccountName = "kokumi-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "kokumi-controller-manager-metrics-service"

// registryDeploymentName is the name of the bundled OCI registry Deployment
const registryDeploymentName = "kokumi-registry"

// registryServiceName is the name of the bundled OCI registry Service
const registryServiceName = "kokumi-registry"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "kokumi-metrics-binding"

// signingKeySecretName is the Secret holding the Kitchen signing key, in
// signingKeyNamespace.
const (
	signingKeySecretName = "kokumi-signing-key"
	signingKeyNamespace  = "kokumi-signing"
)

// inClusterRegistry is the bundled registry as the controller reaches it.
const inClusterRegistry = "kokumi-registry.kokumi.svc.cluster.local:5000"

// signingNamespace holds the Products and Orders of the signing specs.
const signingNamespace = "e2e-signing"

// imagesNamespace holds the Products, Orders and Pods of the image specs.
const imagesNamespace = "e2e-images"

// upstreamImage is the image the vendoring spec copies into the bundled
// registry. It has Linux platforms only, so every layer can be copied.
const upstreamImage = "ghcr.io/stefanprodan/podinfo:6.14.1"

// argoCDApplicationCRD is the Application CRD of the Argo CD release
// "make argocd" installs. The Serving controller watches Applications and
// does not start without it; the suite runs no Argo CD.
const argoCDApplicationCRD = "https://raw.githubusercontent.com/argoproj/argo-cd/v3.5.1/manifests/crds/application-crd.yaml"

// scanningNamespace holds the Products, Orders and Pods of the scanning spec.
const scanningNamespace = "e2e-scanning"

// pipelineNamespace holds the Product and Orders of the pipeline spec.
const pipelineNamespace = "e2e-pipeline"

// argoCDNamespace holds the Applications of all Servings.
const argoCDNamespace = "argocd"

// scannerDeploymentName is the kokumi-scanner Deployment, and its Service.
const scannerDeploymentName = "kokumi-scanner"

// vulnerableImage is alpine 3.14.0, pinned: its apk-tools and OpenSSL have
// known Critical vulnerabilities that later releases fix
// (CVE-2021-36159, CVE-2021-3711). It has Linux platforms only.
const vulnerableImage = "mirror.gcr.io/library/alpine@sha256:adab3844f497ab9171f070d4cae4114b5aec565ac772e2f2579405b78be67c96"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("installing the Argo CD Application CRD")
		cmd = exec.Command("kubectl", "apply", "--server-side", "-f", argoCDApplicationCRD)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install the Argo CD Application CRD")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("removing the signing and images namespaces")
		cmd = exec.Command("kubectl", "delete", "ns", signingNamespace, imagesNamespace, scanningNamespace, pipelineNamespace,
			"--ignore-not-found", "--timeout=2m")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)
		cmd = exec.Command("kubectl", "delete", "--ignore-not-found", "-f", argoCDApplicationCRD)
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
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

		It("should ensure the metrics endpoint is serving metrics", func() {
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
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
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
				}`, token, metricsServiceName, namespace, serviceAccountName))
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

		It("should serve the OCI referrers API from the bundled registry", func() {
			By("waiting for the registry to be ready")
			cmd := exec.Command("kubectl", "rollout", "status", "deployment/"+registryDeploymentName,
				"-n", namespace, "--timeout=3m")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Registry did not become ready")

			By("forwarding a local port to the registry")
			host := forwardRegistry()

			ctx := context.Background()
			client := oci.NewClient(oci.Options{PlainHTTP: true})

			By("pushing an artifact with a config blob")
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte("kind: ConfigMap\n"), 0o600)).To(Succeed())

			subject, err := oci.Parse(host + "/e2e/referrers:1.0.0")
			Expect(err).NotTo(HaveOccurred())
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

		It("should sign every Preparation so that stock cosign verifies it offline", func() {
			cosign := cosignBinary()
			host := forwardRegistry()

			By("pushing a source to the bundled registry")
			pushSource(host, "e2e/source")

			By("rendering an Order of a Product of it")
			createNamespace(signingNamespace)
			apply(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: signed
  namespace: %[1]s
spec:
  source:
    url: oci://%[2]s/e2e/source
    version: "1.0.0"
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: signed
  namespace: %[1]s
spec:
  productRef:
    name: signed
  destination:
    url: oci://%[2]s/e2e/rendered
`, signingNamespace, inClusterRegistry))

			var digest string
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "preparations", "-n", signingNamespace,
					"-l", "delivery.kokumi.dev/order=signed", "-o",
					`jsonpath={range .items[*]}{.spec.artifact.digest} {.status.conditions[?(@.type=='Signed')].status} {.status.conditions[?(@.type=='Ready')].status}{"\n"}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				fields := strings.Fields(out)
				g.Expect(fields).To(HaveLen(3), "one Preparation: "+out)
				g.Expect(fields[1:]).To(Equal([]string{"True", "True"}), "signed and ready")
				digest = fields[0]
			}, 3*time.Minute).Should(Succeed())

			By("reading the public key the Kitchen publishes")
			publicKey, err := utils.Run(exec.Command("kubectl", "get", "kitchen", "default", "-n", namespace,
				"-o", "jsonpath={.status.signing.publicKey}"))
			Expect(err).NotTo(HaveOccurred())
			keyFile := filepath.Join(GinkgoT().TempDir(), "kokumi.pub")
			Expect(os.WriteFile(keyFile, []byte(publicKey), 0o600)).To(Succeed())

			ref := fmt.Sprintf("%s/e2e/rendered@%s", host, digest)

			By("verifying the signature with cosign")
			_, err = utils.Run(exec.Command(cosign, "verify", "--key", keyFile,
				"--insecure-ignore-tlog=true", "--allow-http-registry", ref))
			Expect(err).NotTo(HaveOccurred())

			By("verifying the provenance with cosign")
			_, err = utils.Run(exec.Command(cosign, "verify-attestation", "--key", keyFile,
				"--insecure-ignore-tlog=true", "--allow-http-registry", "--type", "slsaprovenance1", ref))
			Expect(err).NotTo(HaveOccurred())

			By("not verifying it with another key")
			otherKey := generateCosignKey()
			_, err = utils.Run(exec.Command(cosign, "verify", "--key", otherKey,
				"--insecure-ignore-tlog=true", "--allow-http-registry", ref))
			Expect(err).To(HaveOccurred())
		})

		It("should render a source only once its signature verifies", func() {
			cosign := cosignBinary()
			host := forwardRegistry()

			By("pushing an unsigned source")
			source := pushSource(host, "e2e/verified-source")
			createNamespace(signingNamespace)

			projectDir, err := utils.GetProjectDir()
			Expect(err).NotTo(HaveOccurred())
			publicKey, err := os.ReadFile(filepath.Join(projectDir, "hack", "testdata", "cosign.pub"))
			Expect(err).NotTo(HaveOccurred())

			By("requiring a signature by the testdata key")
			apply(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: testdata-signing-key
  namespace: %[1]s
stringData:
  cosign.pub: |
%[3]s
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: verified
  namespace: %[1]s
spec:
  source:
    url: oci://%[2]s/e2e/verified-source
    version: "1.0.0"
    verify:
      secretRef:
        name: testdata-signing-key
`, signingNamespace, inClusterRegistry, indent(string(publicKey), "    ")))

			sourceVerified := func() string {
				out, _ := utils.Run(exec.Command("kubectl", "get", "product", "verified", "-n", signingNamespace, "-o",
					`jsonpath={.status.conditions[?(@.type=='SourceVerified')].status}/{.status.conditions[?(@.type=='Ready')].status}`))
				return out
			}
			Eventually(sourceVerified).Should(Equal("False/False"), "an unsigned source is not ready")

			By("signing the source")
			GinkgoT().Setenv("COSIGN_PASSWORD", "")
			_, err = utils.Run(exec.Command(cosign, "sign", "--yes",
				"--key", filepath.Join(projectDir, "hack", "testdata", "cosign.key"),
				"--use-signing-config=false", "--tlog-upload=false", "--allow-http-registry",
				fmt.Sprintf("%s/e2e/verified-source@%s", host, source)))
			Expect(err).NotTo(HaveOccurred())

			By("verifying it again, without waiting for the retry interval")
			_, err = utils.Run(exec.Command("kubectl", "annotate", "product", "verified", "-n", signingNamespace,
				"e2e.kokumi.dev/retry="+time.Now().Format(time.RFC3339Nano), "--overwrite"))
			Expect(err).NotTo(HaveOccurred())
			Eventually(sourceVerified).Should(Equal("True/True"))
		})

		It("should vendor images into repositories of their own and run them from there", func() {
			host := forwardRegistry()

			By("pushing a source whose Pod runs an upstream image")
			pushManifest(host, "e2e/workload", fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: web
spec:
  containers:
    - name: web
      image: %s
`, upstreamImage))

			By("rendering an Order of a Product that vendors its images and lets Orders label the Pod")
			createNamespace(imagesNamespace)
			apply(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: vendored
  namespace: %[1]s
spec:
  source:
    url: oci://%[2]s/e2e/workload
    version: "1.0.0"
  vendor:
    url: oci://%[2]s/e2e/vendor/workload
  overrides:
    patches:
      policy: Listed
      targets:
        - target:
            kind: Pod
            name: web
          paths:
            - .metadata.labels.tier
  images:
    policy: Vendor
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: vendored
  namespace: %[1]s
spec:
  productRef:
    name: vendored
  destination:
    url: oci://%[2]s/e2e/vendored
`, imagesNamespace, inClusterRegistry))

			var artifactDigest, imageDigest, vendored string
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "preparations", "-n", imagesNamespace,
					"-l", "delivery.kokumi.dev/order=vendored", "-o",
					`jsonpath={range .items[*]}{.spec.artifact.digest} {.spec.images[0].ref} {.spec.images[0].digest} {.spec.images[0].vendored}{"\n"}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				fields := strings.Fields(out)
				g.Expect(fields).To(HaveLen(4), "one Preparation with one image: "+out)
				g.Expect(fields[1]).To(Equal(upstreamImage))
				artifactDigest, imageDigest, vendored = fields[0], fields[2], fields[3]
			}, 5*time.Minute).Should(Succeed())
			imageRepository := "e2e/vendor/workload/images/" + strings.TrimSuffix(upstreamImage, ":6.14.1")
			Expect(vendored).To(Equal(fmt.Sprintf("%s/%s:6.14.1@%s", inClusterRegistry, imageRepository, imageDigest)),
				"the image keeps its name and tag in a repository of its own below the Product's vendor location")

			ctx := context.Background()
			client := oci.NewClient(oci.Options{PlainHTTP: true})

			By("finding the image, with all its platforms, under its own tag in its own repository")
			copied, err := oci.Parse(fmt.Sprintf("%s/%s:6.14.1", host, imageRepository))
			Expect(err).NotTo(HaveOccurred())
			desc, err := client.Resolve(ctx, copied)
			Expect(err).NotTo(HaveOccurred())
			Expect(desc.Digest).To(Equal(imageDigest))
			index, err := client.FetchManifest(ctx, copied)
			Expect(err).NotTo(HaveOccurred())
			Expect(len(index.Manifests)).To(BeNumerically(">", 1), "an index keeps all platforms")

			By("finding the vendored reference in the rendered manifests")
			rendered, err := oci.Parse(fmt.Sprintf("%s/e2e/vendored@%s", host, artifactDigest))
			Expect(err).NotTo(HaveOccurred())
			dir := GinkgoT().TempDir()
			_, _, _, err = client.Pull(ctx, rendered, dir)
			Expect(err).NotTo(HaveOccurred())
			manifest, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(manifest)).To(ContainSubstring("image: " + vendored))

			By("running a Pod from the vendored reference")
			apply(fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: vendored
  namespace: %s
spec:
  containers:
    - name: web
      image: %s
`, imagesNamespace, vendored))
			_, err = utils.Run(exec.Command("kubectl", "wait", "pod/vendored", "-n", imagesNamespace,
				"--for=condition=Ready", "--timeout=3m"))
			Expect(err).NotTo(HaveOccurred(), "the node pulls the vendored image from the bundled registry")

			By("keeping the vendored digest when an edit renders the Order again")
			_, err = utils.Run(exec.Command("kubectl", "patch", "orders.delivery.kokumi.dev", "vendored", "-n", imagesNamespace, "--type=merge", "-p",
				`{"spec":{"edits":[{"target":{"kind":"Pod","name":"web"},"set":{".metadata.labels.tier":"web"}}]}}`))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "preparations", "-n", imagesNamespace,
					"-l", "delivery.kokumi.dev/order=vendored", "-o",
					`jsonpath={range .items[*]}{.spec.artifact.digest} {.spec.images[0].digest} {.spec.images[0].vendored}{"\n"}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				lines := strings.Split(strings.TrimSpace(out), "\n")
				g.Expect(lines).To(HaveLen(2), "a second Preparation: "+out)
				for _, line := range lines {
					g.Expect(strings.Fields(line)[1:]).To(Equal([]string{imageDigest, vendored}))
				}
			}, 5*time.Minute).Should(Succeed())
		})

		It("should scan vendored images once per Product and hold promotions on findings", func() {
			cosign := cosignBinary()
			host := forwardRegistry()
			ctx := context.Background()
			client := oci.NewClient(oci.Options{PlainHTTP: true})

			By("waiting for the scanner")
			_, err := utils.Run(exec.Command("kubectl", "rollout", "status", "deployment/"+scannerDeploymentName,
				"-n", namespace, "--timeout=5m"))
			Expect(err).NotTo(HaveOccurred())

			By("copying an image with known vulnerabilities, with an unsigned SBOM that lists no packages")
			upstream := host + "/e2e/upstream/alpine:3.14.0"
			Expect(crane.Copy(vulnerableImage, upstream, crane.Insecure)).To(Succeed())
			upstreamRef, err := oci.Parse(upstream)
			Expect(err).NotTo(HaveOccurred())
			desc, err := client.Resolve(ctx, upstreamRef)
			Expect(err).NotTo(HaveOccurred())
			upstreamRef.Tag, upstreamRef.Digest = "", desc.Digest
			_, err = client.PushReferrer(ctx, upstreamRef, oci.ReferrerArtifact{
				ArtifactType: "application/vnd.cyclonedx+json",
				MediaType:    "application/vnd.cyclonedx+json",
				Payload:      []byte(`{"bomFormat":"CycloneDX","specVersion":"1.6","components":[]}`),
			})
			Expect(err).NotTo(HaveOccurred())

			By("enabling scanning on the Kitchen")
			_, err = utils.Run(exec.Command("kubectl", "patch", "kitchens.delivery.kokumi.dev", "default", "-n", namespace,
				"--type=merge", "-p", `{"spec":{"scanning":{}}}`))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				_, _ = utils.Run(exec.Command("kubectl", "patch", "kitchens.delivery.kokumi.dev", "default", "-n", namespace,
					"--type=json", "-p", `[{"op":"remove","path":"/spec/scanning"}]`))
			})

			By("rendering two Orders of a Product that vendors the image, one with a vulnerability check")
			pushManifest(host, "e2e/scanned", fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: app
spec:
  containers:
    - name: app
      image: %s/e2e/upstream/alpine:3.14.0
`, inClusterRegistry))
			createNamespace(scanningNamespace)
			apply(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: scanned
  namespace: %[1]s
spec:
  source:
    url: oci://%[2]s/e2e/scanned
    version: "1.0.0"
  vendor:
    url: oci://%[2]s/e2e/vendor/scanned
  images:
    policy: Vendor
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: unchecked
  namespace: %[1]s
spec:
  productRef:
    name: scanned
  destination:
    url: oci://%[2]s/e2e/scanned-unchecked
  promotion:
    mode: Manual
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: checked
  namespace: %[1]s
spec:
  productRef:
    name: scanned
  destination:
    url: oci://%[2]s/e2e/scanned-checked
  promotion:
    mode: Automatic
    checks:
      vulnerabilities:
        maxSeverity: High
`, scanningNamespace, inClusterRegistry))

			var imageDigest string
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "preparations", "-n", scanningNamespace, "-o",
					`jsonpath={range .items[*]}{.status.conditions[?(@.type=='Ready')].status} {.spec.images[0].digest}{"\n"}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				lines := utils.GetNonEmptyLines(out)
				g.Expect(lines).To(HaveLen(2), "a Preparation per Order: "+out)
				for _, line := range lines {
					fields := strings.Fields(line)
					g.Expect(fields).To(HaveLen(2), out)
					g.Expect(fields[0]).To(Equal("True"), out)
					imageDigest = fields[1]
				}
				g.Expect(strings.Count(out, imageDigest)).To(Equal(2), "both run the same vendored image")
			}, 5*time.Minute).Should(Succeed())

			By("scanning the vendored image, ignoring the unsigned SBOM")
			var repository, report string
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "products.delivery.kokumi.dev", "scanned", "-n", scanningNamespace, "-o",
					`jsonpath={.status.conditions[?(@.type=='Scanned')].reason} {range .status.vulnerabilities.images[*]}{.digest} {.repository} {.report} {.sbom.origin} {.summary.critical}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				fields := strings.Fields(out)
				g.Expect(fields).To(HaveLen(6), "one scanned image: "+out)
				g.Expect(fields[0]).To(Equal(deliveryv1alpha1.ReasonScanned), out)
				g.Expect(fields[1]).To(Equal(imageDigest))
				g.Expect(fields[4]).To(Equal(string(deliveryv1alpha1.SBOMOriginGenerated)), "an SBOM nobody signed is not used")
				g.Expect(fields[5]).NotTo(Equal("0"), "the known Critical vulnerabilities are found")
				repository, report = fields[2], fields[3]
			}, 15*time.Minute, 5*time.Second).Should(Succeed())

			By("attaching one signed report for both Orders")
			vendored, err := oci.Parse(strings.Replace(repository, inClusterRegistry, host, 1) + "@" + imageDigest)
			Expect(err).NotTo(HaveOccurred())
			referrers, err := client.ListReferrers(ctx, vendored, oci.SigstoreBundleMediaType)
			Expect(err).NotTo(HaveOccurred())
			var reports []string
			for _, r := range referrers {
				if r.Annotations[oci.AnnotationBundlePredicateType] == vulnerability.PredicateType {
					reports = append(reports, r.Digest)
				}
			}
			Expect(reports).To(ConsistOf(report), "two Orders, one scan")

			publicKey, err := utils.Run(exec.Command("kubectl", "get", "kitchen", "default", "-n", namespace,
				"-o", "jsonpath={.status.signing.publicKey}"))
			Expect(err).NotTo(HaveOccurred())
			keyFile := filepath.Join(GinkgoT().TempDir(), "kokumi.pub")
			Expect(os.WriteFile(keyFile, []byte(publicKey), 0o600)).To(Succeed())
			_, err = utils.Run(exec.Command(cosign, "verify-attestation", "--key", keyFile,
				"--insecure-ignore-tlog=true", "--allow-http-registry", "--type", "vuln", vendored.String()))
			Expect(err).NotTo(HaveOccurred(), "stock cosign verifies the report")

			By("holding the Preparation of the checked Order")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "servings.delivery.kokumi.dev", "checked", "-n", scanningNamespace, "-o",
					`jsonpath={.status.conditions[?(@.type=='ChecksPassed')].reason}|{.status.history[*].promotionRecord}`))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal(deliveryv1alpha1.ReasonVulnerabilitiesFound+"|"), "nothing is deployed")
			}, 5*time.Minute).Should(Succeed())

			By("letting only the controller reach the scanner")
			_, err = utils.Run(exec.Command("kubectl", "run", "curl-scanner", "--restart=Never",
				"--namespace", scanningNamespace,
				"--image=curlimages/curl:latest",
				"--overrides", fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["curl", "-sS", "--max-time", "10", "http://%s.%s.svc:8080/healthz"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {"drop": ["ALL"]},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {"type": "RuntimeDefault"}
							}
						}]
					}
				}`, scannerDeploymentName, namespace)))
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "pod", "curl-scanner", "-n", scanningNamespace,
					"-o", "jsonpath={.status.phase}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(Equal("Failed"), "the NetworkPolicy drops connections from other Pods")
			}, 2*time.Minute).Should(Succeed())
		})

		It("should deploy each stage after the stage before it ran the Product revision healthily, and record it", func() {
			cosign := cosignBinary()
			host := forwardRegistry()

			By("rendering two stages of a Product: prod runs after staging, with a soak")
			pushSource(host, "e2e/pipeline")
			createNamespace(pipelineNamespace)
			createNamespace(argoCDNamespace)
			DeferCleanup(func() {
				_, _ = utils.Run(exec.Command("kubectl", "delete", "applications.argoproj.io", "pipeline-staging", "pipeline-prod",
					"-n", argoCDNamespace, "--ignore-not-found"))
			})
			apply(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Product
metadata:
  name: pipeline
  namespace: %[1]s
spec:
  source:
    url: oci://%[2]s/e2e/pipeline
    version: "1.0.0"
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: pipeline-staging
  namespace: %[1]s
spec:
  productRef:
    name: pipeline
  destination:
    url: oci://%[2]s/e2e/pipeline-staging
  promotion:
    mode: Automatic
---
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Order
metadata:
  name: pipeline-prod
  namespace: %[1]s
spec:
  productRef:
    name: pipeline
  destination:
    url: oci://%[2]s/e2e/pipeline-prod
  promotion:
    mode: Automatic
    after:
    - order: pipeline-staging
      soakSeconds: 20
`, pipelineNamespace, inClusterRegistry))

			By("rendering one Preparation of the same Product revision per stage")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "preparations", "-n", pipelineNamespace, "-o",
					`jsonpath={range .items[*]}{.spec.productRevision}{"\n"}{end}`))
				g.Expect(err).NotTo(HaveOccurred())
				lines := utils.GetNonEmptyLines(out)
				g.Expect(lines).To(HaveLen(2), out)
				g.Expect(lines[0]).To(Equal(lines[1]), "both stages render the same Product change")
			}, 3*time.Minute).Should(Succeed())

			stagingDigest := applicationRevision("pipeline-staging")

			By("holding prod until staging is healthy")
			Eventually(func(g Gomega) {
				g.Expect(servingCondition(g, "pipeline-prod", deliveryv1alpha1.ConditionTypeUpstreamDeployed)).
					To(Equal(deliveryv1alpha1.ReasonNotDeployedUpstream))
			}, time.Minute).Should(Succeed())

			By("soaking prod once staging reports healthy")
			reportHealthy("pipeline-staging", stagingDigest)
			Eventually(func(g Gomega) {
				g.Expect(servingCondition(g, "pipeline-prod", deliveryv1alpha1.ConditionTypeUpstreamDeployed)).
					To(Equal(deliveryv1alpha1.ReasonSoaking))
			}, time.Minute).Should(Succeed())
			_, err := utils.Run(exec.Command("kubectl", "get", "applications.argoproj.io", "pipeline-prod", "-n", argoCDNamespace))
			Expect(err).To(HaveOccurred(), "prod does not deploy during the soak")

			By("deploying prod after the soak")
			prodDigest := applicationRevision("pipeline-prod")
			Expect(servingCondition(Default, "pipeline-prod", deliveryv1alpha1.ConditionTypeUpstreamDeployed)).
				To(Equal(deliveryv1alpha1.ReasonSatisfied))
			reportHealthy("pipeline-prod", prodDigest)
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "get", "servings.delivery.kokumi.dev", "pipeline-prod", "-n", pipelineNamespace,
					"-o", "jsonpath={.status.history[0].deploymentRecord}"))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).To(HavePrefix("sha256:"), "the healthy deployment is recorded")
			}, time.Minute).Should(Succeed())

			By("verifying the promotion and deployment records with cosign")
			publicKey, err := utils.Run(exec.Command("kubectl", "get", "kitchen", "default", "-n", namespace,
				"-o", "jsonpath={.status.signing.publicKey}"))
			Expect(err).NotTo(HaveOccurred())
			keyFile := filepath.Join(GinkgoT().TempDir(), "kokumi.pub")
			Expect(os.WriteFile(keyFile, []byte(publicKey), 0o600)).To(Succeed())
			for _, record := range []struct{ predicateType, ref string }{
				{"https://kokumi.dev/attestations/promotion/v1", host + "/e2e/pipeline-prod@" + prodDigest},
				{"https://kokumi.dev/attestations/deployment/v1", host + "/e2e/pipeline-prod@" + prodDigest},
				{"https://kokumi.dev/attestations/deployment/v1", host + "/e2e/pipeline-staging@" + stagingDigest},
			} {
				_, err = utils.Run(exec.Command(cosign, "verify-attestation", "--key", keyFile,
					"--insecure-ignore-tlog=true", "--allow-http-registry", "--type", record.predicateType, record.ref))
				Expect(err).NotTo(HaveOccurred(), "stock cosign verifies the %s record of %s", record.predicateType, record.ref)
			}
		})

		It("should keep the signing key away from everyone but the controller and cluster admins", func() {
			By("waiting for the controller to generate the key in its own namespace")
			Eventually(func() error {
				_, err := utils.Run(exec.Command("kubectl", "get", "secret", signingKeySecretName, "-n", signingKeyNamespace))
				return err
			}).Should(Succeed())

			By("checking that the built-in identities cannot read or change it")
			for _, sa := range []string{"kokumi-admin", "kokumi-editor", "kokumi-viewer", "kokumi-server"} {
				for _, verb := range []string{"get", "list", "patch", "delete"} {
					out, _ := utils.Run(exec.Command("kubectl", "auth", "can-i", verb,
						"secret/"+signingKeySecretName, "-n", signingKeyNamespace,
						"--as=system:serviceaccount:"+namespace+":"+sa))
					Expect(strings.TrimSpace(out)).To(Equal("no"), "%s may %s the signing key", sa, verb)
				}
			}

			By("checking that the server still reads its auth Secrets in the install namespace")
			out, _ := utils.Run(exec.Command("kubectl", "auth", "can-i", "list", "secrets", "-n", namespace,
				"--as=system:serviceaccount:"+namespace+":kokumi-server"))
			Expect(strings.TrimSpace(out)).To(Equal("yes"))

			By("granting a ServiceAccount Secret write access in the signing namespace")
			apply(fmt.Sprintf(`
apiVersion: v1
kind: ServiceAccount
metadata:
  name: e2e-secret-writer
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: e2e-secret-writer
  namespace: %[2]s
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: e2e-secret-writer
  namespace: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: e2e-secret-writer
subjects:
- kind: ServiceAccount
  name: e2e-secret-writer
  namespace: %[1]s
`, namespace, signingKeyNamespace))

			By("checking that admission still refuses it the key")
			Eventually(func(g Gomega) {
				out, err := utils.Run(exec.Command("kubectl", "patch", "secret", signingKeySecretName, "-n", signingKeyNamespace,
					"--as=system:serviceaccount:"+namespace+":e2e-secret-writer",
					"--type=merge", "-p", `{"metadata":{"labels":{"forged":"true"}}}`))
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error() + out).To(ContainSubstring("signing key can only be written"))
			}).Should(Succeed())

			By("letting a cluster admin change it")
			_, err := utils.Run(exec.Command("kubectl", "label", "secret", signingKeySecretName, "-n", signingKeyNamespace,
				"e2e.kokumi.dev/checked-by=cluster-admin", "--overwrite"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("should let only who may read a Secret name it in a Registry", func() {
			const registryNamespace = "e2e-registries"
			createNamespace(registryNamespace)

			By("refusing the editor, who may not read Secrets, a Registry that names one")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "apply", "--as=system:serviceaccount:"+namespace+":kokumi-editor", "-f", "-")
				cmd.Stdin = strings.NewReader(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Registry
metadata:
  name: private
  namespace: %s
spec:
  url: oci://ghcr.io/kokumi-e2e
  secretRef:
    name: ghcr-credentials
`, registryNamespace))
				out, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error() + out).To(ContainSubstring("A Registry may only name a Secret its author may get"))
			}).Should(Succeed())

			By("letting the editor create a Registry without credentials")
			cmd := exec.Command("kubectl", "apply", "--as=system:serviceaccount:"+namespace+":kokumi-editor", "-f", "-")
			cmd.Stdin = strings.NewReader(fmt.Sprintf(`
apiVersion: delivery.kokumi.dev/v1alpha1
kind: Registry
metadata:
  name: public
  namespace: %s
spec:
  url: oci://ghcr.io/kokumi-e2e
`, registryNamespace))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// forwardRegistry forwards a free local port to the bundled registry for the
// rest of the spec and returns the local host:port.
func forwardRegistry() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := listener.Addr().(*net.TCPAddr).Port
	Expect(listener.Close()).To(Succeed())

	cmd := exec.Command("kubectl", "port-forward", "-n", namespace,
		"service/"+registryServiceName, fmt.Sprintf("%d:5000", port))
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	host := fmt.Sprintf("127.0.0.1:%d", port)
	Eventually(func(g Gomega) {
		resp, err := http.Get("http://" + host + "/v2/")
		g.Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close() //nolint:errcheck
		g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
	}, time.Minute, time.Second).Should(Succeed())

	return host
}

// applicationRevision waits for the Argo CD Application of the Serving name
// and returns the digest it deploys.
func applicationRevision(name string) string {
	var revision string
	Eventually(func(g Gomega) {
		out, err := utils.Run(exec.Command("kubectl", "get", "applications.argoproj.io", name, "-n", argoCDNamespace,
			"-o", "jsonpath={.spec.source.targetRevision}"))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(out).To(HavePrefix("sha256:"))
		revision = out
	}, 2*time.Minute).Should(Succeed())
	return revision
}

// reportHealthy reports the Application of the Serving name synced to
// revision and healthy, as Argo CD would. The suite runs no Argo CD, and the
// Application CRD has no status subresource.
func reportHealthy(name, revision string) {
	_, err := utils.Run(exec.Command("kubectl", "patch", "applications.argoproj.io", name, "-n", argoCDNamespace,
		"--type=merge", "-p", fmt.Sprintf(`{"status":{"sync":{"status":"Synced","revision":%q},"health":{"status":"Healthy"}}}`, revision)))
	Expect(err).NotTo(HaveOccurred())
}

// servingCondition returns the reason of the condition of conditionType of
// the Serving name in the pipeline namespace.
func servingCondition(g Gomega, name, conditionType string) string {
	out, err := utils.Run(exec.Command("kubectl", "get", "servings.delivery.kokumi.dev", name, "-n", pipelineNamespace,
		"-o", fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=='%s')].reason}`, conditionType)))
	g.Expect(err).NotTo(HaveOccurred())
	return out
}

// cosignBinary returns the cosign CLI the e2e tests verify with.
func cosignBinary() string {
	if cosign := os.Getenv("COSIGN"); cosign != "" {
		return cosign
	}
	return "cosign"
}

// generateCosignKey generates a cosign key pair and returns the public key
// file.
func generateCosignKey() string {
	dir := GinkgoT().TempDir()
	GinkgoT().Setenv("COSIGN_PASSWORD", "")
	cmd := exec.Command(cosignBinary(), "generate-key-pair", "--output-key-prefix", filepath.Join(dir, "other"))
	_, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	return filepath.Join(dir, "other.pub")
}

// pushSource pushes a manifest bundle as repository:1.0.0 to the registry
// at host and returns its digest.
func pushSource(host, repository string) string {
	return pushManifest(host, repository, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: e2e\ndata:\n  key: value\n")
}

// pushManifest pushes a manifest bundle of manifest as repository:1.0.0 to
// the registry at host and returns its digest.
func pushManifest(host, repository, manifest string) string {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o600)).To(Succeed())

	ref, err := oci.Parse(host + "/" + repository + ":1.0.0")
	Expect(err).NotTo(HaveOccurred())
	digest, err := oci.NewClient(oci.Options{PlainHTTP: true}).Push(context.Background(), ref, dir, oci.PushOptions{})
	Expect(err).NotTo(HaveOccurred())
	return digest
}

// createNamespace creates name unless it exists.
func createNamespace(name string) {
	out, err := utils.Run(exec.Command("kubectl", "create", "ns", name))
	if err != nil {
		Expect(out).To(ContainSubstring("AlreadyExists"))
	}
}

// apply applies the manifests in yaml.
func apply(yaml string) {
	file := filepath.Join(GinkgoT().TempDir(), "manifests.yaml")
	Expect(os.WriteFile(file, []byte(yaml), 0o600)).To(Succeed())
	_, err := utils.Run(exec.Command("kubectl", "apply", "-f", file))
	Expect(err).NotTo(HaveOccurred())
}

// indent prefixes every line of text with prefix.
func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
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
