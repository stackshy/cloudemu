package opensearch

import (
	"encoding/json"
	"strconv"
)

// rawString marshals s as a JSON string literal.
func rawString(s string) json.RawMessage {
	b, _ := json.Marshal(s)

	return b
}

// rawInt marshals n as a JSON number.
func rawInt(n int) json.RawMessage {
	return json.RawMessage(strconv.Itoa(n))
}

// rawFloat marshals f as a JSON number.
func rawFloat(f float64) json.RawMessage {
	return json.RawMessage(strconv.FormatFloat(f, 'f', -1, 64))
}
