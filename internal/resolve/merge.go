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

package resolve

import (
	"encoding/json"
	"fmt"
	"maps"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// mergeRender merges the Menu's base Helm render config with the Order's overrides.
func mergeRender(menu *deliveryv1alpha1.Menu, order *deliveryv1alpha1.Order) (*deliveryv1alpha1.Render, error) {
	baseRender := menu.Spec.Render
	if baseRender == nil {
		if order.Spec.Render != nil && order.Spec.Render.Helm != nil && order.Spec.Render.Helm.Values != nil {
			if menu.Spec.Overrides.Values.Policy == deliveryv1alpha1.OverridePolicyNone {
				return nil, fmt.Errorf("values overrides are not allowed by Menu %q", menu.Name)
			}
		}
		return baseRender, nil
	}

	result := baseRender.DeepCopy()

	// If no consumer Helm overrides, return base as-is.
	if order.Spec.Render == nil || order.Spec.Render.Helm == nil || order.Spec.Render.Helm.Values == nil {
		return result, nil
	}

	policy := menu.Spec.Overrides.Values

	if policy.Policy == deliveryv1alpha1.OverridePolicyNone {
		return nil, fmt.Errorf("values overrides are not allowed by Menu %q", menu.Name)
	}

	// Parse consumer values.
	var consumerValues map[string]any
	if err := json.Unmarshal(order.Spec.Render.Helm.Values.Raw, &consumerValues); err != nil {
		return nil, fmt.Errorf("failed to parse consumer Helm values: %w", err)
	}

	if policy.Policy == deliveryv1alpha1.OverridePolicyRestricted {
		allowed := make(map[string]bool, len(policy.Allowed))
		for _, p := range policy.Allowed {
			allowed[p] = true
		}
		if err := validateValuePaths("", consumerValues, allowed); err != nil {
			return nil, err
		}
	}

	// Merge base values with consumer values (consumer wins).
	var baseValues map[string]any
	if result.Helm != nil && result.Helm.Values != nil {
		if err := json.Unmarshal(result.Helm.Values.Raw, &baseValues); err != nil {
			return nil, fmt.Errorf("failed to parse base Helm values: %w", err)
		}
	} else {
		baseValues = make(map[string]any)
	}

	mergedValues := mergeMaps(baseValues, consumerValues)

	raw, err := json.Marshal(mergedValues)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal merged values: %w", err)
	}

	if result.Helm == nil {
		result.Helm = &deliveryv1alpha1.HelmRender{}
	}
	result.Helm.Values = &apiextensionsv1.JSON{Raw: raw}

	return result, nil
}

// mergePatches merges Menu base patches with Order consumer patches.
func mergePatches(menu *deliveryv1alpha1.Menu, order *deliveryv1alpha1.Order) ([]deliveryv1alpha1.Patch, error) {
	if len(order.Spec.Patches) == 0 {
		return menu.Spec.Patches, nil
	}

	policy := menu.Spec.Overrides.Patches

	if policy.Policy == deliveryv1alpha1.OverridePolicyNone {
		return nil, fmt.Errorf("patch overrides are not allowed by Menu %q", menu.Name)
	}

	if policy.Policy == deliveryv1alpha1.OverridePolicyRestricted {
		for _, cp := range order.Spec.Patches {
			if err := validatePatchAllowed(cp, policy.Allowed); err != nil {
				return nil, err
			}
		}
	}

	// Build merged result: start with base patches, then apply consumer overrides.
	type patchKey struct {
		Kind, Name, Namespace string
	}

	merged := make(map[patchKey]map[string]string)
	ordering := make([]patchKey, 0)

	for _, p := range menu.Spec.Patches {
		key := patchKey{p.Target.Kind, p.Target.Name, p.Target.Namespace}
		if _, exists := merged[key]; !exists {
			merged[key] = make(map[string]string)
			ordering = append(ordering, key)
		}
		maps.Copy(merged[key], p.Set)
	}

	for _, p := range order.Spec.Patches {
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

// validateValuePaths ensures all leaf keys in the consumer values map are in the allowed set.
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

// validatePatchAllowed checks if a consumer patch is permitted by the allowed list.
func validatePatchAllowed(patch deliveryv1alpha1.Patch, allowed []deliveryv1alpha1.AllowedPatchTarget) error {
	for _, a := range allowed {
		if a.Target.Kind == patch.Target.Kind && a.Target.Name == patch.Target.Name {
			allowedPaths := make(map[string]bool, len(a.Paths))
			for _, p := range a.Paths {
				allowedPaths[p] = true
			}
			for path := range patch.Set {
				if !allowedPaths[path] {
					return fmt.Errorf("patch path %q on %s/%s is not allowed", path, patch.Target.Kind, patch.Target.Name)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("patch target %s/%s is not allowed", patch.Target.Kind, patch.Target.Name)
}
