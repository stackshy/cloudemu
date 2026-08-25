package dynamodb

import (
	"encoding/base64"
	"strconv"

	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// fromWireItem converts a DynamoDB wire-format item to a plain map.
// Wire: {"pk": {"S": "val"}, "age": {"N": "25"}} becomes {"pk": "val", "age": 25}.
func fromWireItem(wire map[string]any) map[string]any {
	if wire == nil {
		return nil
	}

	item := make(map[string]any, len(wire))

	for k, v := range wire {
		item[k] = fromAttributeValue(v)
	}

	return item
}

// toWireItem converts a plain map back to DynamoDB wire format. It delegates to
// the canonical driver-package encoder so the control-plane responses, the
// Streams data plane, and Lambda stream event delivery all render identical
// AttributeValue JSON.
func toWireItem(item map[string]any) map[string]any {
	return dbdriver.MarshalItem(item)
}

// fromAttributeValue extracts the plain value from a DynamoDB AttributeValue.
func fromAttributeValue(v any) any {
	av, ok := v.(map[string]any)
	if !ok {
		return v
	}

	if s, ok := av["S"]; ok {
		return s
	}

	if n, ok := av["N"]; ok {
		// DynamoDB Number is a decimal transmitted as a string; keep the exact
		// string (expr.Number) so values beyond float64 precision round-trip
		// losslessly. Parsing through float64 here corrupts large ids/counters.
		if s, ok := n.(string); ok {
			return expr.Number(s)
		}

		return n
	}

	if b, ok := av["BOOL"]; ok {
		return b
	}

	if _, ok := av["NULL"]; ok {
		return nil
	}

	if l, ok := av["L"]; ok {
		return fromList(l)
	}

	if m, ok := av["M"]; ok {
		return fromMap(m)
	}

	return fromBinaryOrSet(av, v)
}

// fromBinaryOrSet decodes the binary scalar (B) and the set types (SS/NS/BS),
// which the SDK sends as base64 (B/BS) or numeric strings (NS). An unknown
// shape returns the original value untouched.
func fromBinaryOrSet(av map[string]any, v any) any {
	if b, ok := av["B"]; ok {
		return decodeBinary(b)
	}

	if ss, ok := av["SS"]; ok {
		return decodeStringSet(ss)
	}

	if ns, ok := av["NS"]; ok {
		return decodeNumberSet(ns)
	}

	if bs, ok := av["BS"]; ok {
		return decodeBinarySet(bs)
	}

	return v
}

func decodeBinary(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}

	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return v
	}

	return data
}

func decodeStringSet(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}

	out := make(expr.StringSet, 0, len(list))

	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}

	return out
}

func decodeNumberSet(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}

	out := make(expr.NumberSet, 0, len(list))

	for _, e := range list {
		if s, ok := e.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				out = append(out, f)
			}
		}
	}

	return out
}

func decodeBinarySet(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}

	out := make(expr.BinarySet, 0, len(list))

	for _, e := range list {
		if s, ok := e.(string); ok {
			if data, err := base64.StdEncoding.DecodeString(s); err == nil {
				out = append(out, data)
			}
		}
	}

	return out
}

func fromList(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return nil
	}

	result := make([]any, 0, len(list))

	for _, elem := range list {
		result = append(result, fromAttributeValue(elem))
	}

	return result
}

func fromMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	return fromWireItem(m)
}
