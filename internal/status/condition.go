package status

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type mutateStatus[T client.Object] func(latest T)

// SetCondition re-fetches obj, applies mutate, and patches the status. A
// conflict is returned so the reconciler requeues and retries.
func SetCondition[T client.Object](ctx context.Context, c client.Client, obj T, mutate mutateStatus[T]) error {
	logger := log.FromContext(ctx)

	latest := obj.DeepCopyObject().(T)
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		return fmt.Errorf("failed to re-fetch %T: %w", obj, err)
	}

	before := latest.DeepCopyObject().(T)
	mutate(latest)

	if err := c.Status().Patch(ctx, latest, client.MergeFrom(before)); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("Status update conflict, will retry on requeue", "name", latest.GetName(), "namespace", latest.GetNamespace())
		}
		return fmt.Errorf("failed to update %T status: %w", obj, err)
	}

	return nil
}

// NewCondition returns the standard Ready condition for the given generation.
func NewCondition(generation int64, condStatus metav1.ConditionStatus, reason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               "Ready",
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}
