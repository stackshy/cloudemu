package dynamodb

import (
	"encoding/base64"
	"fmt"
	"strconv"

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

// toWireItem converts a plain map back to DynamoDB wire format.
func toWireItem(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}

	w := make(map[string]any, len(item))

	for k, v := range item {
		w[k] = toAttributeValue(v)
	}

	return w
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
		if s, ok := n.(string); ok {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f
			}
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

// toAttributeValue wraps a plain value into DynamoDB wire format.
func toAttributeValue(v any) map[string]any {
	if av, ok := toBinaryOrSetValue(v); ok {
		return av
	}

	switch val := v.(type) {
	case string:
		return map[string]any{"S": val}
	case float64:
		return map[string]any{"N": strconv.FormatFloat(val, 'f', -1, 64)}
	case int:
		return map[string]any{"N": strconv.Itoa(val)}
	case int64:
		return map[string]any{"N": strconv.FormatInt(val, 10)}
	case bool:
		return map[string]any{"BOOL": val}
	case nil:
		return map[string]any{"NULL": true}
	case []any:
		return toListValue(val)
	case map[string]any:
		return map[string]any{"M": toWireItem(val)}
	default:
		return map[string]any{"S": fmt.Sprintf("%v", val)}
	}
}

// toBinaryOrSetValue encodes the binary scalar (B) and the set types (SS/NS/BS),
// keeping toAttributeValue's own type switch within the complexity budget.
func toBinaryOrSetValue(v any) (map[string]any, bool) {
	switch val := v.(type) {
	case []byte:
		return map[string]any{"B": base64.StdEncoding.EncodeToString(val)}, true
	case expr.StringSet:
		return map[string]any{"SS": []string(val)}, true
	case expr.NumberSet:
		return map[string]any{"NS": formatNumberSet(val)}, true
	case expr.BinarySet:
		return map[string]any{"BS": formatBinarySet(val)}, true
	default:
		return nil, false
	}
}

func formatNumberSet(ns expr.NumberSet) []string {
	out := make([]string, 0, len(ns))
	for _, f := range ns {
		out = append(out, strconv.FormatFloat(f, 'f', -1, 64))
	}

	return out
}

func formatBinarySet(bs expr.BinarySet) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, base64.StdEncoding.EncodeToString(b))
	}

	return out
}

func toListValue(list []any) map[string]any {
	items := make([]any, 0, len(list))

	for _, elem := range list {
		items = append(items, toAttributeValue(elem))
	}

	return map[string]any{"L": items}
}
