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

// Package resolve computes the effective rendering spec for an Order by merging
// Menu base configuration with Order-level overrides.
// It is a pure transform layer: no I/O, no Kubernetes client calls.
package resolve

import (
	"fmt"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// EffectiveSpec holds the resolved source, render config, and patches
// that the order service will use to process an Order.
type EffectiveSpec struct {
	Source  deliveryv1alpha1.OCISource
	Render  *deliveryv1alpha1.Render
	Patches []deliveryv1alpha1.Patch
}

// FromOrder builds an EffectiveSpec directly from a standalone Order
// (one that has no menuRef). The Order's own fields are used as-is.
func FromOrder(order *deliveryv1alpha1.Order) (*EffectiveSpec, error) {
	if order.Spec.Source == nil {
		return nil, fmt.Errorf("either source or menuRef must be set")
	}

	return &EffectiveSpec{
		Source:  *order.Spec.Source,
		Render:  order.Spec.Render,
		Patches: order.Spec.Patches,
	}, nil
}

// ForMenu builds an EffectiveSpec for a Menu-based Order.
// The Menu's base config is merged with validated consumer overrides.
func ForMenu(menu *deliveryv1alpha1.Menu, order *deliveryv1alpha1.Order) (*EffectiveSpec, error) {
	mergedRender, err := mergeRender(menu, order)
	if err != nil {
		return nil, fmt.Errorf("helm values override violation: %w", err)
	}

	mergedPatches, err := mergePatches(menu, order)
	if err != nil {
		return nil, fmt.Errorf("patch override violation: %w", err)
	}

	return &EffectiveSpec{
		Source:  menu.Spec.Source,
		Render:  mergedRender,
		Patches: mergedPatches,
	}, nil
}
