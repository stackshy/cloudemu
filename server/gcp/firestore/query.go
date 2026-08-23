package firestore

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// buildFilterNode translates a Firestore StructuredQuery where clause into an
// expr AST, reusing the shared type-aware evaluator. A nil filter (or an empty
// composite) yields a nil node, meaning "match every document".
func buildFilterNode(f *queryFilter) (expr.Node, error) {
	if f == nil {
		return nil, nil
	}

	switch {
	case f.FieldFilter != nil:
		return fieldFilterNode(f.FieldFilter)
	case f.CompositeFilter != nil:
		return compositeFilterNode(f.CompositeFilter)
	case f.UnaryFilter != nil:
		return unaryFilterNode(f.UnaryFilter)
	default:
		return nil, nil
	}
}

// firestoreCompareOps maps the relational Firestore operators onto expr
// comparison operators.
//
//nolint:gochecknoglobals // static lookup table
var firestoreCompareOps = map[fieldOp]string{
	"EQUAL":                 "=",
	"NOT_EQUAL":             "<>",
	"LESS_THAN":             "<",
	"LESS_THAN_OR_EQUAL":    "<=",
	"GREATER_THAN":          ">",
	"GREATER_THAN_OR_EQUAL": ">=",
}

func fieldFilterNode(ff *fieldFilter) (expr.Node, error) {
	path := fieldPathToOperand(ff.Field.FieldPath)

	if op, ok := firestoreCompareOps[ff.Op]; ok {
		return &expr.Comparison{Op: op, Left: path, Right: valueOperand(ff.Value)}, nil
	}

	if ff.Op == "ARRAY_CONTAINS" {
		return arrayContainsNode(path, &expr.Contains{Path: path, Operand: valueOperand(ff.Value)}), nil
	}

	return listFilterNode(ff)
}

// listFilterNode handles the array-operand operators IN, NOT_IN and
// ARRAY_CONTAINS_ANY, whose value is a (non-empty) array.
func listFilterNode(ff *fieldFilter) (expr.Node, error) {
	path := fieldPathToOperand(ff.Field.FieldPath)

	members, err := arrayMembers(ff.Value)
	if err != nil {
		return nil, err
	}

	switch ff.Op {
	case "IN":
		return inNode(path, members), nil
	case "NOT_IN":
		return notInNode(path, members), nil
	case "ARRAY_CONTAINS_ANY":
		return arrayContainsNode(path, orContains(path, members)), nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported filter op %q", ff.Op)
	}
}

// notInNode matches documents whose field is present, non-null, and not equal
// to any member — Firestore excludes absent and null fields from not-in (a bare
// negation would wrongly include them).
func notInNode(path *expr.PathOperand, members []any) expr.Node {
	presentAndNotNull := &expr.Comparison{Op: "<>", Left: path, Right: &expr.ValueOperand{Value: nil}}
	return &expr.And{Left: presentAndNotNull, Right: &expr.Not{Child: inNode(path, members)}}
}

// arrayContainsNode gates an array-membership test on the field actually being
// an array, so it never falls back to the substring match that the shared
// Contains node performs on string fields.
func arrayContainsNode(path *expr.PathOperand, membership expr.Node) expr.Node {
	return &expr.And{Left: isArray(path), Right: membership}
}

// isArray is true when path resolves to a list ("L" is the type code
// expr.dynamoType reports for a native []any).
func isArray(path *expr.PathOperand) expr.Node {
	return &expr.AttrType{Path: path, Type: &expr.ValueOperand{Value: "L"}}
}

func compositeFilterNode(cf *compositeFilter) (expr.Node, error) {
	if cf.Op != compositeAnd && cf.Op != compositeOr {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported composite op %q", cf.Op)
	}

	var node expr.Node

	for i := range cf.Filters {
		child, err := buildFilterNode(&cf.Filters[i])
		if err != nil {
			return nil, err
		}

		if child == nil {
			// A match-all sub-filter absorbs an OR (always true) but is a no-op
			// in an AND.
			if cf.Op == compositeOr {
				return nil, nil
			}

			continue
		}

		if node == nil {
			node = child
			continue
		}

		node = combine(cf.Op, node, child)
	}

	return node, nil
}

func combine(op compositeOp, left, right expr.Node) expr.Node {
	if op == compositeOr {
		return &expr.Or{Left: left, Right: right}
	}

	return &expr.And{Left: left, Right: right}
}

func unaryFilterNode(uf *unaryFilter) (expr.Node, error) {
	path := fieldPathToOperand(uf.Field.FieldPath)

	switch uf.Op {
	case "IS_NULL":
		return &expr.Comparison{Op: "=", Left: path, Right: &expr.ValueOperand{Value: nil}}, nil
	case "IS_NOT_NULL":
		return &expr.Comparison{Op: "<>", Left: path, Right: &expr.ValueOperand{Value: nil}}, nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported unary op %q (IS_NAN/IS_NOT_NAN are not modeled)", uf.Op)
	}
}

func inNode(path *expr.PathOperand, members []any) expr.Node {
	list := make([]expr.Operand, len(members))
	for i, m := range members {
		list[i] = &expr.ValueOperand{Value: m}
	}

	return &expr.In{Operand: path, List: list}
}

// orContains builds an OR of ARRAY_CONTAINS over each member, for
// ARRAY_CONTAINS_ANY. members is non-empty (validated by arrayMembers).
func orContains(path *expr.PathOperand, members []any) expr.Node {
	var node expr.Node

	for _, m := range members {
		c := &expr.Contains{Path: path, Operand: &expr.ValueOperand{Value: m}}
		if node == nil {
			node = c
			continue
		}

		node = &expr.Or{Left: node, Right: c}
	}

	return node
}

// arrayMembers returns the native values of an array-valued query operand,
// rejecting a non-array or empty array (Firestore requires a non-empty array
// for IN / NOT_IN / ARRAY_CONTAINS_ANY).
//
//nolint:gocritic // hugeParam: value is the wire field type
func arrayMembers(v value) ([]any, error) {
	if v.ArrayValue == nil || len(v.ArrayValue.Values) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"IN / NOT_IN / ARRAY_CONTAINS_ANY require a non-empty array value")
	}

	out := make([]any, len(v.ArrayValue.Values))
	for i, el := range v.ArrayValue.Values {
		out[i] = firestoreValueToGo(el)
	}

	return out, nil
}

func fieldPathToOperand(path string) *expr.PathOperand {
	segments := strings.Split(path, ".")

	parts := make([]expr.PathPart, 0, len(segments))
	for _, name := range segments {
		parts = append(parts, expr.PathPart{Name: name})
	}

	return &expr.PathOperand{Parts: parts}
}

//nolint:gocritic // hugeParam: value is the wire field type
func valueOperand(v value) *expr.ValueOperand {
	return &expr.ValueOperand{Value: firestoreValueToGo(v)}
}
