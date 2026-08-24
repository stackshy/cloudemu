// dynamodb_transaction_test.go — real aws-sdk-go-v2 round-trips proving
// TransactWriteItems honors per-operation ConditionExpression, applies Update
// and ConditionCheck items, and rejects duplicate targets — the divergences
// where the wire handler previously modeled only unconditional Put/Delete.
package dynamodb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apiErrorCode extracts the AWS API error code from an SDK error.
func apiErrorCode(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr, "expected an AWS API error, got %T", err)

	return apiErr.ErrorCode()
}

// TestDDBTransactWriteConditionEnforced: a Put with attribute_not_exists(pk)
// against an existing item must cancel the whole transaction and write nothing.
func TestDDBTransactWriteConditionEnforced(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tw_cond", "pk", "")
	suiteDDBPut(t, client, "tw_cond", map[string]ddbtypes.AttributeValue{
		"pk": sAttr("k1"), "val": sAttr("original"),
	})

	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName:           aws.String("tw_cond"),
				Item:                map[string]ddbtypes.AttributeValue{"pk": sAttr("k1"), "val": sAttr("OVERWRITTEN")},
				ConditionExpression: aws.String("attribute_not_exists(pk)"),
			}},
		},
	})

	assert.Equal(t, "TransactionCanceledException", apiErrorCode(t, err))

	got := suiteDDBGet(t, client, "tw_cond", map[string]ddbtypes.AttributeValue{"pk": sAttr("k1")})
	assert.Equal(t, "original", attrS(t, got.Item, "val"), "the failed transaction must not overwrite the item")
}

// TestDDBTransactWriteUpdateApplied: an Update item mutates the target per its
// UpdateExpression (previously silently dropped).
func TestDDBTransactWriteUpdateApplied(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tw_upd", "pk", "")
	suiteDDBPut(t, client, "tw_upd", map[string]ddbtypes.AttributeValue{"pk": sAttr("u1"), "n": nAttr("1")})

	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Update: &ddbtypes.Update{
				TableName:                 aws.String("tw_upd"),
				Key:                       map[string]ddbtypes.AttributeValue{"pk": sAttr("u1")},
				UpdateExpression:          aws.String("SET n = :v"),
				ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": nAttr("999")},
			}},
		},
	})
	require.NoError(t, err)

	got := suiteDDBGet(t, client, "tw_upd", map[string]ddbtypes.AttributeValue{"pk": sAttr("u1")})
	assert.Equal(t, "999", attrN(t, got.Item, "n"), "the transactional Update must be applied")
}

// TestDDBTransactWriteConditionCheckEnforced: a ConditionCheck whose condition
// is unmet cancels the transaction (and its sibling Put is not applied).
func TestDDBTransactWriteConditionCheckEnforced(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tw_cc", "pk", "")

	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{ConditionCheck: &ddbtypes.ConditionCheck{
				TableName:           aws.String("tw_cc"),
				Key:                 map[string]ddbtypes.AttributeValue{"pk": sAttr("missing")},
				ConditionExpression: aws.String("attribute_exists(pk)"),
			}},
			{Put: &ddbtypes.Put{
				TableName: aws.String("tw_cc"),
				Item:      map[string]ddbtypes.AttributeValue{"pk": sAttr("written")},
			}},
		},
	})

	assert.Equal(t, "TransactionCanceledException", apiErrorCode(t, err))

	got, gerr := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("tw_cc"),
		Key:       map[string]ddbtypes.AttributeValue{"pk": sAttr("written")},
	})
	require.NoError(t, gerr)
	assert.Nil(t, got.Item, "the sibling Put must not be applied when a ConditionCheck fails")
}

// TestDDBTransactWriteDuplicateItem: two operations on the same item in one
// transaction are a ValidationException.
func TestDDBTransactWriteDuplicateItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tw_dup", "pk", "")

	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{TableName: aws.String("tw_dup"), Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("same")}}},
			{Put: &ddbtypes.Put{TableName: aws.String("tw_dup"), Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("same")}}},
		},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}
