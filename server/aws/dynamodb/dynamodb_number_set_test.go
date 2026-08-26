// dynamodb_number_set_test.go — real aws-sdk-go-v2 round-trips proving the
// DynamoDB Number Set (NS) preserves each element's exact decimal string.
// Elements beyond float64's mantissa (large ids, high-precision decimals) must
// not be corrupted by parsing through float64, and set operations (ADD/DELETE)
// must act by numeric value while keeping the survivors' exact strings.
package dynamodb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nsAttr(vals ...string) ddbtypes.AttributeValue {
	return &ddbtypes.AttributeValueMemberNS{Value: vals}
}

func attrNS(t *testing.T, item map[string]ddbtypes.AttributeValue, field string) []string {
	t.Helper()

	v, ok := item[field].(*ddbtypes.AttributeValueMemberNS)
	require.True(t, ok, "attribute %q should be NS, got %T", field, item[field])

	return v.Value
}

func suiteDDBUpdate(t *testing.T, client *dynamodb.Client, table, expr string,
	key, vals map[string]ddbtypes.AttributeValue) {
	t.Helper()

	_, err := client.UpdateItem(context.Background(), &dynamodb.UpdateItemInput{
		TableName:                 aws.String(table),
		Key:                       key,
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeValues: vals,
	})
	require.NoError(t, err, "UpdateItem %q on %q", expr, table)
}

// TestDDBNumberSetPrecision: an NS carrying two 30-digit integers that map to the
// SAME float64 round-trips both exact strings — float parsing would corrupt them
// and collapse them into one element.
func TestDDBNumberSetPrecision(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	const (
		bigA = "123456789012345678901234567890"
		bigB = "123456789012345678901234567891"
	)

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"ids": nsAttr(bigA, bigB),
	})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.ElementsMatch(t, []string{bigA, bigB}, attrNS(t, got.Item, "ids"),
		"NS elements beyond 2^53 must round-trip as exact strings and stay distinct")
}

// TestDDBNumberSetDecimalPrecision: an NS decimal element with far more than 15
// significant digits round-trips without float rounding.
func TestDDBNumberSetDecimalPrecision(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	const pi = "3.141592653589793238462643383279"

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":   sAttr("x"),
		"vals": nsAttr(pi, "-0.5"),
	})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.ElementsMatch(t, []string{pi, "-0.5"}, attrNS(t, got.Item, "vals"),
		"high-precision decimal NS elements must round-trip exactly")
}

// TestDDBNumberSetAddUnion: ADD unions by numeric value and keeps the exact
// string of a large surviving element; a numerically-equal add is a no-op.
func TestDDBNumberSetAddUnion(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	const big = "999999999999999999999999999999"

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"ids": nsAttr("1", big),
	})

	suiteDDBUpdate(t, client, "nums", "ADD ids :n",
		map[string]ddbtypes.AttributeValue{"id": sAttr("x")},
		map[string]ddbtypes.AttributeValue{":n": nsAttr("1.0", "42")})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.ElementsMatch(t, []string{"1", big, "42"}, attrNS(t, got.Item, "ids"),
		"ADD unions by numeric value; large element keeps its exact string, 1.0 dedups against 1")
}

// TestDDBNumberSetDelete: DELETE removes by numeric value and preserves the
// exact strings of the survivors.
func TestDDBNumberSetDelete(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	const big = "123456789012345678901234567890"

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"ids": nsAttr("1", "2", big),
	})

	suiteDDBUpdate(t, client, "nums", "DELETE ids :n",
		map[string]ddbtypes.AttributeValue{"id": sAttr("x")},
		map[string]ddbtypes.AttributeValue{":n": nsAttr("2")})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.ElementsMatch(t, []string{"1", big}, attrNS(t, got.Item, "ids"),
		"DELETE removes by numeric value, survivors keep exact strings")
}

// TestDDBNumberSetDuplicateDedup: numerically-equal elements in one NS collapse
// to a single element (DynamoDB set semantics), keeping the first-seen string.
func TestDDBNumberSetDuplicateDedup(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"ids": nsAttr("1", "1.0", "2"),
	})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.ElementsMatch(t, []string{"1", "2"}, attrNS(t, got.Item, "ids"),
		"numerically-equal NS elements dedup to one")
}

// TestDDBScalarNumberStillRoundTrips: regression guard that the scalar N path
// (which NS mirrors) still preserves an exact high-precision decimal.
func TestDDBScalarNumberStillRoundTrips(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, "nums", "id", "")

	const dec = "9.999999999999999999999999999999"

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"big": nAttr(dec),
	})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.Equal(t, dec, attrN(t, got.Item, "big"), "scalar N must still round-trip exactly")
}
