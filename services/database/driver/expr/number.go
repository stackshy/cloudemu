package expr

import (
	"encoding/json"
	"strconv"
)

// NumberJSONTag is the single key of the self-describing JSON object a Number
// marshals to, so an exact-decimal N survives a snapshot round-trip through a
// generic map[string]any (a bare JSON number would restore as float64). Persist
// re-types objects carrying this key back into a Number on restore.
const NumberJSONTag = "$ddbN"

// Number is a DynamoDB Number (N) scalar preserved as its exact decimal string.
// DynamoDB numbers are 38-significant-digit decimals transmitted as strings, so
// values beyond float64's 53-bit mantissa (large ids, money, high-precision
// counters) must round-trip through storage without being parsed through
// float64. Keeping the original string makes PutItem/GetItem lossless. Arithmetic
// (SET a = a + :n, ADD) still coerces to float64 — a documented precision limit,
// the same one NumberSet carries — but storage and read-back are exact.
type Number string

// MarshalJSON emits a self-describing object ({"$ddbN":"25"}) so the exact
// decimal survives a persist snapshot's map[string]any JSON round-trip; a bare
// JSON number would be restored as a lossy float64.
func (n Number) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{NumberJSONTag: string(n)})
}

// UnmarshalJSON accepts the self-describing object form as well as a bare
// number/string literal, so a Number decoded into a *Number is lossless.
func (n *Number) UnmarshalJSON(b []byte) error {
	var tagged map[string]string
	if err := json.Unmarshal(b, &tagged); err == nil {
		if v, ok := tagged[NumberJSONTag]; ok {
			*n = Number(v)
			return nil
		}
	}

	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}

	*n = Number(s)

	return nil
}

// Float returns n's numeric value for arithmetic and ordering. A malformed
// number reports ok=false so it stays type-segregated, like any non-number.
func (n Number) Float() (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0, false
	}

	return f, true
}
