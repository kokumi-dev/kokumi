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

package framework

import (
	. "github.com/onsi/gomega" //nolint:revive,staticcheck
	"github.com/onsi/gomega/gstruct"
	"github.com/onsi/gomega/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// HaveCondition succeeds for an object whose status.conditions holds a
// condition of conditionType with status.
func HaveCondition(conditionType string, status metav1.ConditionStatus) types.GomegaMatcher {
	return haveCondition(gstruct.Fields{
		"Type":   Equal(conditionType),
		"Status": Equal(status),
	})
}

// HaveConditionReason succeeds for an object whose status.conditions holds a
// condition of conditionType with reason.
func HaveConditionReason(conditionType, reason string) types.GomegaMatcher {
	return haveCondition(gstruct.Fields{
		"Type":   Equal(conditionType),
		"Reason": Equal(reason),
	})
}

// BeReady succeeds for an object whose Ready condition is True.
func BeReady() types.GomegaMatcher {
	return HaveCondition(deliveryv1alpha1.ConditionTypeReady, metav1.ConditionTrue)
}

func haveCondition(fields gstruct.Fields) types.GomegaMatcher {
	return HaveField("Status.Conditions", ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, fields)))
}
