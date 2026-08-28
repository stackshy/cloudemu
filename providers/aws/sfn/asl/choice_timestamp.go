package asl

import (
	"strings"
	"time"
)

func isTimestampOp(op string) bool {
	base := strings.TrimSuffix(op, "Path")

	return inList(base, "TimestampEquals", "TimestampLessThan", "TimestampGreaterThan",
		"TimestampLessThanEquals", "TimestampGreaterThanEquals")
}

// evalTimestamp evaluates the timestamp comparator family, parsing both operands
// as RFC 3339. A value that is not a valid timestamp string is a non-match.
func evalTimestamp(op string, val, comparand any) bool {
	a, ok := toTime(val)
	if !ok {
		return false
	}

	b, ok := toTime(comparand)
	if !ok {
		return false
	}

	return orderMatch(cmpTime(a, b), op)
}

func toTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}

	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}

	return t, true
}

func isTimestamp(s string) bool {
	_, err := time.Parse(time.RFC3339, s)

	return err == nil
}

func cmpTime(a, b time.Time) int {
	switch {
	case a.Before(b):
		return -1
	case a.After(b):
		return 1
	default:
		return 0
	}
}
