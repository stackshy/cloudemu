package server

import (
	"fmt"
	"strconv"
)

// fromDynamoDBItem converts a DynamoDB wire-format item to a plain map.
// Wire: {"pk": {"S": "val"}, "age": {"N": "25"}} becomes {"pk": "val", "age": "25"}.
func fromDynamoDBItem(wire map[string]any) map[string]any {
	if wire == nil {
		return nil
	}

	item := make(map[string]any, len(wire))

	for k, v := range wire {
		item[k] = fromAttributeValue(v)
	}

	return item
}

// toDynamoDBItem converts a plain map back to DynamoDB wire format.
func toDynamoDBItem(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}

	wire := make(map[string]any, len(item))

	for k, v := range item {
		wire[k] = toAttributeValue(v)
	}

	return wire
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
		return fromDynamoDBList(l)
	}

	if m, ok := av["M"]; ok {
		return fromDynamoDBMap(m)
	}

	return v
}

func fromDynamoDBList(v any) []any {
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

func fromDynamoDBMap(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}

	return fromDynamoDBItem(m)
}

// toAttributeValue wraps a plain value into DynamoDB wire format.
func toAttributeValue(v any) map[string]any {
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
		return map[string]any{"M": toDynamoDBItem(val)}
	default:
		return map[string]any{"S": fmt.Sprintf("%v", val)}
	}
}

func toListValue(list []any) map[string]any {
	items := make([]any, 0, len(list))

	for _, elem := range list {
		items = append(items, toAttributeValue(elem))
	}

	return map[string]any{"L": items}
}
