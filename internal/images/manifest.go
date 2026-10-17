package images

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"

	"go.yaml.in/yaml/v3"
	"sigs.k8s.io/kustomize/api/filters/fsslice"
	"sigs.k8s.io/kustomize/api/types"
	kyaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

// containerFields are the fields whose list elements carry an image field:
// the containers of a PodSpec, wherever it is embedded.
var containerFields = []string{"containers", "initContainers", "ephemeralContainers"}

// Manifest is a parsed multi-document YAML file whose image references can be
// rewritten in place.
type Manifest struct {
	docs []*yaml.Node
	// fields are the scalar nodes holding image references, in document
	// order and without duplicates.
	fields []*yaml.Node
}

// ParseManifest parses content and finds its image references: the image of
// every container, init container and ephemeral container at any depth of
// any object, the reference of every image volume, and the fields specs
// locate. CustomResourceDefinitions are skipped, as kustomize does, since
// their schemas mention the same field names.
func ParseManifest(content []byte, specs []FieldSpec) (*Manifest, error) {
	m := &Manifest{}
	seen := map[*yaml.Node]bool{}
	add := func(node *yaml.Node) {
		if !seen[node] && node.Value != "" && node.ShortTag() != "!!null" {
			seen[node] = true
			m.fields = append(m.fields, node)
		}
	}

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	for {
		doc := &yaml.Node{}
		if err := decoder.Decode(doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("failed to parse manifest: %w", err)
		}
		m.docs = append(m.docs, doc)

		if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
			continue
		}
		root := doc.Content[0]
		if kind := scalarField(root, "kind"); kind != nil && kind.Value == "CustomResourceDefinition" {
			continue
		}

		findDefault(root, add)
		if err := findSpecified(root, specs, add); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// findDefault calls add for the image fields of containers and image volumes
// anywhere below node.
func findDefault(node *yaml.Node, add func(*yaml.Node)) {
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if value.Kind == yaml.SequenceNode {
				for _, element := range value.Content {
					switch {
					case slices.Contains(containerFields, key.Value):
						if image := scalarField(element, "image"); image != nil {
							add(image)
						}
					case key.Value == "volumes":
						if reference := scalarField(mappingField(element, "image"), "reference"); reference != nil {
							add(reference)
						}
					}
				}
			}
			findDefault(value, add)
		}
	case yaml.SequenceNode:
		for _, element := range node.Content {
			findDefault(element, add)
		}
	}
}

// findSpecified calls add for every field specs locate in the object root,
// with kustomize's own field spec semantics.
func findSpecified(root *yaml.Node, specs []FieldSpec, add func(*yaml.Node)) error {
	if len(specs) == 0 {
		return nil
	}

	slice := make(types.FsSlice, 0, len(specs))
	for _, spec := range specs {
		slice = append(slice, types.FieldSpec{
			Group: spec.Group, Version: spec.Version, Kind: spec.Kind,
			Path: spec.Path,
		})
	}

	return kyaml.NewRNode(root).PipeE(fsslice.Filter{
		FsSlice: slice,
		SetValue: func(node *kyaml.RNode) error {
			if node.YNode().Kind != yaml.ScalarNode {
				return fmt.Errorf("image field spec does not locate a string")
			}
			add(node.YNode())
			return nil
		},
	})
}

// mappingField returns the value of the mapping field name of node, or nil.
func mappingField(node *yaml.Node, name string) *yaml.Node {
	if value := field(node, name); value != nil && value.Kind == yaml.MappingNode {
		return value
	}
	return nil
}

// scalarField returns the value of the scalar field name of node, or nil.
func scalarField(node *yaml.Node, name string) *yaml.Node {
	if value := field(node, name); value != nil && value.Kind == yaml.ScalarNode {
		return value
	}
	return nil
}

// field returns the value of the field name of the mapping node, or nil.
func field(node *yaml.Node, name string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == name {
			return node.Content[i+1]
		}
	}
	return nil
}

// References returns the image references of the manifest as written, in
// document order and without duplicates.
func (m *Manifest) References() []string {
	refs := make([]string, 0, len(m.fields))
	seen := map[string]bool{}
	for _, node := range m.fields {
		if !seen[node.Value] {
			seen[node.Value] = true
			refs = append(refs, node.Value)
		}
	}
	return refs
}

// Rewrite replaces every reference that is a key of rewrites with its value
// and reports whether anything changed.
func (m *Manifest) Rewrite(rewrites map[string]string) bool {
	changed := false
	for _, node := range m.fields {
		if rewritten, ok := rewrites[node.Value]; ok && rewritten != node.Value {
			node.Value = rewritten
			changed = true
		}
	}
	return changed
}

// Encode returns the manifest as YAML, formatted as the render pipeline
// formats every manifest.
func (m *Manifest) Encode() ([]byte, error) {
	var out bytes.Buffer

	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)

	for _, doc := range m.docs {
		if err := encoder.Encode(doc); err != nil {
			return nil, fmt.Errorf("failed to encode manifest: %w", err)
		}
	}

	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("failed to encode manifest: %w", err)
	}

	return out.Bytes(), nil
}
