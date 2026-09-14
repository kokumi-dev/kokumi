package resolve

import (
	"testing"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestForMenuPreRendered(t *testing.T) {
	const testPath = ".spec.replicas"
	const appName = "app"
	const depKind = "Deployment"

	menu := &deliveryv1alpha1.Menu{
		Spec: deliveryv1alpha1.MenuSpec{
			Overrides: deliveryv1alpha1.OverridePolicy{
				Values:  deliveryv1alpha1.ValueOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyNone},
				Patches: deliveryv1alpha1.PatchOverridePolicy{Policy: deliveryv1alpha1.OverridePolicyAll},
			},
			// Menu patches are baked into the pre-rendered artifact.
			Patches: []deliveryv1alpha1.Patch{
				{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: "basePkg"}, Set: map[string]string{testPath: "1"}},
			},
		},
		Status: deliveryv1alpha1.MenuStatus{
			Source: &deliveryv1alpha1.MenuSourceStatus{
				OCI:         "oci://registry/rendered/app",
				Version:     "1.0.0",
				PreRendered: true,
			},
		},
	}

	t.Run("renders are dropped and menu patches not re-merged", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{
			Spec: deliveryv1alpha1.OrderSpec{
				Patches: []deliveryv1alpha1.Patch{
					{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: "5"}},
				},
			},
		}

		eff, err := ForMenu(menu, order)
		if err != nil {
			t.Fatalf("ForMenu: %v", err)
		}

		if eff.Render != nil {
			t.Errorf("expected nil render, got %+v", eff.Render)
		}
		if len(eff.Patches) != 1 || eff.Patches[0].Target.Name != appName {
			t.Errorf("expected only consumer patches, got %+v", eff.Patches)
		}
		if eff.Source.OCI != "oci://registry/rendered/app" {
			t.Errorf("expected pre-rendered source, got %q", eff.Source.OCI)
		}
	})

	t.Run("value overrides are rejected", func(t *testing.T) {
		order := &deliveryv1alpha1.Order{
			Spec: deliveryv1alpha1.OrderSpec{
				Render: &deliveryv1alpha1.Render{
					Helm: &deliveryv1alpha1.HelmRender{},
				},
			},
		}

		_, err := ForMenu(menu, order)
		if err == nil {
			t.Fatal("expected error for value override on pre-rendered menu")
		}
	})

	t.Run("patches policy None rejects consumer patches", func(t *testing.T) {
		noneMenu := menu.DeepCopy()
		noneMenu.Spec.Overrides.Patches.Policy = deliveryv1alpha1.OverridePolicyNone

		order := &deliveryv1alpha1.Order{
			Spec: deliveryv1alpha1.OrderSpec{
				Patches: []deliveryv1alpha1.Patch{
					{Target: deliveryv1alpha1.PatchTarget{Kind: depKind, Name: appName}, Set: map[string]string{testPath: "5"}},
				},
			},
		}

		if _, err := ForMenu(noneMenu, order); err == nil {
			t.Fatal("expected error for patch override with policy None")
		}
	})
}
