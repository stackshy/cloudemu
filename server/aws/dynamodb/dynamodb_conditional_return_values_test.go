// dynamodb_conditional_return_values_test.go — real aws-sdk-go-v2 journeys
// asserting ReturnValuesOnConditionCheckFailure=ALL_OLD returns the conflicting
// item in ConditionalCheckFailedException.Item for PutItem, UpdateItem and
// DeleteItem, and that omitting it leaves Item empty.
package dynamodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDDBPutItemConditionFailureReturnsItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "widgets", "id", "")
	suiteDDBPut(t, client, "widgets", map[string]ddbtypes.AttributeValue{
		"id":    sAttr("w1"),
		"color": sAttr("red"),
	})

	// attribute_not_exists on an existing item fails the condition. With
	// ALL_OLD, DynamoDB returns the current item in the exception's Item member.
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                           aws.String("widgets"),
		Item:                                map[string]ddbtypes.AttributeValue{"id": sAttr("w1"), "color": sAttr("blue")},
		ConditionExpression:                 aws.String("attribute_not_exists(id)"),
		ReturnValuesOnConditionCheckFailure: ddbtypes.ReturnValuesOnConditionCheckFailureAllOld,
	})

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.ErrorAs(t, err, &ccf)
	require.NotEmpty(t, ccf.Item)
	assert.Equal(t, "w1", attrS(t, ccf.Item, "id"))
	assert.Equal(t, "red", attrS(t, ccf.Item, "color"))
}

func TestDDBPutItemConditionFailureNoReturnValuesOmitsItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "widgets", "id", "")
	suiteDDBPut(t, client, "widgets", map[string]ddbtypes.AttributeValue{
		"id":    sAttr("w1"),
		"color": sAttr("red"),
	})

	// Without ReturnValuesOnConditionCheckFailure, Item is absent.
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String("widgets"),
		Item:                map[string]ddbtypes.AttributeValue{"id": sAttr("w1"), "color": sAttr("blue")},
		ConditionExpression: aws.String("attribute_not_exists(id)"),
	})

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.ErrorAs(t, err, &ccf)
	assert.Empty(t, ccf.Item)
}

func TestDDBUpdateItemConditionFailureReturnsItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "widgets", "id", "")
	suiteDDBPut(t, client, "widgets", map[string]ddbtypes.AttributeValue{
		"id":    sAttr("w1"),
		"stock": nAttr("5"),
	})

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String("widgets"),
		Key:                       map[string]ddbtypes.AttributeValue{"id": sAttr("w1")},
		UpdateExpression:          aws.String("SET stock = :n"),
		ConditionExpression:       aws.String("stock = :expected"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":n": nAttr("9"), ":expected": nAttr("1")},

		ReturnValuesOnConditionCheckFailure: ddbtypes.ReturnValuesOnConditionCheckFailureAllOld,
	})

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.ErrorAs(t, err, &ccf)
	require.NotEmpty(t, ccf.Item)
	assert.Equal(t, "w1", attrS(t, ccf.Item, "id"))
	assert.Equal(t, "5", attrN(t, ccf.Item, "stock"))
}

func TestDDBDeleteItemConditionFailureReturnsItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "widgets", "id", "")
	suiteDDBPut(t, client, "widgets", map[string]ddbtypes.AttributeValue{
		"id":    sAttr("w1"),
		"color": sAttr("green"),
	})

	_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName:                 aws.String("widgets"),
		Key:                       map[string]ddbtypes.AttributeValue{"id": sAttr("w1")},
		ConditionExpression:       aws.String("color = :c"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":c": sAttr("purple")},

		ReturnValuesOnConditionCheckFailure: ddbtypes.ReturnValuesOnConditionCheckFailureAllOld,
	})

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.ErrorAs(t, err, &ccf)
	require.NotEmpty(t, ccf.Item)
	assert.Equal(t, "green", attrS(t, ccf.Item, "color"))

	// The item is still present — a failed conditional delete does not remove it.
	got := suiteDDBGet(t, client, "widgets", map[string]ddbtypes.AttributeValue{"id": sAttr("w1")})
	require.NotEmpty(t, got.Item)
	assert.Equal(t, "green", attrS(t, got.Item, "color"))
}

// A conditional failure with ALL_OLD but no existing item leaves Item empty
// (there is nothing to return).
func TestDDBPutItemConditionFailureMissingItemHasNoItem(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "widgets", "id", "")

	// attribute_exists on an absent item fails; there is no current item.
	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                           aws.String("widgets"),
		Item:                                map[string]ddbtypes.AttributeValue{"id": sAttr("ghost")},
		ConditionExpression:                 aws.String("attribute_exists(id)"),
		ReturnValuesOnConditionCheckFailure: ddbtypes.ReturnValuesOnConditionCheckFailureAllOld,
	})

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.True(t, errors.As(err, &ccf))
	assert.Empty(t, ccf.Item)
}
