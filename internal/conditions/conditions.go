// Package conditions sets the status conditions of Kokumi objects. Every
// condition it sets records the generation of its object, so clients can
// tell whether it describes the current spec. It only changes the object in
// memory; the controller writes the status.
package conditions

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Setter is an object with status conditions.
type Setter interface {
	GetGeneration() int64
	GetConditions() []metav1.Condition
	SetConditions(conditions []metav1.Condition)
}

// MarkTrue sets the condition of conditionType on obj to True.
func MarkTrue(obj Setter, conditionType, reason, message string) {
	Set(obj, conditionType, metav1.ConditionTrue, reason, message)
}

// MarkFalse sets the condition of conditionType on obj to False.
func MarkFalse(obj Setter, conditionType, reason, message string) {
	Set(obj, conditionType, metav1.ConditionFalse, reason, message)
}

// MarkUnknown sets the condition of conditionType on obj to Unknown.
func MarkUnknown(obj Setter, conditionType, reason, message string) {
	Set(obj, conditionType, metav1.ConditionUnknown, reason, message)
}

// Set sets the condition of conditionType on obj to status, observed at the
// generation of obj. Its lastTransitionTime changes only with its status.
func Set(obj Setter, conditionType string, status metav1.ConditionStatus, reason, message string) {
	conditions := obj.GetConditions()
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: obj.GetGeneration(),
	})
	obj.SetConditions(conditions)
}

// Remove removes the condition of conditionType from obj.
func Remove(obj Setter, conditionType string) {
	conditions := obj.GetConditions()
	meta.RemoveStatusCondition(&conditions, conditionType)
	obj.SetConditions(conditions)
}

// HasReason reports whether the condition of conditionType on obj has
// reason.
func HasReason(obj Setter, conditionType, reason string) bool {
	condition := meta.FindStatusCondition(obj.GetConditions(), conditionType)
	return condition != nil && condition.Reason == reason
}
