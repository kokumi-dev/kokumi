package index

import (
	"context"
	"slices"

	corev1 "k8s.io/api/core/v1"
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
		{&deliveryv1alpha1.Order{}, FieldRegistryRefNames, orderRegistries},
		{&deliveryv1alpha1.Product{}, FieldRegistryRefNames, productRegistries},
		{&deliveryv1alpha1.Product{}, FieldVerifySecretNames, productVerifySecrets},
		{&deliveryv1alpha1.Registry{}, FieldRegistrySecretNames, registrySecrets},
		{&deliveryv1alpha1.Repository{}, FieldRegistryRefName, repositoryRegistry},
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

// OrdersForRegistry returns the Orders that list the named Registry in
// their registryRefs. It requires the cache indexes registered by Setup.
func OrdersForRegistry(ctx context.Context, r client.Reader, namespace, registryName string) ([]deliveryv1alpha1.Order, error) {
	orders := &deliveryv1alpha1.OrderList{}
	if err := r.List(ctx, orders,
		client.InNamespace(namespace),
		client.MatchingFields{FieldRegistryRefNames: registryName},
	); err != nil {
		return nil, err
	}
	return orders.Items, nil
}

// ProductsForRegistry returns the Products that list the named Registry in
// their registryRefs. It requires the cache indexes registered by Setup.
func ProductsForRegistry(ctx context.Context, r client.Reader, namespace, registryName string) ([]deliveryv1alpha1.Product, error) {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products,
		client.InNamespace(namespace),
		client.MatchingFields{FieldRegistryRefNames: registryName},
	); err != nil {
		return nil, err
	}
	return products.Items, nil
}

// ProductsForSecret returns the Products whose spec.source.verify reads the
// named Secret. It requires the cache indexes registered by Setup.
func ProductsForSecret(ctx context.Context, r client.Reader, namespace, secretName string) ([]deliveryv1alpha1.Product, error) {
	products := &deliveryv1alpha1.ProductList{}
	if err := r.List(ctx, products,
		client.InNamespace(namespace),
		client.MatchingFields{FieldVerifySecretNames: secretName},
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

func productRegistries(obj client.Object) []string {
	product, ok := obj.(*deliveryv1alpha1.Product)
	if !ok {
		return nil
	}
	return refNames(product.Spec.RegistryRefs)
}

func productVerifySecrets(obj client.Object) []string {
	product, ok := obj.(*deliveryv1alpha1.Product)
	if !ok || product.Spec.Source.Verify == nil {
		return nil
	}
	verify := product.Spec.Source.Verify
	var names []string
	if ref := verify.SecretRef; ref != nil {
		names = append(names, ref.Name)
	}
	if ref := verify.TrustedRootSecretRef; ref != nil && !slices.Contains(names, ref.Name) {
		names = append(names, ref.Name)
	}
	return names
}

func orderRegistries(obj client.Object) []string {
	order, ok := obj.(*deliveryv1alpha1.Order)
	if !ok {
		return nil
	}
	return refNames(order.Spec.RegistryRefs)
}

// refNames returns the names refs reference.
func refNames(refs []corev1.LocalObjectReference) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

// RegistriesForSecret returns the Registries whose secretRef or
// certSecretRef names the Secret. It requires the cache indexes registered
// by Setup.
func RegistriesForSecret(ctx context.Context, r client.Reader, namespace, secretName string) ([]deliveryv1alpha1.Registry, error) {
	registries := &deliveryv1alpha1.RegistryList{}
	if err := r.List(ctx, registries,
		client.InNamespace(namespace),
		client.MatchingFields{FieldRegistrySecretNames: secretName},
	); err != nil {
		return nil, err
	}
	return registries.Items, nil
}

// RepositoriesForRegistry returns the Repositories read through the named
// Registry. It requires the cache indexes registered by Setup.
func RepositoriesForRegistry(ctx context.Context, r client.Reader, namespace, registryName string) ([]deliveryv1alpha1.Repository, error) {
	repositories := &deliveryv1alpha1.RepositoryList{}
	if err := r.List(ctx, repositories,
		client.InNamespace(namespace),
		client.MatchingFields{FieldRegistryRefName: registryName},
	); err != nil {
		return nil, err
	}
	return repositories.Items, nil
}

func registrySecrets(obj client.Object) []string {
	reg, ok := obj.(*deliveryv1alpha1.Registry)
	if !ok {
		return nil
	}
	var names []string
	if ref := reg.Spec.SecretRef; ref != nil {
		names = append(names, ref.Name)
	}
	if ref := reg.Spec.CertSecretRef; ref != nil && !slices.Contains(names, ref.Name) {
		names = append(names, ref.Name)
	}
	return names
}

func repositoryRegistry(obj client.Object) []string {
	repo, ok := obj.(*deliveryv1alpha1.Repository)
	if !ok || repo.Spec.RegistryRef == nil {
		return nil
	}
	return []string{repo.Spec.RegistryRef.Name}
}
