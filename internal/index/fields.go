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

	// FieldPantryRefNames indexes Products by the Pantries their source and
	// vendor destination reference (cache only).
	FieldPantryRefNames = "pantryRefNames"

	// FieldDestinationPantryRefName indexes Orders by spec.destination.pantryRef.name (cache only).
	FieldDestinationPantryRefName = "spec.destination.pantryRef.name"
)
