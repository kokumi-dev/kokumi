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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
)

// kokumiKinds are the kinds a failed spec dumps from its namespaces.
const kokumiKinds = "products,orders,preparations,servings,recipes,approvals,registries,repositories"

// components are the label selectors of the Pods of the install namespace
// whose logs a failed spec dumps, by name.
var components = map[string]string{
	"controller-manager": "control-plane=controller-manager",
	"server":             "app.kubernetes.io/component=server",
	"scanner":            "app.kubernetes.io/component=scanner",
	"registry":           "app.kubernetes.io/component=registry",
}

// specFailed records that a spec of the suite failed.
var specFailed atomic.Bool

// SpecFailed reports whether a spec of the suite failed, as DumpOnFailure saw.
func SpecFailed() bool {
	return specFailed.Load()
}

// ArtifactsDir returns the directory diagnostics are written to: $ARTIFACTS,
// or _artifacts in the repository.
func ArtifactsDir() string {
	if dir := os.Getenv("ARTIFACTS"); dir != "" {
		return dir
	}
	return filepath.Join(ProjectDir(), "_artifacts")
}

// DumpOnFailure writes the state of the cluster to a directory of the
// current spec under ArtifactsDir when the spec failed: the kokumi objects
// and events of the scenario namespaces, the logs and events of the install
// namespace, the Kitchen and the Argo CD Applications. Call it from an
// AfterEach, which runs before the scenario's cleanup.
func DumpOnFailure() {
	report := CurrentSpecReport()
	if !report.Failed() {
		return
	}
	specFailed.Store(true)
	dir := filepath.Join(ArtifactsDir(), "e2e", fileName(report.FullText()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to create the diagnostics directory: %v\n", err)
		return
	}

	dump := func(file string, args ...string) {
		out, err := Kubectl(args...)
		if err != nil {
			out = err.Error()
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(out), 0o600); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to write %s: %v\n", file, err)
		}
	}

	for _, ns := range scenarioNamespaces() {
		dump(ns+"-objects.yaml", "get", kokumiKinds, "-n", ns, "-o", "yaml")
		dump(ns+"-events.txt", "get", "events", "-n", ns, "--sort-by=.lastTimestamp")
	}
	for name, selector := range components {
		dump(name+".log", "logs", "-n", InstallNamespace, "-l", selector,
			"--all-containers", "--prefix", "--tail=-1")
	}
	dump("install-pods.txt", "get", "pods", "-n", InstallNamespace, "-o", "wide")
	dump("install-events.txt", "get", "events", "-n", InstallNamespace, "--sort-by=.lastTimestamp")
	dump("kitchen.yaml", "get", "kitchens", "-n", InstallNamespace, "-o", "yaml")
	dump("applications.yaml", "get", "applications.argoproj.io", "-n", ArgoCDNamespace, "-o", "yaml")

	AddReportEntry("diagnostics", dir)
	_, _ = fmt.Fprintf(GinkgoWriter, "Wrote the diagnostics of the failed spec to %s\n", dir)
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// fileName turns a spec's text into a directory name.
func fileName(text string) string {
	name := strings.Trim(unsafeFileChars.ReplaceAllString(text, "-"), "-")
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}
