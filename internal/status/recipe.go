package status

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Updater updates the status of a Recipe object.
type RecipeUpdater struct {
	client client.Client
}

// New returns an Updater backed by the given client.
func NewRecipeUpdater(c client.Client) *RecipeUpdater {
	return &RecipeUpdater{client: c}
}

// Processing marks the Recipe as actively being processed.
func (u *RecipeUpdater) Processing(ctx context.Context, r *deliveryv1alpha1.Recipe, configHash string) error {
	return u.set(ctx, r, deliveryv1alpha1.RecipePhaseProcessing, configHash, "Processing component configuration")
}

// Ready marks the Recipe as successfully reconciled.
func (u *RecipeUpdater) Ready(ctx context.Context, r *deliveryv1alpha1.Recipe, configHash, msg string) error {
	return u.set(ctx, r, deliveryv1alpha1.RecipePhaseReady, configHash, msg)
}

// Failed marks the Recipe as failed with the supplied error as the message.
func (u *RecipeUpdater) Failed(ctx context.Context, r *deliveryv1alpha1.Recipe, err error) error {
	r.Status.LatestConfigHash = ""
	return u.set(ctx, r, deliveryv1alpha1.RecipePhaseFailed, "", err.Error())
}

func (u *RecipeUpdater) set(
	ctx context.Context,
	recipe *deliveryv1alpha1.Recipe,
	phase deliveryv1alpha1.RecipePhase,
	configHash string,
	msg string,
) error {
	recipe.Status.Phase = phase

	if phase == deliveryv1alpha1.RecipePhaseReady || phase == deliveryv1alpha1.RecipePhaseFailed {
		recipe.Status.ObservedGeneration = recipe.Generation
	}

	if configHash != "" {
		recipe.Status.LatestConfigHash = configHash
	}

	condition := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             string(phase),
		Message:            msg,
		ObservedGeneration: recipe.Generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}

	switch phase {
	case deliveryv1alpha1.RecipePhaseReady:
		condition.Status = metav1.ConditionTrue
	case deliveryv1alpha1.RecipePhaseFailed:
		condition.Type = "Degraded"
		condition.Reason = "ProcessingFailed"
	}

	meta.SetStatusCondition(&recipe.Status.Conditions, condition)

	if err := u.client.Status().Update(ctx, recipe); err != nil {
		if apierrors.IsConflict(err) {
			return nil
		}

		return fmt.Errorf("failed to update Recipe status: %w", err)
	}

	return nil
}
