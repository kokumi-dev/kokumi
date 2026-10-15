package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestStateFromConditions(t *testing.T) {
	ready := func(status metav1.ConditionStatus, reason string) []metav1.Condition {
		return []metav1.Condition{{Type: deliveryv1alpha1.ConditionTypeReady, Status: status, Reason: reason}}
	}

	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       string
	}{
		{name: "no Ready condition", want: "Unknown"},
		{name: "ready", conditions: ready(metav1.ConditionTrue, "Ready"), want: "Ready"},
		{name: "processing", conditions: ready(metav1.ConditionUnknown, "Processing"), want: "Processing"},
		{name: "failed", conditions: ready(metav1.ConditionFalse, "ProcessingFailed"), want: stateFailed},
		{name: "Product not found", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductNotFound), want: stateWaiting},
		{name: "Product not ready", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductNotReady), want: stateWaiting},
		{name: "Product deleting", conditions: ready(metav1.ConditionFalse, deliveryv1alpha1.ReasonProductDeleting), want: stateWaiting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, stateFromConditions(tt.conditions))
		})
	}
}

func TestProductToDTO(t *testing.T) {
	t.Run("a deleted Product waiting for its Orders is deleting", func(t *testing.T) {
		now := metav1.Now()
		product := deliveryv1alpha1.Product{
			Name: "app", DeletionTimestamp: &now,
			Status: deliveryv1alpha1.ProductStatus{Conditions: []metav1.Condition{{
				Type: deliveryv1alpha1.ConditionTypeReady, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonInUse,
			}}},
		}

		dto := productToDTO(product)
		require.Equal(t, stateDeleting, dto.State)
		require.NotNil(t, dto.DeletionTimestamp)
	})

	t.Run("an unset override policy reads as None", func(t *testing.T) {
		dto := productToDTO(deliveryv1alpha1.Product{})
		require.Equal(t, string(deliveryv1alpha1.OverridePolicyNone), dto.Overrides.Values.Policy)
		require.Equal(t, string(deliveryv1alpha1.OverridePolicyNone), dto.Overrides.Patches.Policy)
	})

	t.Run("listed overrides round-trip", func(t *testing.T) {
		overrides := deliveryv1alpha1.Overrides{
			Values: deliveryv1alpha1.ValueOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Paths: []string{"ui.message"}},
			Patches: deliveryv1alpha1.PatchOverrides{Policy: deliveryv1alpha1.OverridePolicyListed, Targets: []deliveryv1alpha1.AllowedPatchTarget{{
				Target: deliveryv1alpha1.PatchTarget{Kind: "Deployment", Name: "app"}, Paths: []string{".spec.replicas"},
			}}},
		}

		require.Equal(t, overrides, overridesFromDTO(overridesToDTO(overrides)))
	})
}
