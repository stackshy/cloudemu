// dynamodb_validation_test.go — real aws-sdk-go-v2 round-trips proving the
// request-shape and schema validations DynamoDB enforces: CreateTable key/attr
// consistency, PutItem key presence and type, BatchWriteItem/BatchGetItem size
// caps and duplicate keys, and UpdateTable add-GSI attribute-definition checks.
package dynamodb_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDDBCreateTableKeyNotInAttributeDefs: a KeySchema attribute absent from
// AttributeDefinitions is a ValidationException.
func TestDDBCreateTableKeyNotInAttributeDefs(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("bad1"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("somethingElse"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}

// TestDDBCreateTableUnusedAttributeDef: an AttributeDefinition used by no key is
// a ValidationException.
func TestDDBCreateTableUnusedAttributeDef(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("bad2"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("unused"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}

// TestDDBPutItemMissingKey: an item that omits the partition key is rejected.
func TestDDBPutItemMissingKey(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "pk_miss", "id", "")

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("pk_miss"),
		Item:      map[string]ddbtypes.AttributeValue{"data": sAttr("x")},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}

// TestDDBPutItemKeyTypeMismatch: a key attribute whose type differs from the
// schema is rejected.
func TestDDBPutItemKeyTypeMismatch(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "pk_type", "id", "") // id declared as S

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("pk_type"),
		Item:      map[string]ddbtypes.AttributeValue{"id": nAttr("123")},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.Contains(t, err.Error(), "Type mismatch for key id")
}

// TestDDBBatchWriteOver25: more than 25 requests in one BatchWriteItem is a
// ValidationException.
func TestDDBBatchWriteOver25(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "bw_cap", "pk", "")

	reqs := make([]ddbtypes.WriteRequest, 0, 30)
	for i := range 30 {
		reqs = append(reqs, ddbtypes.WriteRequest{
			PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{"pk": sAttr(fmt.Sprintf("k%d", i))}},
		})
	}

	_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{"bw_cap": reqs},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}

// TestDDBBatchWriteDuplicateKeys: two operations on the same key in one
// BatchWriteItem is a ValidationException.
func TestDDBBatchWriteDuplicateKeys(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "bw_dup", "pk", "")

	_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{
			"bw_dup": {
				{PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("dup")}}},
				{PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("dup")}}},
			},
		},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.True(t, strings.Contains(err.Error(), "duplicates"), "message should name the duplicate cause: %v", err)
}

// TestDDBBatchGetOver100: more than 100 keys in one BatchGetItem is a
// ValidationException.
func TestDDBBatchGetOver100(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "bg_cap", "pk", "")

	keys := make([]map[string]ddbtypes.AttributeValue, 0, 101)
	for i := range 101 {
		keys = append(keys, map[string]ddbtypes.AttributeValue{"pk": sAttr(fmt.Sprintf("k%d", i))})
	}

	_, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{"bg_cap": {Keys: keys}},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}

// TestDDBUpdateTableAddGSIWithoutAttrDef: creating a GSI via UpdateTable whose
// key attribute is not supplied in the request AttributeDefinitions is a
// ValidationException.
func TestDDBUpdateTableAddGSIWithoutAttrDef(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "gsi_missing", "id", "")

	_, err := client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName: aws.String("gsi_missing"),
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{{
			Create: &ddbtypes.CreateGlobalSecondaryIndexAction{
				IndexName:  aws.String("gsi1"),
				KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("gsikey"), KeyType: ddbtypes.KeyTypeHash}},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
			},
		}},
	})

	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
}
