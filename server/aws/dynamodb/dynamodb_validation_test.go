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

// TestDDBPutItemEmptyStringPartitionKey: an empty-string value on the partition
// key is a ValidationException — a String key attribute may not be zero-length.
func TestDDBPutItemEmptyStringPartitionKey(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "pk_empty", "id", "")

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("pk_empty"),
		Item:      map[string]ddbtypes.AttributeValue{"id": sAttr(""), "data": sAttr("x")},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.Contains(t, err.Error(), "cannot contain an empty string value")
	assert.Contains(t, err.Error(), "Key: id")
}

// TestDDBPutItemEmptyStringSortKey: an empty-string value on the sort key is
// rejected the same way as the partition key.
func TestDDBPutItemEmptyStringSortKey(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "sk_empty", "id", "sk")

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("sk_empty"),
		Item:      map[string]ddbtypes.AttributeValue{"id": sAttr("a"), "sk": sAttr("")},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.Contains(t, err.Error(), "cannot contain an empty string value")
	assert.Contains(t, err.Error(), "Key: sk")
}

// TestDDBPutItemExceedsMaxSize: an item larger than the 400 KB ceiling is a
// ValidationException.
func TestDDBPutItemExceedsMaxSize(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "big_item", "id", "")

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("big_item"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   sAttr("k1"),
			"blob": sAttr(strings.Repeat("a", 500*1024)),
		},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.Contains(t, err.Error(), "maximum allowed size")
}

// TestDDBPutItemUnderMaxSizeSucceeds: an item comfortably under 400 KB still
// writes, guarding against an over-eager size check.
func TestDDBPutItemUnderMaxSizeSucceeds(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "ok_item", "id", "")

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("ok_item"),
		Item: map[string]ddbtypes.AttributeValue{
			"id":   sAttr("k1"),
			"blob": sAttr(strings.Repeat("a", 100*1024)),
		},
	})

	require.NoError(t, err)
}

// TestDDBUpdateItemExceedsMaxSize: an UpdateItem whose SET grows the item past
// the 400 KB ceiling is a ValidationException naming the maximum allowed size.
func TestDDBUpdateItemExceedsMaxSize(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "upd_big", "id", "")
	suiteDDBPut(t, client, "upd_big", map[string]ddbtypes.AttributeValue{"id": sAttr("k1")})

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String("upd_big"),
		Key:                       map[string]ddbtypes.AttributeValue{"id": sAttr("k1")},
		UpdateExpression:          aws.String("SET blob = :b"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":b": sAttr(strings.Repeat("a", 500*1024))},
	})

	require.Error(t, err)
	assert.Equal(t, "ValidationException", apiErrorCode(t, err))
	assert.Contains(t, err.Error(), "maximum allowed size")
}

// TestDDBUpdateItemUnderMaxSizeSucceeds: an UpdateItem whose result stays under
// 400 KB is accepted, guarding against an over-eager post-update size check.
func TestDDBUpdateItemUnderMaxSizeSucceeds(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "upd_ok", "id", "")
	suiteDDBPut(t, client, "upd_ok", map[string]ddbtypes.AttributeValue{"id": sAttr("k1")})

	_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String("upd_ok"),
		Key:                       map[string]ddbtypes.AttributeValue{"id": sAttr("k1")},
		UpdateExpression:          aws.String("SET blob = :b"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":b": sAttr(strings.Repeat("a", 100*1024))},
	})

	require.NoError(t, err)
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

// TestDDBCreateTableBillingThroughput: CreateTable's cross-field rule between
// BillingMode and ProvisionedThroughput — PROVISIONED requires a valid (>=1)
// RCU/WCU, PAY_PER_REQUEST forbids either being set, and PAY_PER_REQUEST with
// no throughput at all (the ordinary on-demand case) must NOT be rejected.
func TestDDBCreateTableBillingThroughput(t *testing.T) {
	cases := []struct {
		name        string
		billingMode ddbtypes.BillingMode
		throughput  *ddbtypes.ProvisionedThroughput
		wantErr     bool
	}{
		{
			name:        "provisioned with valid throughput succeeds",
			billingMode: ddbtypes.BillingModeProvisioned,
			throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			wantErr:     false,
		},
		{
			name:        "provisioned with zero throughput rejected",
			billingMode: ddbtypes.BillingModeProvisioned,
			throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(0), WriteCapacityUnits: aws.Int64(0)},
			wantErr:     true,
		},
		{
			name:        "provisioned with no throughput rejected",
			billingMode: ddbtypes.BillingModeProvisioned,
			throughput:  nil,
			wantErr:     true,
		},
		{
			name:        "pay-per-request with no throughput succeeds",
			billingMode: ddbtypes.BillingModePayPerRequest,
			throughput:  nil,
			wantErr:     false,
		},
		{
			name:        "pay-per-request with explicit throughput rejected",
			billingMode: ddbtypes.BillingModePayPerRequest,
			throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			wantErr:     true,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSuiteDDBEnv(t)
			ctx := context.Background()
			table := fmt.Sprintf("billing_ct_%d", i)

			_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
				TableName:   aws.String(table),
				BillingMode: tc.billingMode,
				KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
				AttributeDefinitions: []ddbtypes.AttributeDefinition{
					{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
				},
				ProvisionedThroughput: tc.throughput,
			})

			if tc.wantErr {
				assert.Equal(t, "ValidationException", apiErrorCode(t, err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDDBUpdateTableBillingThroughput: UpdateTable's cross-field rule between
// BillingMode and ProvisionedThroughput, on both directions of a billing-mode
// switch. Each case first creates the table in its starting billing mode, then
// applies the UpdateTable under test.
func TestDDBUpdateTableBillingThroughput(t *testing.T) {
	type step struct {
		billingMode ddbtypes.BillingMode
		throughput  *ddbtypes.ProvisionedThroughput
	}

	cases := []struct {
		name    string
		initial step
		update  step
		wantErr bool
	}{
		{
			name:    "pay-per-request to provisioned with valid throughput succeeds",
			initial: step{billingMode: ddbtypes.BillingModePayPerRequest},
			update: step{
				billingMode: ddbtypes.BillingModeProvisioned,
				throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			},
			wantErr: false,
		},
		{
			name:    "pay-per-request to provisioned with missing throughput rejected",
			initial: step{billingMode: ddbtypes.BillingModePayPerRequest},
			update:  step{billingMode: ddbtypes.BillingModeProvisioned},
			wantErr: true,
		},
		{
			name: "provisioned to pay-per-request succeeds",
			initial: step{
				billingMode: ddbtypes.BillingModeProvisioned,
				throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			},
			update:  step{billingMode: ddbtypes.BillingModePayPerRequest},
			wantErr: false,
		},
		{
			name: "provisioned to pay-per-request with explicit throughput rejected",
			initial: step{
				billingMode: ddbtypes.BillingModeProvisioned,
				throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			},
			update: step{
				billingMode: ddbtypes.BillingModePayPerRequest,
				throughput:  &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			},
			wantErr: true,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := newSuiteDDBEnv(t)
			ctx := context.Background()
			table := fmt.Sprintf("billing_ut_%d", i)

			_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
				TableName:   aws.String(table),
				BillingMode: tc.initial.billingMode,
				KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
				AttributeDefinitions: []ddbtypes.AttributeDefinition{
					{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
				},
				ProvisionedThroughput: tc.initial.throughput,
			})
			require.NoError(t, err)

			_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
				TableName:             aws.String(table),
				BillingMode:           tc.update.billingMode,
				ProvisionedThroughput: tc.update.throughput,
			})

			if tc.wantErr {
				assert.Equal(t, "ValidationException", apiErrorCode(t, err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDDBUpdateTableAddGSIOnProvisionedTableSucceeds: adding a GSI via
// UpdateTable without touching BillingMode/ProvisionedThroughput at all must
// still succeed on an already-valid PROVISIONED table — the billing/throughput
// validation must not fire for an unrelated field.
func TestDDBUpdateTableAddGSIOnProvisionedTableSucceeds(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("gsi_add_prov"),
		BillingMode: ddbtypes.BillingModeProvisioned,
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		ProvisionedThroughput: &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
	})
	require.NoError(t, err)

	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName: aws.String("gsi_add_prov"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("gk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{{
			Create: &ddbtypes.CreateGlobalSecondaryIndexAction{
				IndexName:             aws.String("gsi1"),
				KeySchema:             []ddbtypes.KeySchemaElement{{AttributeName: aws.String("gk"), KeyType: ddbtypes.KeyTypeHash}},
				Projection:            &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
				ProvisionedThroughput: &ddbtypes.ProvisionedThroughput{ReadCapacityUnits: aws.Int64(5), WriteCapacityUnits: aws.Int64(5)},
			},
		}},
	})

	require.NoError(t, err)
}

// TestDDBPutItemGSIKeyTypeValidation: PutItem validates a GSI key attribute's
// type when the item carries it, names the offending index, but does NOT
// require the attribute to be present at all — a sparse GSI (an item that
// omits the GSI key attribute) is a normal, valid write.
func TestDDBPutItemGSIKeyTypeValidation(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("gsi_type"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		KeySchema:   []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("gk"), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName:  aws.String("by-gk"),
			KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("gk"), KeyType: ddbtypes.KeyTypeHash}},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
		}},
	})
	require.NoError(t, err)

	t.Run("correctly typed GSI key succeeds", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("gsi_type"),
			Item:      map[string]ddbtypes.AttributeValue{"pk": sAttr("k1"), "gk": nAttr("42")},
		})
		require.NoError(t, err)
	})

	t.Run("wrong typed GSI key rejected naming the index", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("gsi_type"),
			Item:      map[string]ddbtypes.AttributeValue{"pk": sAttr("k2"), "gk": sAttr("not-a-number")},
		})
		require.Error(t, err)
		assert.Equal(t, "ValidationException", apiErrorCode(t, err))
		assert.Contains(t, err.Error(), "Type mismatch for Index Key gk")
		assert.Contains(t, err.Error(), "IndexName: by-gk")
	})

	t.Run("item omitting the GSI key attribute (sparse GSI) succeeds", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("gsi_type"),
			Item:      map[string]ddbtypes.AttributeValue{"pk": sAttr("k3")},
		})
		require.NoError(t, err)
	})
}
