// dynamodb_scan_select_key_idempotency_test.go — real-user-journey tests
// driving the genuine aws-sdk-go-v2 DynamoDB client against the emulator's
// HTTP server for four correctness fixes: parallel-scan segmentation, the
// Select projection mode, numeric-key identity normalization, and
// TransactWriteItems ClientRequestToken idempotency / the 100-op limit.
package dynamodb_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createNumberKeyTable creates a table whose HASH key is a Number (N) attribute,
// used to prove numeric key identity normalization.
func createNumberKeyTable(t *testing.T, client *dynamodb.Client, table, pk string) {
	t.Helper()

	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String(pk), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String(pk), AttributeType: ddbtypes.ScalarAttributeTypeN},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err, "CreateTable %q", table)
}

// TestSuiteDDBParallelScanDisjoint (B1) proves a parallel scan partitions the
// table: the union of all TotalSegments segments covers every item exactly once
// — no duplicate, no skip — where the old whole-table-per-segment behavior would
// have returned every item in every segment.
func TestSuiteDDBParallelScanDisjoint(t *testing.T) {
	t.Parallel()

	const (
		table         = "pscan"
		itemCount     = 10
		totalSegments = 4
	)

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "pk", "")

	for i := 0; i < itemCount; i++ {
		suiteDDBPut(t, client, table, map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: fmt.Sprintf("item-%d", i)},
		})
	}

	seen := map[string]int{}

	for seg := int32(0); seg < totalSegments; seg++ {
		out, err := client.Scan(context.Background(), &dynamodb.ScanInput{
			TableName:     aws.String(table),
			Segment:       aws.Int32(seg),
			TotalSegments: aws.Int32(totalSegments),
		})
		require.NoError(t, err, "Scan segment %d", seg)

		for _, item := range out.Items {
			pk := item["pk"].(*ddbtypes.AttributeValueMemberS).Value
			seen[pk]++
		}
	}

	require.Len(t, seen, itemCount, "union of segments must cover every item")
	for pk, n := range seen {
		assert.Equalf(t, 1, n, "item %q appeared in %d segments (want exactly one)", pk, n)
	}
}

// TestSuiteDDBParallelScanValidation (B1) rejects a malformed parallel-scan
// request (Segment without TotalSegments, and Segment >= TotalSegments) with a
// ValidationException.
func TestSuiteDDBParallelScanValidation(t *testing.T) {
	t.Parallel()

	const table = "pscanval"

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "pk", "")

	_, err := client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(table),
		Segment:   aws.Int32(0),
	})
	assertValidationException(t, err, "Segment without TotalSegments")

	_, err = client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName:     aws.String(table),
		Segment:       aws.Int32(4),
		TotalSegments: aws.Int32(4),
	})
	assertValidationException(t, err, "Segment >= TotalSegments")
}

// TestSuiteDDBSelectCountEmptyItems (B2) proves Select=COUNT returns the counts
// only, with an empty Items array, on both Scan and Query — where the old
// behavior returned the full items.
func TestSuiteDDBSelectCountEmptyItems(t *testing.T) {
	t.Parallel()

	const (
		table     = "selcount"
		itemCount = 3
	)

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "pk", "sk")

	for i := 0; i < itemCount; i++ {
		suiteDDBPut(t, client, table, map[string]ddbtypes.AttributeValue{
			"pk": &ddbtypes.AttributeValueMemberS{Value: "p"},
			"sk": &ddbtypes.AttributeValueMemberS{Value: fmt.Sprintf("s-%d", i)},
		})
	}

	scanOut, err := client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(table),
		Select:    ddbtypes.SelectCount,
	})
	require.NoError(t, err, "Scan COUNT")
	assert.Empty(t, scanOut.Items, "Scan COUNT must return no items")
	assert.Equal(t, int32(itemCount), scanOut.Count, "Scan COUNT Count")

	queryOut, err := client.Query(context.Background(), &dynamodb.QueryInput{
		TableName:                 aws.String(table),
		KeyConditionExpression:    aws.String("pk = :p"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":p": &ddbtypes.AttributeValueMemberS{Value: "p"}},
		Select:                    ddbtypes.SelectCount,
	})
	require.NoError(t, err, "Query COUNT")
	assert.Empty(t, queryOut.Items, "Query COUNT must return no items")
	assert.Equal(t, int32(itemCount), queryOut.Count, "Query COUNT Count")
}

// TestSuiteDDBSelectProjectionConflict (B2) rejects the Select vs
// ProjectionExpression conflicts DynamoDB forbids.
func TestSuiteDDBSelectProjectionConflict(t *testing.T) {
	t.Parallel()

	const table = "selconflict"

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "pk", "")

	// ALL_ATTRIBUTES with a ProjectionExpression is invalid.
	_, err := client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName:                aws.String(table),
		Select:                   ddbtypes.SelectAllAttributes,
		ProjectionExpression:     aws.String("#a"),
		ExpressionAttributeNames: map[string]string{"#a": "pk"},
	})
	assertValidationException(t, err, "ALL_ATTRIBUTES with ProjectionExpression")

	// ALL_PROJECTED_ATTRIBUTES is only valid on an index query/scan.
	_, err = client.Scan(context.Background(), &dynamodb.ScanInput{
		TableName: aws.String(table),
		Select:    ddbtypes.SelectAllProjectedAttributes,
	})
	assertValidationException(t, err, "ALL_PROJECTED_ATTRIBUTES off an index")
}

// TestSuiteDDBNumericKeyIdentity (B3) proves a Number key compares numerically:
// putting "100" then "100.0" writes ONE item (the second overwrites the first),
// and GetItem "100" resolves that canonical item — where the old raw-string key
// stored two distinct items.
func TestSuiteDDBNumericKeyIdentity(t *testing.T) {
	t.Parallel()

	const table = "numkey"

	client, _ := newSuiteDDBEnv(t)
	createNumberKeyTable(t, client, table, "id")

	put := func(id, val string) {
		suiteDDBPut(t, client, table, map[string]ddbtypes.AttributeValue{
			"id":  &ddbtypes.AttributeValueMemberN{Value: id},
			"val": &ddbtypes.AttributeValueMemberS{Value: val},
		})
	}

	put("100", "first")
	put("100.0", "second")

	got := suiteDDBGet(t, client, table, map[string]ddbtypes.AttributeValue{
		"id": &ddbtypes.AttributeValueMemberN{Value: "100"},
	})
	require.NotNil(t, got.Item, "GetItem 100 must resolve the canonical item")
	assert.Equal(t, "second", got.Item["val"].(*ddbtypes.AttributeValueMemberS).Value,
		"the second write must have overwritten the first")

	// "1e2" is the same number, so it must resolve the same single item.
	gotSci := suiteDDBGet(t, client, table, map[string]ddbtypes.AttributeValue{
		"id": &ddbtypes.AttributeValueMemberN{Value: "1e2"},
	})
	require.NotNil(t, gotSci.Item, "GetItem 1e2 must resolve the same canonical item")

	scanOut, err := client.Scan(context.Background(), &dynamodb.ScanInput{TableName: aws.String(table)})
	require.NoError(t, err, "Scan")
	assert.Equal(t, int32(1), scanOut.Count, "table must hold exactly one item")
}

// TestSuiteDDBTransactIdempotencyToken (B4) proves TransactWriteItems is
// idempotent under a reused ClientRequestToken: an ADD counter transaction
// replayed three times with the same token increments the counter ONCE — where
// the old behavior re-applied every replay (counter = 3).
func TestSuiteDDBTransactIdempotencyToken(t *testing.T) {
	t.Parallel()

	const (
		table = "txidem"
		token = "fixed-client-request-token"
	)

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "id", "")

	addOne := func(clientToken string, delta string) error {
		_, err := client.TransactWriteItems(context.Background(), &dynamodb.TransactWriteItemsInput{
			ClientRequestToken: aws.String(clientToken),
			TransactItems: []ddbtypes.TransactWriteItem{{
				Update: &ddbtypes.Update{
					TableName:                 aws.String(table),
					Key:                       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "counter"}},
					UpdateExpression:          aws.String("ADD cnt :d"),
					ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":d": &ddbtypes.AttributeValueMemberN{Value: delta}},
				},
			}},
		})

		return err
	}

	for i := 0; i < 3; i++ {
		require.NoErrorf(t, addOne(token, "1"), "replay %d", i)
	}

	got := suiteDDBGet(t, client, table, map[string]ddbtypes.AttributeValue{
		"id": &ddbtypes.AttributeValueMemberS{Value: "counter"},
	})
	require.NotNil(t, got.Item, "counter item must exist")
	assert.Equal(t, "1", got.Item["cnt"].(*ddbtypes.AttributeValueMemberN).Value,
		"three token-idempotent replays must increment the counter exactly once")

	// The same token with a DIFFERENT request body is an
	// IdempotentParameterMismatchException.
	err := addOne(token, "5")
	require.Error(t, err, "same token, different body")

	var apiErr smithy.APIError
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, "IdempotentParameterMismatchException", apiErr.ErrorCode())
}

// TestSuiteDDBTransactHundredLimit (B5) rejects a transaction of more than 100
// operations with a ValidationException, before anything is applied.
func TestSuiteDDBTransactHundredLimit(t *testing.T) {
	t.Parallel()

	const table = "txlimit"

	client, _ := newSuiteDDBEnv(t)
	suiteDDBCreateTable(t, client, table, "id", "")

	items := make([]ddbtypes.TransactWriteItem, 0, 101)
	for i := 0; i < 101; i++ {
		items = append(items, ddbtypes.TransactWriteItem{
			Put: &ddbtypes.Put{
				TableName: aws.String(table),
				Item:      map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: fmt.Sprintf("k-%d", i)}},
			},
		})
	}

	_, err := client.TransactWriteItems(context.Background(), &dynamodb.TransactWriteItemsInput{TransactItems: items})
	assertValidationException(t, err, "101-op transaction")

	// Nothing was applied.
	scanOut, serr := client.Scan(context.Background(), &dynamodb.ScanInput{TableName: aws.String(table)})
	require.NoError(t, serr, "Scan")
	assert.Equal(t, int32(0), scanOut.Count, "an over-limit transaction must write nothing")
}

// assertValidationException asserts err is a DynamoDB ValidationException.
func assertValidationException(t *testing.T, err error, context string) {
	t.Helper()
	require.Errorf(t, err, "%s must be rejected", context)

	var apiErr smithy.APIError
	require.Truef(t, errors.As(err, &apiErr), "%s: expected an API error, got %v", context, err)
	assert.Equalf(t, "ValidationException", apiErr.ErrorCode(), "%s", context)
}
