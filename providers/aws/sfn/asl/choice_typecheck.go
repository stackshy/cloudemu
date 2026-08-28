package asl

import "encoding/json"

func isTypeCheckOp(op string) bool {
	return inList(op, "IsNull", "IsPresent", "IsNumeric", "IsString", "IsBoolean", "IsTimestamp")
}

// evalTypeCheck evaluates the Is* comparators, whose operand is a boolean: the
// rule matches when the actual type-check result equals the requested boolean.
// These comparators handle an absent variable themselves.
func evalTypeCheck(op string, operand json.RawMessage, val any, present bool) (bool, error) {
	var want bool
	if err := json.Unmarshal(operand, &want); err != nil {
		return false, err
	}

	return typeCheckResult(op, val, present) == want, nil
}

func typeCheckResult(op string, val any, present bool) bool {
	switch op {
	case "IsPresent":
		return present
	case "IsNull":
		return present && val == nil
	case "IsString":
		_, ok := val.(string)

		return ok
	case "IsBoolean":
		_, ok := val.(bool)

		return ok
	case "IsNumeric":
		_, ok := toFloat(val)

		return ok
	case "IsTimestamp":
		s, ok := val.(string)

		return ok && isTimestamp(s)
	default:
		return false
	}
}
