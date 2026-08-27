// index_projection_sdk_roundtrip_test.go —
// Real-user-journey tests driving the genuine aws-sdk-go-v2 DynamoDB client
// against the emulator for secondary-index attribute projection (GSI/LSI
// KEYS_ONLY / INCLUDE / ALL) and per-table BatchGetItem ProjectionExpression.
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

// createGSITable creates a table id(HASH)+sk(RANGE) with a GSI "by-cat" on
// cat(HASH) using the given projection type and non-key attributes.
func createGSITable(
	t *testing.T, client *dynamodb.Client, table string, proj ddbtypes.ProjectionType, nonKey []string,
) {
	t.Helper()

	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName:   aws.String(table),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("cat"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{{
			IndexName:  aws.String("by-cat"),
			KeySchema:  []ddbtypes.KeySchemaElement{{AttributeName: aws.String("cat"), KeyType: ddbtypes.KeyTypeHash}},
			Projection: &ddbtypes.Projection{ProjectionType: proj, NonKeyAttributes: nonKey},
		}},
	})
	require.NoError(t, err)
}

// TestGSIQueryProjection covers the GSI attribute-projection contract: a query
// on a KEYS_ONLY index returns only the table + index key attributes; INCLUDE
// adds the named non-key attributes and nothing else; ALL returns everything.
func TestGSIQueryProjection(t *testing.T) {
	ctx := context.Background()

	item := map[string]ddbtypes.AttributeValue{
		"id":    sAttr("u1"),
		"sk":    sAttr("s1"),
		"cat":   sAttr("books"),
		"price": nAttr("42"),
		"title": sAttr("Go"),
		"notes": sAttr("secret"),
	}

	queryByCat := func(client *dynamodb.Client, table string) map[string]ddbtypes.AttributeValue {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String(table),
			IndexName:                 aws.String("by-cat"),
			KeyConditionExpression:    aws.String("cat = :c"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":c": sAttr("books")},
		})
		require.NoError(t, err)
		require.Len(t, out.Items, 1)

		return out.Items[0]
	}

	t.Run("KEYS_ONLY returns only table and index keys", func(t *testing.T) {
		client, _ := newSuiteDDBEnv(t)
		createGSITable(t, client, "kt", ddbtypes.ProjectionTypeKeysOnly, nil)
		suiteDDBPut(t, client, "kt", item)

		got := queryByCat(client, "kt")
		assert.Len(t, got, 3, "only id, sk and cat are projected")
		assert.Equal(t, "u1", attrS(t, got, "id"))
		assert.Equal(t, "s1", attrS(t, got, "sk"))
		assert.Equal(t, "books", attrS(t, got, "cat"))
		_, hasPrice := got["price"]
		assert.False(t, hasPrice, "a non-projected base-table attribute must not appear")
	})

	t.Run("INCLUDE returns keys plus the named non-key attributes", func(t *testing.T) {
		client, _ := newSuiteDDBEnv(t)
		createGSITable(t, client, "it", ddbtypes.ProjectionTypeInclude, []string{"price", "title"})
		suiteDDBPut(t, client, "it", item)

		got := queryByCat(client, "it")
		assert.Len(t, got, 5, "id, sk, cat, price, title")
		assert.Equal(t, "42", attrN(t, got, "price"))
		assert.Equal(t, "Go", attrS(t, got, "title"))
		_, hasNotes := got["notes"]
		assert.False(t, hasNotes, "an attribute not in NonKeyAttributes must not appear")
	})

	t.Run("ALL returns every attribute", func(t *testing.T) {
		client, _ := newSuiteDDBEnv(t)
		createGSITable(t, client, "at", ddbtypes.ProjectionTypeAll, nil)
		suiteDDBPut(t, client, "at", item)

		got := queryByCat(client, "at")
		assert.Len(t, got, 6, "ALL projects the full item")
		assert.Equal(t, "secret", attrS(t, got, "notes"))
	})

	t.Run("ProjectionExpression cannot recover a non-projected attribute", func(t *testing.T) {
		client, _ := newSuiteDDBEnv(t)
		createGSITable(t, client, "pt", ddbtypes.ProjectionTypeKeysOnly, nil)
		suiteDDBPut(t, client, "pt", item)

		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("pt"),
			IndexName:                 aws.String("by-cat"),
			KeyConditionExpression:    aws.String("cat = :c"),
			ProjectionExpression:      aws.String("id, price"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":c": sAttr("books")},
		})
		require.NoError(t, err)
		require.Len(t, out.Items, 1)
		assert.Equal(t, "u1", attrS(t, out.Items[0], "id"))
		_, hasPrice := out.Items[0]["price"]
		assert.False(t, hasPrice, "price is not projected into the index, so it cannot be fetched")
	})
}

// TestGSIProjectionRoundTrip covers that an INCLUDE index's NonKeyAttributes
// round-trip through DescribeTable.
func TestGSIProjectionRoundTrip(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	createGSITable(t, client, "rt", ddbtypes.ProjectionTypeInclude, []string{"price", "title"})

	d, err := client.DescribeTable(context.Background(),
		&dynamodb.DescribeTableInput{TableName: aws.String("rt")})
	require.NoError(t, err)
	require.Len(t, d.Table.GlobalSecondaryIndexes, 1)

	proj := d.Table.GlobalSecondaryIndexes[0].Projection
	require.NotNil(t, proj)
	assert.Equal(t, ddbtypes.ProjectionTypeInclude, proj.ProjectionType)
	assert.ElementsMatch(t, []string{"price", "title"}, proj.NonKeyAttributes)
}

// TestLSIQueryProjection covers the projection contract on a Local Secondary
// Index. Unlike a GSI, an LSI can transparently fetch non-projected attributes
// from the base table (per the AWS LSI developer guide and the Query Select
// rules): with no ProjectionExpression the query defaults to
// ALL_PROJECTED_ATTRIBUTES (a KEYS_ONLY LSI returns only the keys), but a
// ProjectionExpression naming a non-projected attribute recovers it from the
// base table.
func TestLSIQueryProjection(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("lt"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("sk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("alt"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
			{AttributeName: aws.String("sk"), KeyType: ddbtypes.KeyTypeRange},
		},
		LocalSecondaryIndexes: []ddbtypes.LocalSecondaryIndex{{
			IndexName: aws.String("by-alt"),
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
				{AttributeName: aws.String("alt"), KeyType: ddbtypes.KeyTypeRange},
			},
			Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeKeysOnly},
		}},
	})
	require.NoError(t, err)

	suiteDDBPut(t, client, "lt", map[string]ddbtypes.AttributeValue{
		"id": sAttr("u1"), "sk": sAttr("s1"), "alt": sAttr("a1"), "extra": sAttr("x"),
	})

	t.Run("no ProjectionExpression defaults to ALL_PROJECTED_ATTRIBUTES", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("lt"),
			IndexName:                 aws.String("by-alt"),
			KeyConditionExpression:    aws.String("id = :i AND alt = :a"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":i": sAttr("u1"), ":a": sAttr("a1")},
		})
		require.NoError(t, err)
		require.Len(t, out.Items, 1)
		assert.Len(t, out.Items[0], 3, "id, sk and alt only (KEYS_ONLY projected set)")
		_, hasExtra := out.Items[0]["extra"]
		assert.False(t, hasExtra, "a non-projected attribute must not appear without a ProjectionExpression")
	})

	t.Run("ProjectionExpression fetches a non-projected attribute from the base table", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("lt"),
			IndexName:                 aws.String("by-alt"),
			KeyConditionExpression:    aws.String("id = :i AND alt = :a"),
			ProjectionExpression:      aws.String("id, extra"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":i": sAttr("u1"), ":a": sAttr("a1")},
		})
		require.NoError(t, err)
		require.Len(t, out.Items, 1)
		assert.Len(t, out.Items[0], 2, "only the requested attributes")
		assert.Equal(t, "x", attrS(t, out.Items[0], "extra"),
			"an LSI transparently fetches a non-projected attribute from the base table")
	})
}

// TestBatchGetItemProjection covers the per-table ProjectionExpression on
// BatchGetItem: when specified only the named attributes are returned; when
// omitted, all attributes are returned. ExpressionAttributeNames placeholders
// in the projection are honored.
func TestBatchGetItemProjection(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "forum", "name", "")
	suiteDDBPut(t, client, "forum", map[string]ddbtypes.AttributeValue{
		"name":     sAttr("DynamoDB"),
		"threads":  nAttr("5"),
		"messages": nAttr("19"),
		"views":    nAttr("35"),
	})

	t.Run("ProjectionExpression trims each item", func(t *testing.T) {
		out, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
			RequestItems: map[string]ddbtypes.KeysAndAttributes{
				"forum": {
					Keys:                 []map[string]ddbtypes.AttributeValue{{"name": sAttr("DynamoDB")}},
					ProjectionExpression: aws.String("#n, threads"),
					ExpressionAttributeNames: map[string]string{
						"#n": "name",
					},
				},
			},
		})
		require.NoError(t, err)
		got := out.Responses["forum"]
		require.Len(t, got, 1)
		assert.Len(t, got[0], 2, "only name and threads are returned")
		assert.Equal(t, "DynamoDB", attrS(t, got[0], "name"))
		assert.Equal(t, "5", attrN(t, got[0], "threads"))
		_, hasViews := got[0]["views"]
		assert.False(t, hasViews, "a non-projected attribute must not appear")
	})

	t.Run("no ProjectionExpression returns all attributes", func(t *testing.T) {
		out, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
			RequestItems: map[string]ddbtypes.KeysAndAttributes{
				"forum": {Keys: []map[string]ddbtypes.AttributeValue{{"name": sAttr("DynamoDB")}}},
			},
		})
		require.NoError(t, err)
		got := out.Responses["forum"]
		require.Len(t, got, 1)
		assert.Len(t, got[0], 4, "all four attributes are returned")
	})
}
