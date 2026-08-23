// Package cosmossql parses the faithful subset of the Cosmos DB SQL (core
// NoSQL) query language used by the emulator, producing a Statement whose WHERE
// clause is a shared expr AST evaluated over native map[string]any documents.
package cosmossql

import "github.com/stackshy/cloudemu/v2/services/database/driver/expr"

// ProjKind is the shape of a SELECT projection.
type ProjKind int

const (
	// ProjStar is SELECT * — the whole document.
	ProjStar ProjKind = iota
	// ProjFields is SELECT c.a, c.b AS x — an object of the named fields.
	ProjFields
	// ProjValue is SELECT VALUE c.a — the bare value of one path per document.
	ProjValue
	// ProjAggregate is SELECT VALUE COUNT(1) / SUM(c.x) — a single scalar over
	// the whole result set.
	ProjAggregate
)

// Statement is a parsed Cosmos SQL query.
type Statement struct {
	Distinct bool
	Top      int // 0 = no TOP
	Proj     Projection
	Where    expr.Node // nil = match all
	OrderBy  []OrderTerm
	Offset   int
	Limit    int // -1 = no LIMIT
}

// Projection describes what SELECT returns.
type Projection struct {
	Kind      ProjKind
	Fields    []ProjField // ProjFields
	ValuePath []string    // ProjValue (nil for ProjStar/ProjAggregate)
	Aggregate *Aggregate  // ProjAggregate
	// Bare reports a projection written without SELECT VALUE. For an aggregate
	// this wraps the scalar in {"$1": v}; ignored otherwise.
	Bare bool
}

// ProjField is one item of a field projection: a path and its output alias.
type ProjField struct {
	Path  []string
	Alias string
}

// Aggregate is a COUNT/SUM/AVG/MIN/MAX over the matched documents. Path is nil
// for COUNT(1)/COUNT(*).
type Aggregate struct {
	Func string
	Path []string
}

// OrderTerm is one ORDER BY key.
type OrderTerm struct {
	Path []string
	Desc bool
}
