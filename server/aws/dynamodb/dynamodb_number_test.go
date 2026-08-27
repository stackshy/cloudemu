// dynamodb_number_test.go — real aws-sdk-go-v2 round-trip proving the DynamoDB
// Number type preserves its exact decimal string. Values beyond float64's
// mantissa must not be corrupted by parsing through float64 on the wire.
package dynamodb_test

import (
	"testing"

	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
)

// TestDDBNumberPrecision: a 30-digit integer round-trips exactly through
// PutItem/GetItem (float64 would round it to 123456789012345680000000000000).
func TestDDBNumberPrecision(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)

	suiteDDBCreateTable(t, client, "nums", "id", "")

	const big = "123456789012345678901234567890"

	suiteDDBPut(t, client, "nums", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("x"),
		"big": nAttr(big),
	})

	got := suiteDDBGet(t, client, "nums", map[string]ddbtypes.AttributeValue{"id": sAttr("x")})
	assert.Equal(t, big, attrN(t, got.Item, "big"), "N must round-trip as its exact decimal string")
}
