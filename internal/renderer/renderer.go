package renderer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	ctrl "sigs.k8s.io/controller-runtime"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// ApplyPatches applies patches to YAML content, preserving document order, comments,
// and formatting. Returns the modified content, or the original content unchanged if
// no patches matched.
func ApplyPatches(ctx context.Context, content []byte, patches []deliveryv1alpha1.Patch) ([]byte, error) {
	log := ctrl.LoggerFrom(ctx)

	decoder := yaml.NewDecoder(strings.NewReader(string(content)))

	var documents []*yaml.Node
	modified := false

	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			break
		}

		if applyPatchesToNode(ctx, &doc, patches) {
			modified = true
		}

		documents = append(documents, &doc)
	}

	if modified {
		log.Info("Patches applied to YAML content")
		return marshalYAMLNodes(documents)
	}

	return content, nil
}

// NormalizeYAML parses and re-serializes YAML to produce consistent formatting.
// This guarantees that manifests rendered without any patches have identical bytes
// for the same source, making content-addressed comparisons reliable.
func NormalizeYAML(content []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(content)))

	var documents []*yaml.Node

	for {
		var doc yaml.Node
		if err := decoder.Decode(&doc); err != nil {
			break
		}

		documents = append(documents, &doc)
	}

	return marshalYAMLNodes(documents)
}

// CalculateSpecHash computes a stable SHA-256 hash over the complete set of inputs
// that determine the content of a rendered artifact.
func CalculateSpecHash(spec deliveryv1alpha1.RecipeSpec) (string, error) {
	var builder strings.Builder

	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)

	if err := encoder.Encode(struct {
		OCI     string                   `yaml:"oci"`
		Version string                   `yaml:"version"`
		Patches []deliveryv1alpha1.Patch `yaml:"patches,omitempty"`
	}{
		OCI:     spec.Source.OCI,
		Version: spec.Source.Version,
		Patches: spec.Patches,
	}); err != nil {
		return "", fmt.Errorf("failed to encode spec for hashing: %w", err)
	}

	encoder.Close() //nolint:errcheck

	hash := sha256.Sum256([]byte(builder.String()))

	return fmt.Sprintf("sha256:%x", hash), nil
}

// applyPatchesToNode applies patches to a single YAML document node.
// Returns true if any patch was applied.
func applyPatchesToNode(ctx context.Context, docNode *yaml.Node, patches []deliveryv1alpha1.Patch) bool {
	log := ctrl.LoggerFrom(ctx)

	if docNode.Kind != yaml.DocumentNode || len(docNode.Content) == 0 {
		return false
	}

	root := docNode.Content[0]
	if root.Kind != yaml.MappingNode {
		return false
	}

	kind := getYAMLNodeField(root, "kind")
	name := getYAMLNodeField(root, "metadata", "name")
	namespace := getYAMLNodeField(root, "metadata", "namespace")

	if kind == "" || name == "" {
		return false
	}

	modified := false

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
			modified = true
		}
	}

	return modified
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

// marshalYAMLNodes re-encodes a slice of YAML document nodes with consistent 2-space indentation.
func marshalYAMLNodes(documents []*yaml.Node) ([]byte, error) {
	var builder strings.Builder

	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)

	for _, doc := range documents {
		if err := encoder.Encode(doc); err != nil {
			return nil, fmt.Errorf("failed to encode document: %w", err)
		}
	}

	encoder.Close() //nolint:errcheck

	return []byte(builder.String()), nil
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
