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

// Package examples checks the examples in examples/ without a cluster: that
// the API accepts them, and that the e2e suite runs and the docs show each of
// them. The e2e suite checks that they do what they say.
package examples

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// root is the repository root, relative to this package.
var root = filepath.Join("..", "..")

// kitchenPatch is the file an example holds a patch of the Kitchen in, for
// a cluster admin to apply; it is not part of the example's kustomization.
const kitchenPatch = "kitchen-patch.yaml"

// TestExamplesAreAccepted applies every example, and the samples in
// config/samples, to an API server with kokumi's CRDs as a server-side dry
// run, so that an API change that breaks one fails in seconds instead of in
// the e2e suite.
func TestExamplesAreAccepted(t *testing.T) {
	ctx := context.Background()
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: firstEnvTestBinaryDir(),
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("Failed to start the API server: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	if err := deliveryv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"examples", "kokumi"} {
		if err := c.Create(ctx, &corev1.Namespace{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	// The controller creates the Kitchen at startup.
	kitchen := &deliveryv1alpha1.Kitchen{Name: "default", Namespace: "kokumi"}
	if err := c.Create(ctx, kitchen); err != nil {
		t.Fatal(err)
	}

	t.Run("config/samples", func(t *testing.T) {
		for _, obj := range build(t, filepath.Join(root, "config", "samples")) {
			dryRunApply(ctx, t, c, obj)
		}
	})

	for _, example := range examples(t) {
		t.Run(example, func(t *testing.T) {
			dir := filepath.Join(root, "examples", example)
			for _, obj := range build(t, dir) {
				if obj.GetNamespace() != "" {
					t.Errorf("%s %s sets a namespace; examples apply to the namespace a user chooses", obj.GetKind(), obj.GetName())
				}
				obj.SetNamespace("examples")
				dryRunApply(ctx, t, c, obj)
			}

			patch, err := os.ReadFile(filepath.Join(dir, kitchenPatch))
			if os.IsNotExist(err) {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			patchJSON, err := yaml.YAMLToJSON(patch)
			if err != nil {
				t.Fatalf("Failed to read %s: %v", kitchenPatch, err)
			}
			mergePatch := client.RawPatch(types.MergePatchType, patchJSON)
			if err := c.Patch(ctx, kitchen.DeepCopy(), mergePatch, client.DryRunAll); err != nil {
				t.Errorf("The API refuses %s: %v", kitchenPatch, err)
			}
		})
	}
}

// build builds the kustomization in dir, as kubectl apply -k does.
func build(t *testing.T, dir string) []*unstructured.Unstructured {
	resources, err := krusty.MakeKustomizer(krusty.MakeDefaultOptions()).Run(filesys.MakeFsOnDisk(), dir)
	if err != nil {
		t.Fatalf("Failed to build the kustomization: %v", err)
	}
	objs := make([]*unstructured.Unstructured, 0, len(resources.Resources()))
	for _, resource := range resources.Resources() {
		fields, err := resource.Map()
		if err != nil {
			t.Fatal(err)
		}
		objs = append(objs, &unstructured.Unstructured{Object: fields})
	}
	return objs
}

// dryRunApply applies obj with a server-side dry run, so that it is validated
// whether or not it exists.
func dryRunApply(ctx context.Context, t *testing.T, c client.Client, obj *unstructured.Unstructured) {
	t.Helper()
	err := c.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj),
		client.DryRunAll, client.FieldOwner("examples-test"), client.ForceOwnership)
	if err != nil {
		t.Errorf("The API refuses %s %s: %v", obj.GetKind(), obj.GetName(), err)
	}
}

// TestExamplesAreTestedAndDocumented fails for an example that no e2e spec
// applies or no docs page shows, so that no example goes untested and none
// is left out of the docs.
func TestExamplesAreTestedAndDocumented(t *testing.T) {
	specs := readAll(t, filepath.Join(root, "test", "e2e"), ".go")
	docs := readAll(t, filepath.Join(root, "docs", "content"), ".md")

	for _, example := range examples(t) {
		applied := regexp.MustCompile(`ApplyExample(Overlay)?\([^,]+,\s*"` + regexp.QuoteMeta(example) + `"\)`)
		if !applied.MatchString(specs) {
			t.Errorf("No e2e spec applies examples/%s: call framework.ApplyExample(ns, %q)", example, example)
		}
		shown := regexp.MustCompile(`\{\{<\s*example\s+file="` + regexp.QuoteMeta(example) + `/`)
		if !shown.MatchString(docs) {
			t.Errorf(`No docs page shows examples/%s: use {{< example file="%s/<file>" >}}`, example, example)
		}
	}
}

// examples returns the names of the directories in examples/.
func examples(t *testing.T) []string {
	entries, err := os.ReadDir(filepath.Join(root, "examples"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("Found no examples")
	}
	return names
}

// readAll returns the concatenated content of the files with ext below dir.
func readAll(t *testing.T, dir, ext string) string {
	var content []byte
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ext {
			return err
		}
		data, err := os.ReadFile(path)
		content = append(content, data...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// firstEnvTestBinaryDir returns the envtest binaries "make setup-envtest"
// installed, so that the test also runs outside "make test", which sets
// KUBEBUILDER_ASSETS.
func firstEnvTestBinaryDir() string {
	entries, err := os.ReadDir(filepath.Join(root, "bin", "k8s"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(root, "bin", "k8s", entry.Name())
		}
	}
	return ""
}
