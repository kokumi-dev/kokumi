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
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/gomega" //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// RenderedObject returns the object of kind and name from the manifests in
// dir, a pulled Preparation, or fails.
func RenderedObject(dir, kind, name string) *unstructured.Unstructured {
	var found *unstructured.Unstructured
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
		for {
			doc, err := reader.Read()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			// Through JSON, so that integers decode as int64, as the API
			// server's would.
			jsonDoc, err := yaml.YAMLToJSON(doc)
			if err != nil {
				return err
			}
			obj := &unstructured.Unstructured{}
			if err := obj.UnmarshalJSON(jsonDoc); err != nil {
				continue // not an object, such as an empty document
			}
			if obj.GetKind() == kind && obj.GetName() == name {
				found = obj
			}
		}
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(found).NotTo(BeNil(), "The rendered manifests hold no %s %s", kind, name)
	return found
}

// ContainerEnv returns the value of the environment variable name of the
// first container of the Deployment deployment, a rendered object.
func ContainerEnv(deployment *unstructured.Unstructured, name string) string {
	containers, _, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	Expect(err).NotTo(HaveOccurred())
	Expect(containers).NotTo(BeEmpty(), "%s has no containers", deployment.GetName())
	env, _, err := unstructured.NestedSlice(containers[0].(map[string]any), "env")
	Expect(err).NotTo(HaveOccurred())
	for _, e := range env {
		if variable := e.(map[string]any); variable["name"] == name {
			value, _ := variable["value"].(string)
			return value
		}
	}
	return ""
}

// Field returns the value at fields in the rendered object obj, or nil.
func Field(obj *unstructured.Unstructured, fields ...string) any {
	value, _, _ := unstructured.NestedFieldNoCopy(obj.Object, fields...)
	return value
}
