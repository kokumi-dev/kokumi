package resolve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// mergeRender deep-merges the Order's Helm values over the Product's render
// config. The render type always comes from the Product.
func mergeRender(product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order) (*deliveryv1alpha1.Render, error) {
	result := product.Spec.Render.DeepCopy()

	if order.Spec.Values == nil || len(order.Spec.Values.Raw) == 0 {
		return result, nil
	}

	if result == nil || result.Helm == nil {
		return nil, fmt.Errorf("values are only allowed when the Product renders with Helm; Product %q does not", product.Name)
	}

	orderValues, err := decodeValues(order.Spec.Values.Raw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Order values: %w", err)
	}

	if err := validateValues(product.Spec.Overrides.Values, orderValues); err != nil {
		return nil, fmt.Errorf("values not allowed by Product %q: %w", product.Name, err)
	}

	baseValues := map[string]any{}
	if result.Helm.Values != nil && len(result.Helm.Values.Raw) > 0 {
		if baseValues, err = decodeValues(result.Helm.Values.Raw); err != nil {
			return nil, fmt.Errorf("failed to parse Product values: %w", err)
		}
	}

	raw, err := json.Marshal(mergeMaps(baseValues, orderValues))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged values: %w", err)
	}

	result.Helm.Values = &apiextensionsv1.JSON{Raw: raw}

	return result, nil
}

// decodeValues decodes Helm values, keeping numbers as written: float64
// would round large integers and change the rendered output.
func decodeValues(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, err
	}
	return values, nil
}

// mergePatches merges the Order's patches over the Product's patches. Patches
// on the same target are combined; the Order wins on the same path.
func mergePatches(product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order) ([]deliveryv1alpha1.Patch, error) {
	if len(order.Spec.Patches) == 0 {
		return product.Spec.Patches, nil
	}

	if err := validatePatches(product.Spec.Overrides.Patches, order.Spec.Patches); err != nil {
		return nil, fmt.Errorf("patches not allowed by Product %q: %w", product.Name, err)
	}

	type patchKey struct {
		Kind, Name, Namespace string
	}

	merged := make(map[patchKey]map[string]string)
	ordering := make([]patchKey, 0)

	for _, p := range slices.Concat(product.Spec.Patches, order.Spec.Patches) {
		key := patchKey{p.Target.Kind, p.Target.Name, p.Target.Namespace}
		if _, exists := merged[key]; !exists {
			merged[key] = make(map[string]string)
			ordering = append(ordering, key)
		}
		maps.Copy(merged[key], p.Set)
	}

	result := make([]deliveryv1alpha1.Patch, 0, len(merged))
	for _, key := range ordering {
		result = append(result, deliveryv1alpha1.Patch{
			Target: deliveryv1alpha1.PatchTarget{
				Kind:      key.Kind,
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Set: merged[key],
		})
	}

	return result, nil
}

// validateEdits checks the Order's edits against the Product's patch
// overrides. Edits are never merged with the Product's patches; they are
// applied after them.
func validateEdits(product *deliveryv1alpha1.Product, order *deliveryv1alpha1.Order) error {
	if err := validatePatches(product.Spec.Overrides.Patches, order.Spec.Edits); err != nil {
		return fmt.Errorf("edits not allowed by Product %q: %w", product.Name, err)
	}
	return nil
}

// validateValues checks values against the Product's value overrides. Policy
// None, like an unset policy, allows no values.
func validateValues(overrides deliveryv1alpha1.ValueOverrides, values map[string]any) error {
	if overrides.Policy == deliveryv1alpha1.OverridePolicyAll {
		return nil
	}

	allowed := map[string]bool{}
	if overrides.Policy == deliveryv1alpha1.OverridePolicyListed {
		for _, p := range overrides.Paths {
			allowed[p] = true
		}
	}

	return validateValuePaths("", values, allowed)
}

// validatePatches checks patches against the Product's patch overrides.
// Policy None, like an unset policy, allows no patches.
func validatePatches(overrides deliveryv1alpha1.PatchOverrides, patches []deliveryv1alpha1.Patch) error {
	if overrides.Policy == deliveryv1alpha1.OverridePolicyAll {
		return nil
	}

	var allowed []deliveryv1alpha1.AllowedPatchTarget
	if overrides.Policy == deliveryv1alpha1.OverridePolicyListed {
		allowed = overrides.Targets
	}

	for _, patch := range patches {
		if err := validatePatchAllowed(patch, allowed); err != nil {
			return err
		}
	}

	return nil
}

// mergeMaps deep-merges src into dst, with src values taking precedence.
func mergeMaps(dst, src map[string]any) map[string]any {
	result := make(map[string]any, len(dst))
	maps.Copy(result, dst)
	for k, v := range src {
		if srcMap, ok := v.(map[string]any); ok {
			if dstMap, ok := result[k].(map[string]any); ok {
				result[k] = mergeMaps(dstMap, srcMap)
				continue
			}
		}
		result[k] = v
	}
	return result
}

// validateValuePaths ensures all leaf keys in the values map are in the allowed set.
func validateValuePaths(prefix string, values map[string]any, allowed map[string]bool) error {
	for k, v := range values {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			if err := validateValuePaths(path, nested, allowed); err != nil {
				return err
			}
		} else {
			if !allowed[path] {
				return fmt.Errorf("value path %q is not allowed", path)
			}
		}
	}
	return nil
}

// validatePatchAllowed checks a patch against the allowed targets. An allowed
// target without a namespace matches the resource in any namespace, as it does
// when patching. The paths of all matching targets are allowed.
func validatePatchAllowed(patch deliveryv1alpha1.Patch, allowed []deliveryv1alpha1.AllowedPatchTarget) error {
	target := patch.Target
	paths := map[string]bool{}
	matched := false
	for _, a := range allowed {
		if a.Target.Kind != target.Kind || a.Target.Name != target.Name {
			continue
		}
		if a.Target.Namespace != "" && a.Target.Namespace != target.Namespace {
			continue
		}
		matched = true
		for _, p := range a.Paths {
			paths[p] = true
		}
	}

	if !matched {
		return fmt.Errorf("patch target %s is not allowed", describeTarget(target))
	}

	for _, path := range slices.Sorted(maps.Keys(patch.Set)) {
		if !paths[path] {
			return fmt.Errorf("patch path %q on %s is not allowed", path, describeTarget(target))
		}
	}

	return nil
}

// describeTarget returns kind/name, or namespace/kind/name when the target is
// namespaced.
func describeTarget(target deliveryv1alpha1.PatchTarget) string {
	if target.Namespace == "" {
		return target.Kind + "/" + target.Name
	}
	return target.Namespace + "/" + target.Kind + "/" + target.Name
}
