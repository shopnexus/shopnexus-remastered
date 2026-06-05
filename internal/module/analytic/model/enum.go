package analyticmodel

//go:generate go run shopnexus-server/cmd/genenum -type=InteractionRefType

// InteractionRefType is the kind of entity an interaction refers to, decoupled from the DB enum.
type InteractionRefType string

const (
	InteractionRefTypeProduct  InteractionRefType = "Product"
	InteractionRefTypeCategory InteractionRefType = "Category"
)
