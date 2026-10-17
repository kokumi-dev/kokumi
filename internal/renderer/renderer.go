package renderer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	ctrl "sigs.k8s.io/controller-runtime"
)

// PatchTarget identifies which resource to patch.
type PatchTarget struct {
	// Kind specifies the Kubernetes resource kind to patch.
	Kind string
	// Name specifies the name of the resource to patch.
	Name string
	// Namespace optionally narrows the match to a single namespace.
	Namespace string
}

// Patch defines a modification to apply to a resource.
type Patch struct {
	// Target identifies which resource to patch.
	Target PatchTarget
	// Set contains JSONPath expressions and their values to set.
	// Keys are JSONPath expressions (e.g., ".spec.replicas").
	Set map[string]string
}

// Process normalizes YAML content and applies patches to it, one document at a
// time: it decodes a document, applies the patches that target it, encodes it
// with 2-space indentation and drops it before decoding the next. Memory
// follows the largest document instead of the whole content. Every output is
// formatted the same way, whether patches matched or not, so the same source
// always yields the same bytes. A document that does not parse fails with its
// 1-based index.
func Process(ctx context.Context, content []byte, patches []Patch) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(content))

	var out bytes.Buffer
	for i := 1; ; i++ {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return out.Bytes(), nil
			}
			return nil, fmt.Errorf("document %d: %w", i, err)
		}

		applyPatchesToNode(ctx, &doc, patches)

		if i > 1 {
			out.WriteString("---\n")
		}
		if err := encodeDocument(&out, &doc); err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
	}
}

// encodeDocument writes doc to w with 2-space indentation, as one encoder
// writes each document of a stream after the separator. Every document gets
// its own encoder because an encoder keeps every event it emitted until it is
// dropped, which would make memory follow the whole stream.
func encodeDocument(w io.Writer, doc *yaml.Node) error {
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)

	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("failed to encode: %w", err)
	}

	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to encode: %w", err)
	}

	return nil
}

// applyPatchesToNode applies the patches that target a single YAML document.
func applyPatchesToNode(ctx context.Context, docNode *yaml.Node, patches []Patch) {
	if len(patches) == 0 || docNode.Kind != yaml.DocumentNode || len(docNode.Content) == 0 {
		return
	}

	root := docNode.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}

	kind := getYAMLNodeField(root, "kind")
	name := getYAMLNodeField(root, "metadata", "name")
	namespace := getYAMLNodeField(root, "metadata", "namespace")

	if kind == "" || name == "" {
		return
	}

	log := ctrl.LoggerFrom(ctx)

	for _, patch := range patches {
		if patch.Target.Kind != kind || patch.Target.Name != name {
			continue
		}

		if patch.Target.Namespace != "" && patch.Target.Namespace != namespace {
			continue
		}

		log.Info("Applying patch", "kind", kind, "name", name, "sets", len(patch.Set))

		for jsonPath, value := range patch.Set {
			parsedValue := parseValue(value)

			if err := setYAMLNodeFieldByPath(root, jsonPath, parsedValue); err != nil {
				log.Error(err, "Failed to apply patch", "path", jsonPath, "value", value)
				continue
			}

			log.Info("Applied patch", "path", jsonPath, "value", value)
		}
	}
}

// getYAMLNodeField retrieves a scalar value from a YAML mapping node by path.
func getYAMLNodeField(node *yaml.Node, path ...string) string {
	current := node

	for _, key := range path {
		if current.Kind != yaml.MappingNode {
			return ""
		}

		found := false

		for i := 0; i < len(current.Content); i += 2 {
			if current.Content[i].Value == key {
				current = current.Content[i+1]
				found = true
				break
			}
		}

		if !found {
			return ""
		}
	}

	if current.Kind == yaml.ScalarNode {
		return current.Value
	}

	return ""
}

// setYAMLNodeFieldByPath sets a value in a YAML mapping node using a dot-separated path
// that may contain array indices (e.g. ".spec.containers[0].image").
func setYAMLNodeFieldByPath(root *yaml.Node, path string, value any) error {
	path = strings.TrimPrefix(path, ".")
	parts := strings.Split(path, ".")

	return setYAMLNodeField(root, parts, value)
}

// setYAMLNodeField recursively sets a value in a YAML mapping node.
func setYAMLNodeField(node *yaml.Node, parts []string, value any) error {
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}

	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got kind %v", node.Kind)
	}

	key := parts[0]
	isLast := len(parts) == 1

	if strings.Contains(key, "[") {
		return setYAMLNodeFieldWithArray(node, parts, value)
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != key {
			continue
		}

		if isLast {
			node.Content[i+1] = createYAMLNode(value)
			return nil
		}

		return setYAMLNodeField(node.Content[i+1], parts[1:], value)
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}

	if isLast {
		node.Content = append(node.Content, keyNode, createYAMLNode(value))
		return nil
	}

	newMapping := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{}}
	node.Content = append(node.Content, keyNode, newMapping)

	return setYAMLNodeField(newMapping, parts[1:], value)
}

// setYAMLNodeFieldWithArray handles array-index segments (e.g. "containers[0]").
func setYAMLNodeFieldWithArray(node *yaml.Node, parts []string, value any) error {
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}

	arrayName, index, err := parseArrayIndex(parts[0])
	if err != nil {
		return err
	}

	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value != arrayName {
			continue
		}

		valueNode := node.Content[i+1]

		if valueNode.Kind != yaml.SequenceNode {
			return fmt.Errorf("field %s is not a sequence", arrayName)
		}

		if index < 0 || index >= len(valueNode.Content) {
			return fmt.Errorf("array index %d out of bounds for %s (len=%d)", index, arrayName, len(valueNode.Content))
		}

		if len(parts) == 1 {
			valueNode.Content[index] = createYAMLNode(value)
			return nil
		}

		return setYAMLNodeField(valueNode.Content[index], parts[1:], value)
	}

	return fmt.Errorf("field %s not found", arrayName)
}

// createYAMLNode creates a scalar yaml.Node from a Go value.
func createYAMLNode(value any) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode}

	switch v := value.(type) {
	case string:
		node.Value = v
	case int, int64:
		node.Value = fmt.Sprintf("%d", v)
	case float64:
		node.Value = fmt.Sprintf("%v", v)
	case bool:
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	default:
		node.Value = fmt.Sprintf("%v", v)
	}

	return node
}

// parseArrayIndex parses "containers[0]" into the field name "containers" and index 0.
func parseArrayIndex(s string) (string, int, error) {
	open := strings.Index(s, "[")
	close := strings.Index(s, "]")

	if open == -1 || close == -1 {
		return "", 0, fmt.Errorf("invalid array index syntax in %q", s)
	}

	name := s[:open]
	indexStr := s[open+1 : close]

	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return "", 0, fmt.Errorf("non-integer array index in %q: %w", s, err)
	}

	return name, index, nil
}

// parseValue coerces a string into bool, int64, float64, or leaves it as string.
func parseValue(s string) any {
	if s == "true" {
		return true
	}

	if s == "false" {
		return false
	}

	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	return s
}
