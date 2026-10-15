package index

import (
	"context"
	"slices"

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
		{&deliveryv1alpha1.Order{}, FieldProductRefName, orderProduct},
		{&deliveryv1alpha1.Order{}, FieldDestinationPantryRefName, orderDestinationPantry},
		{&deliveryv1alpha1.Product{}, FieldPantryRefNames, productPantries},
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

// OrdersForProduct returns the Orders that are variants of the named Product.
// It requires the cache indexes registered by Setup.
func OrdersForProduct(ctx context.Context, r client.Reader, namespace, productName string) ([]deliveryv1alpha1.Order, error) {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders,
		client.InNamespace(namespace),
		client.MatchingFields{FieldProductRefName: productName},
	); err != nil {
		return nil, err
	}
	return orders.Items, nil
}

// OrdersForPantry returns the Orders whose destination references the named
// Pantry. Orders follow their Product's source Pantry through the Product.
// It requires the cache indexes registered by Setup.
func OrdersForPantry(ctx context.Context, r client.Reader, namespace, pantryName string) ([]deliveryv1alpha1.Order, error) {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders,
		client.InNamespace(namespace),
		client.MatchingFields{FieldDestinationPantryRefName: pantryName},
	); err != nil {
		return nil, err
	}
	return orders.Items, nil
}

// ProductsForPantry returns the Products whose source or vendor destination
// references the named Pantry. It requires the cache indexes registered by
// Setup.
func ProductsForPantry(ctx context.Context, r client.Reader, namespace, pantryName string) ([]deliveryv1alpha1.Product, error) {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products,
		client.InNamespace(namespace),
		client.MatchingFields{FieldPantryRefNames: pantryName},
	); err != nil {
		return nil, err
	}
	return products.Items, nil
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

func orderProduct(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	return []string{order.Spec.ProductRef.Name}
}

func productPantries(obj client.Object) []string {
	product, ok := obj.(*deliveryv1alpha1.Product)
	if !ok {
		return nil
	}
	var names []string
	if ref := product.Spec.Source.PantryRef; ref != nil {
		names = append(names, ref.Name)
	}
	if vendor := product.Spec.Vendor; vendor != nil && vendor.PantryRef != nil && !slices.Contains(names, vendor.PantryRef.Name) {
		names = append(names, vendor.PantryRef.Name)
	}
	return names
}

func orderDestinationPantry(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok || order.Spec.Destination == nil || order.Spec.Destination.PantryRef == nil {
		return nil
	}
	return []string{order.Spec.Destination.PantryRef.Name}
}
