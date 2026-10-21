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
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
)

// ApplyExample applies the example examples/<name> to namespace with the
// command the docs give users: kubectl apply -n <namespace> -k examples/<name>.
func ApplyExample(namespace, name string) {
	By(fmt.Sprintf("applying the %s example: kubectl apply -n %s -k examples/%s", name, namespace, name))
	_, err := Kubectl("apply", "-n", namespace, "-k", filepath.Join(ProjectDir(), "examples", name))
	Expect(err).NotTo(HaveOccurred(), "Failed to apply the %s example", name)
}

// ApplyExampleOverlay applies the example examples/<name> through the overlay
// test/e2e/testdata/<name>, a kustomization that builds on the example and
// fills in what a user must, such as the URL and the credentials of their
// registry: kubectl apply -n <namespace> -k test/e2e/testdata/<name>.
func ApplyExampleOverlay(namespace, name string) {
	By(fmt.Sprintf("applying the %s example, with what a user fills in from test/e2e/testdata/%s", name, name))
	_, err := Kubectl("apply", "-n", namespace, "-k", filepath.Join(ProjectDir(), "test", "e2e", "testdata", name))
	Expect(err).NotTo(HaveOccurred(), "Failed to apply the %s example", name)
}

// variable matches ${NAME}, as envsubst and clusterctl templates write it.
var variable = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// ApplyTestdata applies the test-only fixture test/e2e/testdata/<file> to
// namespace, or to the namespaces it names when namespace is empty, after
// replacing every ${NAME} in it with vars[NAME]. ${REGISTRY} is the bundled
// registry and ${NAMESPACE} is namespace, unless vars names them. A fixture
// holds what only a spec needs, such as a Product of a source the spec
// pushed; what users would write belongs into an example.
func ApplyTestdata(namespace, file string, vars map[string]string) {
	all := map[string]string{"REGISTRY": InClusterRegistry, "NAMESPACE": namespace}
	maps.Copy(all, vars)
	args := []string{"apply", "-f", TestdataFile(file, all)}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := Kubectl(args...)
	Expect(err).NotTo(HaveOccurred(), "Failed to apply %s", file)
}

// TestdataFile writes the test-only fixture test/e2e/testdata/<file>, with
// every ${NAME} in it replaced with vars[NAME], to a temporary file and
// returns the file, for a spec to pass to kubectl itself.
func TestdataFile(file string, vars map[string]string) string {
	expanded := filepath.Join(GinkgoT().TempDir(), filepath.Base(file))
	Expect(os.WriteFile(expanded, []byte(testdata(file, vars)), 0o600)).To(Succeed())
	return expanded
}

// testdata returns the content of test/e2e/testdata/<file> with every
// ${NAME} in it replaced with vars[NAME].
func testdata(file string, vars map[string]string) string {
	content, err := os.ReadFile(filepath.Join(ProjectDir(), "test", "e2e", "testdata", file))
	Expect(err).NotTo(HaveOccurred())

	var missing []string
	expanded := variable.ReplaceAllStringFunc(string(content), func(match string) string {
		name := variable.FindStringSubmatch(match)[1]
		value, ok := vars[name]
		if !ok {
			missing = append(missing, name)
		}
		return value
	})
	Expect(missing).To(BeEmpty(), "%s needs these variables", file)
	return expanded
}
