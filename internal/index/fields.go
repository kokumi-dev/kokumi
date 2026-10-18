package index

// Field paths used with field selectors and cache field indexes. The paths
// that are also CRD selectable fields can be used against the API server.
const (
	// FieldOrderName selects Approvals and Preparations by spec.orderName.
	FieldOrderName = "spec.orderName"

	// FieldPreparationRefName selects Approvals by spec.preparationRef.name.
	FieldPreparationRefName = "spec.preparationRef.name"

	// FieldProductRefName selects Orders by spec.productRef.name.
	FieldProductRefName = "spec.productRef.name"

	// FieldRegistryRefNames indexes Products and Orders by the Registries
	// their spec.registryRefs list (cache only).
	FieldRegistryRefNames = "registryRefNames"

	// FieldVerifySecretNames indexes Products by the Secrets their
	// spec.source.verify reads keys or a trusted root from (cache only).
	FieldVerifySecretNames = "verifySecretNames"

	// FieldRegistrySecretNames indexes Registries by the Secrets their
	// secretRef and certSecretRef name (cache only).
	FieldRegistrySecretNames = "registrySecretNames"

	// FieldRegistryRefName indexes Repositories by spec.registryRef.name
	// (cache only).
	FieldRegistryRefName = "spec.registryRef.name"
)
