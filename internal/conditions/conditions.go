// Package conditions sets the status conditions of Kokumi objects. Every
// condition it sets records the generation of its object, so clients can
// tell whether it describes the current spec. It only changes the object in
// memory; the controller writes the status.
package conditions

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// maxMessageLength is the most a condition message may hold, as the API
// validates it.
const maxMessageLength = 32768

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
// generation of obj. Its lastTransitionTime changes only with its status. A
// message longer than the API accepts is cut.
func Set(obj Setter, conditionType string, status metav1.ConditionStatus, reason, message string) {
	conditions := obj.GetConditions()
	meta.SetStatusCondition(&conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            Truncate(message, maxMessageLength),
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

// List lists items for a message, separated by sep: all of them when there
// are at most limit, else the first limit and how many more there are.
func List(items []string, sep string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, sep)
	}
	return strings.Join(items[:limit], sep) + sep + "and " + strconv.Itoa(len(items)-limit) + " more"
}

// Truncate cuts s to at most limit bytes, on a rune boundary, and marks the
// cut with an ellipsis.
func Truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	const ellipsis = "…"
	cut := limit - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}
