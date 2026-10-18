package conditions

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

func TestSet(t *testing.T) {
	const conditionType = deliveryv1alpha1.ConditionTypeReady
	earlier := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))

	tests := []struct {
		name           string
		existing       []metav1.Condition
		mark           func(Setter)
		wantStatus     metav1.ConditionStatus
		wantReason     string
		wantTransition bool
	}{
		{
			name:           "adds a condition",
			mark:           func(obj Setter) { MarkTrue(obj, conditionType, deliveryv1alpha1.ReasonReady, "ready") },
			wantStatus:     metav1.ConditionTrue,
			wantReason:     deliveryv1alpha1.ReasonReady,
			wantTransition: true,
		},
		{
			name: "keeps the transition time while the status holds",
			existing: []metav1.Condition{{
				Type: conditionType, Status: metav1.ConditionFalse, Reason: deliveryv1alpha1.ReasonProductNotFound, LastTransitionTime: earlier,
			}},
			mark:       func(obj Setter) { MarkFalse(obj, conditionType, deliveryv1alpha1.ReasonProductNotReady, "not ready") },
			wantStatus: metav1.ConditionFalse,
			wantReason: deliveryv1alpha1.ReasonProductNotReady,
		},
		{
			name: "moves the transition time with the status",
			existing: []metav1.Condition{{
				Type: conditionType, Status: metav1.ConditionTrue, Reason: deliveryv1alpha1.ReasonReady, LastTransitionTime: earlier,
			}},
			mark:           func(obj Setter) { MarkUnknown(obj, conditionType, deliveryv1alpha1.ReasonProcessing, "rendering") },
			wantStatus:     metav1.ConditionUnknown,
			wantReason:     deliveryv1alpha1.ReasonProcessing,
			wantTransition: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := &deliveryv1alpha1.Order{Generation: 3}
			order.Status.Conditions = tt.existing

			tt.mark(order)

			got := meta.FindStatusCondition(order.Status.Conditions, conditionType)
			if got == nil {
				t.Fatal("condition not set")
			}
			if got.Status != tt.wantStatus || got.Reason != tt.wantReason {
				t.Errorf("got %s/%s, want %s/%s", got.Status, got.Reason, tt.wantStatus, tt.wantReason)
			}
			if got.ObservedGeneration != 3 {
				t.Errorf("observedGeneration = %d, want 3", got.ObservedGeneration)
			}
			if transitioned := !got.LastTransitionTime.Equal(&earlier); transitioned != tt.wantTransition {
				t.Errorf("transitioned = %t, want %t", transitioned, tt.wantTransition)
			}
		})
	}
}

func TestRemove(t *testing.T) {
	product := &deliveryv1alpha1.Product{}
	MarkTrue(product, deliveryv1alpha1.ConditionTypeSourceVerified, deliveryv1alpha1.ReasonSucceeded, "verified")
	MarkTrue(product, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonResolved, "resolved")

	Remove(product, deliveryv1alpha1.ConditionTypeSourceVerified)

	if meta.FindStatusCondition(product.Status.Conditions, deliveryv1alpha1.ConditionTypeSourceVerified) != nil {
		t.Error("SourceVerified not removed")
	}
	if !meta.IsStatusConditionTrue(product.Status.Conditions, deliveryv1alpha1.ConditionTypeReady) {
		t.Error("Ready removed")
	}
}

func TestHasReason(t *testing.T) {
	order := &deliveryv1alpha1.Order{}
	if HasReason(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessing) {
		t.Error("HasReason without a condition")
	}
	MarkUnknown(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessing, "rendering")
	if !HasReason(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonProcessing) {
		t.Error("HasReason missed the reason")
	}
	if HasReason(order, deliveryv1alpha1.ConditionTypeReady, deliveryv1alpha1.ReasonReady) {
		t.Error("HasReason matched another reason")
	}
}
