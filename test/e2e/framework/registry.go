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
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/oras-project/oras-go/v3/registry/remote/credentials"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
	"github.com/kokumi-dev/kokumi/internal/oci"
)

// registryHost is the local end of the port-forward to the bundled registry.
var registryHost string

// ForwardRegistry forwards a free local port to the bundled registry until
// the suite ends. Call it from BeforeSuite.
func ForwardRegistry() {
	registryHost = ForwardRegistryService(InstallNamespace, "kokumi-registry")
}

// ForwardRegistryService forwards a free local port to port 5000 of the
// registry behind the Service service in namespace until the scope of the
// calling node ends, waits until the registry answers, and returns the local
// host:port.
func ForwardRegistryService(namespace, service string) string {
	host := forward(namespace, service, 5000)[0]
	Eventually(func(g Gomega) {
		resp, err := http.Get("http://" + host + "/v2/")
		g.Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close() //nolint:errcheck
		g.Expect(resp.StatusCode).To(BeElementOf(http.StatusOK, http.StatusUnauthorized))
	}, time.Minute, time.Second).Should(Succeed(),
		"The registry %s/%s is not reachable through the port-forward", namespace, service)
	return host
}

// RegistryHost returns the host:port the test process reaches the bundled
// registry at.
func RegistryHost() string {
	Expect(registryHost).NotTo(BeEmpty(), "ForwardRegistry has not run")
	return registryHost
}

// Registry returns a client of the bundled registry.
func Registry() *oci.ORASClient {
	return oci.NewClient(oci.Options{PlainHTTP: true})
}

// Local turns a reference to the bundled registry as the cluster names it,
// with or without oci://, into one the test process can reach.
func Local(ref string) string {
	return strings.Replace(strings.TrimPrefix(ref, "oci://"), InClusterRegistry, RegistryHost(), 1)
}

// Ref parses a reference, with or without oci://.
func Ref(ref string) oci.Reference {
	parsed, err := oci.Parse(strings.TrimPrefix(ref, "oci://"))
	Expect(err).NotTo(HaveOccurred())
	return parsed
}

// PushSource pushes the test-only fixture test/e2e/testdata/<file>, with
// every ${NAME} in it replaced with vars[NAME], to the bundled registry as a
// manifest bundle repository:1.0.0, and returns its digest. repository is
// relative to the registry.
func PushSource(repository, file string, vars map[string]string) string {
	dir := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(testdata(file, vars)), 0o600)).To(Succeed())

	ref := Ref(RegistryHost() + "/" + repository + ":1.0.0")
	digest, err := Registry().Push(context.Background(), ref, dir, oci.PushOptions{})
	Expect(err).NotTo(HaveOccurred(), "Failed to push %s", repository)
	return digest
}

// Pull pulls the artifact ref, as the test process reaches it, into a
// temporary directory and returns the directory.
func Pull(ref string) string {
	dir := GinkgoT().TempDir()
	_, _, _, err := Registry().Pull(context.Background(), Ref(ref), dir)
	Expect(err).NotTo(HaveOccurred(), "Failed to pull %s", ref)
	return dir
}

// PreparationRef returns the reference of the artifact of preparation, as the
// test process reaches it.
func PreparationRef(preparation *deliveryv1alpha1.Preparation) string {
	return Local(preparation.Spec.Artifact.OCIRef)
}

// PullPreparation pulls the artifact of preparation into a temporary
// directory and returns the directory.
func PullPreparation(preparation *deliveryv1alpha1.Preparation) string {
	return Pull(PreparationRef(preparation))
}

// CopyToRegistry copies the artifact src from a public registry to dst, a
// reference to a registry the test process reaches over plain HTTP, which it
// authenticates to with username and password.
func CopyToRegistry(src, dst, username, password string) {
	dstRef := Ref(dst)
	target := oci.NewClient(oci.Options{
		Host:       dstRef.Registry,
		Credential: credentials.Credential{Username: username, Password: password},
		PlainHTTP:  true,
	})
	Expect(oci.NewClient(oci.Options{}).Copy(context.Background(), target, Ref(src), dstRef, oci.CopyOptions{})).
		To(Succeed(), "Failed to copy %s to %s", src, dst)
}
