package expr

import "strconv"

// Number is a DynamoDB Number (N) scalar preserved as its exact decimal string.
// DynamoDB numbers are 38-significant-digit decimals transmitted as strings, so
// values beyond float64's 53-bit mantissa (large ids, money, high-precision
// counters) must round-trip through storage without being parsed through
// float64. Keeping the original string makes PutItem/GetItem lossless. Arithmetic
// (SET a = a + :n, ADD) still coerces to float64 — a documented precision limit,
// the same one NumberSet carries — but storage and read-back are exact.
type Number string

// Float returns n's numeric value for arithmetic and ordering. A malformed
// number reports ok=false so it stays type-segregated, like any non-number.
func (n Number) Float() (float64, bool) {
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return 0, false
	}

	return f, true
}
