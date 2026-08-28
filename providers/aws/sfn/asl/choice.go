package asl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ChoiceRule is one rule in a Choice state: either a leaf comparator (Variable +
// one operator in ops) or a logical combinator (And/Or/Not). A top-level rule
// also carries Next.
type ChoiceRule struct {
	Variable string
	Next     string
	And      []*ChoiceRule
	Or       []*ChoiceRule
	Not      *ChoiceRule
	ops      map[string]json.RawMessage
}

// reservedRuleKeys are the ChoiceRule fields that are not comparator operators.
func reservedRuleKey(k string) bool {
	return inList(k, "Variable", "Next", "And", "Or", "Not", "Comment")
}

// UnmarshalJSON separates the structural fields from the (single) comparator
// operator, which is kept in ops for family dispatch.
func (r *ChoiceRule) UnmarshalJSON(b []byte) error {
	var known struct {
		Variable string
		Next     string
		And      []*ChoiceRule
		Or       []*ChoiceRule
		Not      *ChoiceRule
	}

	if err := json.Unmarshal(b, &known); err != nil {
		return err
	}

	r.Variable, r.Next, r.And, r.Or, r.Not = known.Variable, known.Next, known.And, known.Or, known.Not

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	r.ops = make(map[string]json.RawMessage)

	for k, v := range raw {
		if !reservedRuleKey(k) {
			r.ops[k] = v
		}
	}

	return nil
}

// choiceHandler runs a Choice state: it evaluates each rule against the
// InputPath-filtered input, transitioning to the first match's Next (or Default),
// and fails with States.NoChoiceMatched when nothing matches and no Default is set.
func choiceHandler(_ context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error) {
	it.enterState(st, raw)

	in, err := it.applyInputPath(st, raw)
	if err != nil {
		return nil, "", false, err
	}

	for _, rule := range st.Choices {
		match, err := it.evalRule(rule, in)
		if err != nil {
			return nil, "", false, err
		}

		if match {
			return it.choiceExit(st, in, rule.Next)
		}
	}

	if st.Default != "" {
		return it.choiceExit(st, in, st.Default)
	}

	return nil, "", false, &stateError{Code: "States.NoChoiceMatched",
		Cause: fmt.Sprintf("no choice rule matched in state %q and no Default was specified", st.name)}
}

func (it *interp) choiceExit(st *State, in any, next string) (out any, nextState string, terminal bool, err error) {
	out, err = it.applyOutputPath(st, in)
	if err != nil {
		return nil, "", false, err
	}

	it.exitState(st, out)

	return out, next, false, nil
}

// evalRule evaluates a choice rule (leaf comparator or And/Or/Not combinator).
func (it *interp) evalRule(rule *ChoiceRule, input any) (bool, error) {
	switch {
	case rule.Not != nil:
		m, err := it.evalRule(rule.Not, input)

		return !m, err
	case len(rule.And) > 0:
		return it.evalAll(rule.And, input)
	case len(rule.Or) > 0:
		return it.evalAny(rule.Or, input)
	}

	var op string

	var operand json.RawMessage

	for k, v := range rule.ops {
		op, operand = k, v
	}

	return it.evalLeaf(rule.Variable, op, operand, input)
}

// evalLeaf resolves the variable and dispatches to the comparator family. An
// absent variable is a non-match for value comparators; type-check comparators
// handle absence themselves.
func (it *interp) evalLeaf(varPath, op string, operand json.RawMessage, input any) (bool, error) {
	val, present, err := it.resolvePath(varPath, input)
	if err != nil {
		return false, err
	}

	if comparatorFamily(op) == famTypeCheck {
		return evalTypeCheck(op, operand, val, present)
	}

	if !present {
		return false, nil
	}

	comparand, err := it.resolveComparand(op, operand, input)
	if err != nil {
		return false, err
	}

	return dispatchComparator(op, val, comparand)
}

func dispatchComparator(op string, val, comparand any) (bool, error) {
	switch comparatorFamily(op) {
	case famString:
		return evalString(op, val, comparand), nil
	case famNumeric:
		return evalNumeric(op, val, comparand), nil
	case famBoolean:
		return evalBoolean(val, comparand), nil
	case famTimestamp:
		return evalTimestamp(op, val, comparand), nil
	default:
		return false, aslErrf("unknown comparator %q", op)
	}
}

// resolveComparand yields the value a comparator compares against: a ...Path
// operator resolves a JSONPath against the input; otherwise the operand is a
// literal JSON value.
func (it *interp) resolveComparand(op string, operand json.RawMessage, input any) (any, error) {
	if strings.HasSuffix(op, "Path") {
		var p string
		if err := json.Unmarshal(operand, &p); err != nil {
			return nil, err
		}

		v, present, err := it.resolvePath(p, input)
		if err != nil {
			return nil, err
		}

		if !present {
			return nil, aslErrf("comparator path %q could not be found", p)
		}

		return v, nil
	}

	return rawToValue(operand)
}

// Comparator families.
const (
	famTypeCheck = "typecheck"
	famString    = "string"
	famNumeric   = "numeric"
	famBoolean   = "boolean"
	famTimestamp = "timestamp"
)

// comparatorFamily classifies an operator, or returns "" for an unknown one.
func comparatorFamily(op string) string {
	switch {
	case isTypeCheckOp(op):
		return famTypeCheck
	case isStringOp(op):
		return famString
	case isNumericOp(op):
		return famNumeric
	case isBooleanOp(op):
		return famBoolean
	case isTimestampOp(op):
		return famTimestamp
	default:
		return ""
	}
}

func inList(s string, list ...string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}

	return false
}

// orderMatch maps a -1/0/1 comparison result to the operator's ordering
// semantics. More specific suffixes are checked before their prefixes.
func orderMatch(cmp int, op string) bool {
	switch {
	case strings.Contains(op, "GreaterThanEquals"):
		return cmp >= 0
	case strings.Contains(op, "LessThanEquals"):
		return cmp <= 0
	case strings.Contains(op, "GreaterThan"):
		return cmp > 0
	case strings.Contains(op, "LessThan"):
		return cmp < 0
	case strings.Contains(op, "Equals"):
		return cmp == 0
	default:
		return false
	}
}
