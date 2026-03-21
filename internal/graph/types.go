// Package graph defines the core data types and constants for the codemap
// semantic graph: nodes (symbols) and edges (relationships).
package graph

// Relation constants for Edge.Relation.
const (
	RelationCalls      = "calls"
	RelationImplements = "implements"
	RelationReferences = "references"
	RelationImports    = "imports"
)

// Node represents a named symbol in the codebase.
type Node struct {
	ID        string // stable SHA256 hash of (FilePath + ":" + Name + ":" + Kind)
	Name      string
	Kind      string // function | method | type | class | interface | ...
	FilePath  string
	LineStart int
	LineEnd   int
	ColStart  int
	ColEnd    int
	SymbolURI string // LSP file:// URI, populated during LSP enrichment
}

// Edge represents a directed relationship between two symbols.
type Edge struct {
	SourceID string
	TargetID string
	Relation string // one of the Relation* constants
}
