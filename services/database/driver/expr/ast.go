// Package expr parses and evaluates DynamoDB condition and filter
// expressions against native Go items (map[string]any), matching real
// DynamoDB type-aware semantics. It is a pure library: it depends only on
// the standard library and the module's canonical errors package, and is
// shared by the AWS DynamoDB, Azure Cosmos and GCP Firestore providers.
package expr

// Node is a boolean expression AST node produced by ParseCondition and
// evaluated by Eval.
type Node interface {
	isNode()
}

// And is a logical conjunction of two boolean sub-expressions.
type And struct {
	Left  Node
	Right Node
}

// Or is a logical disjunction of two boolean sub-expressions.
type Or struct {
	Left  Node
	Right Node
}

// Not negates a boolean sub-expression.
type Not struct {
	Child Node
}

// Comparison compares two operands with one of = <> < <= > >=.
type Comparison struct {
	Op    string
	Left  Operand
	Right Operand
}

// Between tests lo <= operand <= hi (type-aware, inclusive).
type Between struct {
	Operand Operand
	Lo      Operand
	Hi      Operand
}

// In tests whether Operand equals any member of List.
type In struct {
	Operand Operand
	List    []Operand
}

// AttrExists is attribute_exists(path): true when the path resolves.
type AttrExists struct {
	Path *PathOperand
}

// AttrNotExists is attribute_not_exists(path): true when the path is absent.
type AttrNotExists struct {
	Path *PathOperand
}

// AttrType is attribute_type(path, :t): true when the path value's DynamoDB
// type code equals the (string) type operand.
type AttrType struct {
	Path *PathOperand
	Type Operand
}

// BeginsWith is begins_with(path, operand) on strings or binary.
type BeginsWith struct {
	Path   *PathOperand
	Prefix Operand
}

// Contains is contains(path, operand): substring on strings, membership on
// lists and sets.
type Contains struct {
	Path    *PathOperand
	Operand Operand
}

func (*And) isNode()           {}
func (*Or) isNode()            {}
func (*Not) isNode()           {}
func (*Comparison) isNode()    {}
func (*Between) isNode()       {}
func (*In) isNode()            {}
func (*AttrExists) isNode()    {}
func (*AttrNotExists) isNode() {}
func (*AttrType) isNode()      {}
func (*BeginsWith) isNode()    {}
func (*Contains) isNode()      {}

// Operand is a value-producing leaf: an attribute path, a resolved :value
// placeholder, or a size(path) count.
type Operand interface {
	isOperand()
}

// PathPart is one step of a document path: either a named attribute
// (Name, IsIndex false) or a list index (Index, IsIndex true).
type PathPart struct {
	Name    string
	Index   int
	IsIndex bool
}

// PathOperand is a document path such as a, a.b or a[0].c, with any #alias
// steps already resolved to concrete names.
type PathOperand struct {
	Parts []PathPart
}

// ValueOperand is a :placeholder already resolved to its native value.
type ValueOperand struct {
	Value any
}

// SizeOperand is size(path): the length of the path value as a number,
// usable on either side of a comparison.
type SizeOperand struct {
	Path *PathOperand
}

func (*PathOperand) isOperand()  {}
func (*ValueOperand) isOperand() {}
func (*SizeOperand) isOperand()  {}
