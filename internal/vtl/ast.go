package vtl

// Template nodes. A template body is a []node rendered in order.
type node any

// textNode is literal output, kept as the source pieces it was parsed from so
// building it stays linear.
type textNode struct{ parts []string }

// refNode prints a reference. quiet is the $!x form.
type refNode struct {
	ref   *refExpr
	quiet bool
}

// setNode is #set($target = value).
type setNode struct {
	target *refExpr
	value  expr
}

// ifNode is #if/#elseif/#else/#end. elseBody is nil when there is no #else.
type ifNode struct {
	branches []ifBranch
	elseBody []node
}

type ifBranch struct {
	cond expr
	body []node
}

// foreachNode is #foreach($varName in iter) body #end.
type foreachNode struct {
	varName string
	iter    expr
	body    []node
}

type (
	breakNode struct{}
	stopNode  struct{}
	// returnNode is #return or #return(value).
	returnNode struct{ value expr }
)

// Expressions.
type expr any

type literal struct{ value any }

// interpolated is a double-quoted string literal, rendered as a template.
type interpolated struct{ body []node }

type listExpr struct{ items []expr }

type rangeExpr struct{ from, to expr }

type mapExpr struct {
	keys []expr
	vals []expr
}

// refExpr is $name followed by a chain of property, method and index steps.
type refExpr struct {
	name  string
	chain []accessor
}

type accessorKind int

const (
	accProperty accessorKind = iota
	accMethod
	accIndex
)

type accessor struct {
	kind  accessorKind
	name  string
	args  []expr
	index expr
}

type unaryExpr struct {
	op string
	x  expr
}

type binaryExpr struct {
	op   string
	l, r expr
}

// Operators, as stored in unaryExpr and binaryExpr.
const (
	opAnd = "&&"
	opOr  = "||"
	opEq  = "=="
	opNe  = "!="
	opLt  = "<"
	opGt  = ">"
	opLe  = "<="
	opGe  = ">="
	opAdd = "+"
	opSub = "-"
	opMul = "*"
	opDiv = "/"
	opMod = "%"
	opNot = "!"
)
