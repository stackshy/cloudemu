package asl

func isBooleanOp(op string) bool {
	return op == "BooleanEquals" || op == "BooleanEqualsPath"
}

// evalBoolean evaluates BooleanEquals/BooleanEqualsPath. A non-boolean value or
// comparand is a non-match.
func evalBoolean(val, comparand any) bool {
	a, ok := val.(bool)
	if !ok {
		return false
	}

	b, ok := comparand.(bool)
	if !ok {
		return false
	}

	return a == b
}
