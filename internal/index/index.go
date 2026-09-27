package index

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	deliveryv1alpha1 "github.com/kokumi-dev/kokumi/api/v1alpha1"
)

// Setup registers all field indexes with the manager's cache.
func Setup(ctx context.Context, indexer client.FieldIndexer) error {
	indexes := []struct {
		obj     client.Object
		field   string
		extract client.IndexerFunc
	}{
		{&deliveryv1alpha1.Approval{}, FieldPreparationRefName, approvalPreparation},
		{&deliveryv1alpha1.Preparation{}, FieldOrderName, preparationOrder},
		{&deliveryv1alpha1.Order{}, FieldSourcePantryRefName, orderSourcePantry},
		{&deliveryv1alpha1.Order{}, FieldDestinationPantryRefName, orderDestinationPantry},
	}
	for _, idx := range indexes {
		if err := indexer.IndexField(ctx, idx.obj, idx.field, idx.extract); err != nil {
			return err
		}
	}
	return nil
}

// ApprovalsForPreparation returns the Approvals referencing prep by name.
func ApprovalsForPreparation(ctx context.Context, r client.Reader, prep *deliveryv1alpha1.Preparation) ([]deliveryv1alpha1.Approval, error) {
	approvals := &deliveryv1alpha1.ApprovalList{}
	if err := r.List(ctx, approvals,
		client.InNamespace(prep.Namespace),
		client.MatchingFields{FieldPreparationRefName: prep.Name},
	); err != nil {
		return nil, err
	}
	return approvals.Items, nil
}

// PreparationsForOrder returns the Preparations of the named Order.
func PreparationsForOrder(ctx context.Context, r client.Reader, namespace, orderName string) ([]deliveryv1alpha1.Preparation, error) {
	preparations := &deliveryv1alpha1.PreparationList{}
	if err := r.List(ctx, preparations,
		client.InNamespace(namespace),
		client.MatchingFields{FieldOrderName: orderName},
	); err != nil {
		return nil, err
	}
	return preparations.Items, nil
}

// OrdersForPantry returns the Orders whose source or destination references
// the named Pantry. It requires the cache indexes registered by Setup.
func OrdersForPantry(ctx context.Context, r client.Reader, namespace, pantryName string) ([]deliveryv1alpha1.Order, error) {
	var orders []deliveryv1alpha1.Order
	seen := map[string]struct{}{}
	for _, field := range []string{FieldSourcePantryRefName, FieldDestinationPantryRefName} {
		list := &deliveryv1alpha1.OrderList{}
		if err := r.List(ctx, list, client.InNamespace(namespace), client.MatchingFields{field: pantryName}); err != nil {
			return nil, err
		}
		for _, o := range list.Items {
			if _, dup := seen[o.Name]; dup {
				continue
			}
			seen[o.Name] = struct{}{}
			orders = append(orders, o)
		}
	}
	return orders, nil
}

func approvalPreparation(obj client.Object) []string {
	approval, ok := obj.(*deliveryv1alpha1.Approval)
	if !ok {
		return nil
	}
	return []string{approval.Spec.PreparationRef.Name}
}

func preparationOrder(obj client.Object) []string {
	preparation, ok := obj.(*deliveryv1alpha1.Preparation)
	if !ok {
		return nil
	}
	return []string{preparation.Spec.OrderName}
}

func orderSourcePantry(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok || order.Spec.Source == nil || order.Spec.Source.PantryRef == nil {
		return nil
	}
	return []string{order.Spec.Source.PantryRef.Name}
}

func orderDestinationPantry(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok || order.Spec.Destination == nil || order.Spec.Destination.PantryRef == nil {
		return nil
	}
	return []string{order.Spec.Destination.PantryRef.Name}
}
