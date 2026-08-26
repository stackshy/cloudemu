package expr

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
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

// rat parses n into an exact rational so two decimals compare by their true
// numeric value without ever passing through float64 — the difference between
// two 30-digit ids that share the same float64 must survive. A leading '+' and
// scientific notation are accepted; a malformed number reports ok=false.
func (n Number) rat() (*big.Rat, bool) {
	r, ok := new(big.Rat).SetString(strings.TrimPrefix(string(n), "+"))
	return r, ok
}

// Canonical returns a stable representation of n's numeric value, collapsing
// distinct decimal spellings of the same number ("100", "100.0", "1e2") to one
// string via the exact rational form. A malformed number falls back to its raw
// text so it stays distinct and type-segregated.
func (n Number) Canonical() string {
	if r, ok := n.rat(); ok {
		return r.RatString()
	}

	return string(n)
}

// CanonicalKey renders a primary-key attribute value into a stable identity
// component. A Number collapses to its canonical numeric form so "100" and
// "100.0" resolve to one item key; every other type formats by value exactly as
// before, leaving String/Binary keys byte-for-byte unaffected.
func CanonicalKey(v any) string {
	if n, ok := v.(Number); ok {
		return n.Canonical()
	}

	return fmt.Sprintf("%v", v)
}

// numberEqual reports whether a and b denote the same number. Equality is
// numeric and exact (via big.Rat), so "1" == "1.0" == "+1" yet two values that
// only differ beyond float64 precision stay distinct. Malformed operands fall
// back to an exact string match so they stay type-segregated.
func numberEqual(a, b Number) bool {
	ra, aok := a.rat()
	rb, bok := b.rat()

	if aok && bok {
		return ra.Cmp(rb) == 0
	}

	return string(a) == string(b)
}

// toNumber coerces the numeric kinds the drivers produce to an exact-decimal
// Number for set membership. A Number is kept verbatim; a float/int is
// formatted losslessly enough for membership. Non-numeric values report false.
func toNumber(v any) (Number, bool) {
	switch t := v.(type) {
	case Number:
		return t, true
	case float64:
		return Number(strconv.FormatFloat(t, 'f', -1, 64)), true
	case int:
		return Number(strconv.Itoa(t)), true
	case int64:
		return Number(strconv.FormatInt(t, 10)), true
	}

	return "", false
}
