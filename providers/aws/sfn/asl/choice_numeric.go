package asl

import "strings"

func isNumericOp(op string) bool {
	base := strings.TrimSuffix(op, "Path")

	return inList(base, "NumericEquals", "NumericLessThan", "NumericGreaterThan",
		"NumericLessThanEquals", "NumericGreaterThanEquals")
}

// evalNumeric evaluates the numeric comparator family. A non-numeric value or
// comparand is a non-match.
func evalNumeric(op string, val, comparand any) bool {
	a, ok := toFloat(val)
	if !ok {
		return false
	}

	b, ok := toFloat(comparand)
	if !ok {
		return false
	}

	return orderMatch(cmpFloat(a, b), op)
}

// toFloat converts a JSON-decoded numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
