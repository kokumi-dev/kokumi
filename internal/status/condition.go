package status

import (
	"context"
	"fmt"
	"time"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type mutateStatus[T client.Object] func(latest T)

// setCondition re-fetches obj, applies mutate, and patches the status with an
// optimistic lock. A conflict is returned so the reconciler requeues and
// retries. The re-fetch discards the caller's copy instead of the other way
// around, so it must only be used for writes that may be recomputed at will.
func setCondition[T client.Object](ctx context.Context, c client.Client, obj T, mutate mutateStatus[T]) error {
	latest := obj.DeepCopyObject().(T)
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), latest); err != nil {
		return fmt.Errorf("failed to re-fetch %T: %w", obj, err)
	}

	before := latest.DeepCopyObject().(T)
	mutate(latest)

	if err := patchStatus(ctx, c, latest, before); err != nil {
		return err
	}
	// Keep the caller's copy anchored to the current resource version, so a
	// later compare-and-swap write in the same reconcile does not conflict
	// against the patch that just landed.
	obj.SetResourceVersion(latest.GetResourceVersion())
	return nil
}

// commitCondition applies mutate to obj itself and patches the status with an
// optimistic lock anchored to the resource version the reconciler observed.
// Unlike SetCondition it never re-fetches, so a stale cache read can never
// overwrite writes that must not be lost, such as sealed approval votes. The
// mutation is applied to the caller's object, so the committed status is
// visible to the code that follows in the same reconcile.
func commitCondition[T client.Object](ctx context.Context, c client.Client, obj T, mutate mutateStatus[T]) error {
	before := obj.DeepCopyObject().(T)
	mutate(obj)
	return patchStatus(ctx, c, obj, before)
}

// patchStatus patches mutated relative to before unless the mutation was a
// no-op, and turns conflicts into retriable errors for the reconciler.
func patchStatus[T client.Object](ctx context.Context, c client.Client, mutated, before T) error {
	logger := log.FromContext(ctx)

	if apiequality.Semantic.DeepEqual(mutated, before) {
		return nil
	}

	if err := c.Status().Patch(ctx, mutated, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("Status update conflict, will retry on requeue", "name", mutated.GetName(), "namespace", mutated.GetNamespace())
		}
		return fmt.Errorf("failed to update %T status: %w", mutated, err)
	}

	return nil
}

// NewCondition returns the standard Ready condition for the given generation.
func NewCondition(generation int64, condStatus metav1.ConditionStatus, reason, msg string) metav1.Condition {
	return newTypedCondition("Ready", generation, condStatus, reason, msg)
}

func newTypedCondition(condType string, generation int64, condStatus metav1.ConditionStatus, reason, msg string) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             condStatus,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
}
