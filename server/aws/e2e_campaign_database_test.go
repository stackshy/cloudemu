// e2e_campaign_database_test.go — campaign cell DATABASE / aws / sdk-compat.
//
// Real-user-journey E2E tests that drive the genuine aws-sdk-go-v2 DynamoDB
// client against the emulator's HTTP server (httptest). Assertions are made
// on SDK-decoded responses and SDK-visible typed errors, not raw HTTP.
//
// TTL and streams have no DynamoDB HTTP surface in the emulator (the handler
// returns UnknownOperationException for UpdateTimeToLive etc.), so those
// journeys configure TTL/stream settings on the driver directly while item
// traffic still flows through the real SDK.
package aws_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stackshy/cloudemu/v2"
	emuconfig "github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCampaignDDBEnv builds a real DynamoDB SDK client pointed at a fresh
// emulator instance and also returns the backing provider so driver-only
// features (TTL config, streams, GSIs) can be arranged for SDK journeys.
// Retries are disabled so error-path assertions observe exactly one attempt.
func newCampaignDDBEnv(t *testing.T, opts ...emuconfig.Option) (*dynamodb.Client, *awsprovider.Provider) {
	t.Helper()

	provider := cloudemu.NewAWS(opts...)
	srv := awsserver.New(awsserver.Drivers{DynamoDB: provider.DynamoDB})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	require.NoError(t, err)

	client := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.Retryer = aws.NopRetryer{}
	})

	return client, provider
}

// campaignDDBCreateTable creates a table with a string HASH key and an
// optional string RANGE key through the real SDK.
func campaignDDBCreateTable(t *testing.T, client *dynamodb.Client, table, pk, sk string) {
	t.Helper()

	in := &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String(pk), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String(pk), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	}

	if sk != "" {
		in.KeySchema = append(in.KeySchema,
			ddbtypes.KeySchemaElement{AttributeName: aws.String(sk), KeyType: ddbtypes.KeyTypeRange})
		in.AttributeDefinitions = append(in.AttributeDefinitions,
			ddbtypes.AttributeDefinition{AttributeName: aws.String(sk), AttributeType: ddbtypes.ScalarAttributeTypeS})
	}

	_, err := client.CreateTable(context.Background(), in)
	require.NoError(t, err, "CreateTable %q", table)
}

func campaignDDBPut(t *testing.T, client *dynamodb.Client, table string, item map[string]ddbtypes.AttributeValue) {
	t.Helper()

	_, err := client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item,
	})
	require.NoError(t, err, "PutItem into %q", table)
}

func campaignDDBGet(t *testing.T, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) *dynamodb.GetItemOutput {
	t.Helper()

	out, err := client.GetItem(context.Background(), &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key:       key,
	})
	require.NoError(t, err, "GetItem from %q", table)

	return out
}

func sAttr(v string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberS{Value: v} }
func nAttr(v string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberN{Value: v} }

func attrS(t *testing.T, item map[string]ddbtypes.AttributeValue, field string) string {
	t.Helper()

	v, ok := item[field].(*ddbtypes.AttributeValueMemberS)
	require.True(t, ok, "attribute %q should be S, got %T", field, item[field])

	return v.Value
}

func attrN(t *testing.T, item map[string]ddbtypes.AttributeValue, field string) string {
	t.Helper()

	v, ok := item[field].(*ddbtypes.AttributeValueMemberN)
	require.True(t, ok, "attribute %q should be N, got %T", field, item[field])

	return v.Value
}

// TestE2ECampaignDDBTableLifecycle: create a composite-key table, describe it,
// see it in ListTables, delete it, and observe the typed error once gone.
func TestE2ECampaignDDBTableLifecycle(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "journeys", "pk", "sk")

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String("journeys"),
	})
	require.NoError(t, err)
	assert.Equal(t, "journeys", aws.ToString(desc.Table.TableName))
	assert.Equal(t, ddbtypes.TableStatusActive, desc.Table.TableStatus)
	require.Len(t, desc.Table.KeySchema, 2)
	assert.Equal(t, "pk", aws.ToString(desc.Table.KeySchema[0].AttributeName))
	assert.Equal(t, ddbtypes.KeyTypeHash, desc.Table.KeySchema[0].KeyType)
	assert.Equal(t, "sk", aws.ToString(desc.Table.KeySchema[1].AttributeName))
	assert.Equal(t, ddbtypes.KeyTypeRange, desc.Table.KeySchema[1].KeyType)

	list, err := client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.Contains(t, list.TableNames, "journeys")

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("journeys")})
	require.NoError(t, err)

	list, err = client.ListTables(ctx, &dynamodb.ListTablesInput{})
	require.NoError(t, err)
	assert.NotContains(t, list.TableNames, "journeys")

	_, err = client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String("journeys"),
	})

	var rnf *ddbtypes.ResourceNotFoundException

	require.ErrorAs(t, err, &rnf, "DescribeTable on a deleted table should be ResourceNotFoundException")
}

// TestE2ECampaignDDBItemJourney: put an item with varied attribute types
// (S, N incl. negative decimal, BOOL, NULL, L, M, empty string, ~100KB blob),
// read it back through the SDK, update with SET+REMOVE (ReturnValues ALL_NEW),
// then delete and confirm the miss surfaces as Item == nil, not an error.
func TestE2ECampaignDDBItemJourney(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "profiles", "id", "")

	blob := strings.Repeat("x", 100*1024)

	campaignDDBPut(t, client, "profiles", map[string]ddbtypes.AttributeValue{
		"id":       sAttr("user-1"),
		"name":     sAttr("Amélie"),
		"age":      nAttr("34"),
		"score":    nAttr("-2.5"),
		"active":   &ddbtypes.AttributeValueMemberBOOL{Value: true},
		"nickname": sAttr(""), // empty string attribute
		"notes":    &ddbtypes.AttributeValueMemberNULL{Value: true},
		"blob":     sAttr(blob),
		"tags": &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{
			sAttr("alpha"), nAttr("7"),
		}},
		"address": &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{
			"city": sAttr("Paris"),
			"zip":  sAttr("75001"),
		}},
	})

	got := campaignDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
	require.NotNil(t, got.Item)

	assert.Equal(t, "Amélie", attrS(t, got.Item, "name"))
	assert.Equal(t, "34", attrN(t, got.Item, "age"))
	assert.Equal(t, "-2.5", attrN(t, got.Item, "score"))
	assert.Equal(t, "", attrS(t, got.Item, "nickname"))
	assert.Equal(t, blob, attrS(t, got.Item, "blob"))

	active, ok := got.Item["active"].(*ddbtypes.AttributeValueMemberBOOL)
	require.True(t, ok)
	assert.True(t, active.Value)

	null, ok := got.Item["notes"].(*ddbtypes.AttributeValueMemberNULL)
	require.True(t, ok)
	assert.True(t, null.Value)

	tags, ok := got.Item["tags"].(*ddbtypes.AttributeValueMemberL)
	require.True(t, ok)
	require.Len(t, tags.Value, 2)
	assert.Equal(t, "alpha", tags.Value[0].(*ddbtypes.AttributeValueMemberS).Value)
	assert.Equal(t, "7", tags.Value[1].(*ddbtypes.AttributeValueMemberN).Value)

	address, ok := got.Item["address"].(*ddbtypes.AttributeValueMemberM)
	require.True(t, ok)
	assert.Equal(t, "Paris", address.Value["city"].(*ddbtypes.AttributeValueMemberS).Value)

	// Update: SET one field, REMOVE another, and ask for the new image.
	upd, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String("profiles"),
		Key:              map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")},
		UpdateExpression: aws.String("SET age = :a, city = :c REMOVE nickname"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":a": nAttr("35"),
			":c": sAttr("Lyon"),
		},
		ReturnValues: ddbtypes.ReturnValueAllNew,
	})
	require.NoError(t, err)
	require.NotNil(t, upd.Attributes)
	assert.Equal(t, "35", attrN(t, upd.Attributes, "age"))
	assert.Equal(t, "Lyon", attrS(t, upd.Attributes, "city"))
	assert.NotContains(t, upd.Attributes, "nickname")

	got = campaignDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
	require.NotNil(t, got.Item)
	assert.Equal(t, "35", attrN(t, got.Item, "age"))
	assert.NotContains(t, got.Item, "nickname")
	assert.Equal(t, "Amélie", attrS(t, got.Item, "name"), "untouched attribute survives update")

	// Delete and observe the DynamoDB-style miss (empty response, no error).
	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("profiles"),
		Key:       map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")},
	})
	require.NoError(t, err)

	got = campaignDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
	assert.Nil(t, got.Item)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("profiles")})
	require.NoError(t, err)
}

// TestE2ECampaignDDBQueryPartitionAndSort: query by partition key with sort
// conditions =, >, <=, >= and an ExpressionAttributeNames alias. Note the
// emulator wire parser only accepts token-form "pk = :v AND sk <op> :v2"
// (no begins_with()/BETWEEN function syntax).
func TestE2ECampaignDDBQueryPartitionAndSort(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "orders", "customer", "orderDate")

	for _, o := range []struct{ cust, date, total string }{
		{"alice", "2024-01-01", "10"},
		{"alice", "2024-02-15", "25"},
		{"alice", "2024-03-10", "40"},
		{"bob", "2024-01-05", "99"},
	} {
		campaignDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{
			"customer":  sAttr(o.cust),
			"orderDate": sAttr(o.date),
			"total":     nAttr(o.total),
		})
	}

	query := func(expr string, vals map[string]ddbtypes.AttributeValue, names map[string]string) *dynamodb.QueryOutput {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("orders"),
			KeyConditionExpression:    aws.String(expr),
			ExpressionAttributeValues: vals,
			ExpressionAttributeNames:  names,
		})
		require.NoError(t, err, "Query %q", expr)

		return out
	}

	out := query("customer = :c", map[string]ddbtypes.AttributeValue{":c": sAttr("alice")}, nil)
	assert.Equal(t, int32(3), out.Count)
	assert.Len(t, out.Items, 3)

	for _, item := range out.Items {
		assert.Equal(t, "alice", attrS(t, item, "customer"))
	}

	out = query("customer = :c AND orderDate > :d", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":d": sAttr("2024-01-31"),
	}, nil)
	assert.Equal(t, int32(2), out.Count)

	out = query("customer = :c AND orderDate <= :d", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":d": sAttr("2024-01-01"),
	}, nil)
	require.Equal(t, int32(1), out.Count)
	assert.Equal(t, "2024-01-01", attrS(t, out.Items[0], "orderDate"))

	out = query("customer = :c AND orderDate = :d", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":d": sAttr("2024-02-15"),
	}, nil)
	require.Equal(t, int32(1), out.Count)
	assert.Equal(t, "25", attrN(t, out.Items[0], "total"))

	out = query("customer = :c AND orderDate >= :d", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":d": sAttr("2024-02-15"),
	}, nil)
	assert.Equal(t, int32(2), out.Count)

	// #-aliased attribute name resolves through ExpressionAttributeNames.
	out = query("#c = :c", map[string]ddbtypes.AttributeValue{":c": sAttr("bob")},
		map[string]string{"#c": "customer"})
	require.Equal(t, int32(1), out.Count)
	assert.Equal(t, "99", attrN(t, out.Items[0], "total"))
}

// TestE2ECampaignDDBQueryEdges: query on an empty table returns zero items;
// query against a missing table or unknown index yields the typed error.
func TestE2ECampaignDDBQueryEdges(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "empty", "pk", "")

	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("empty"),
		KeyConditionExpression: aws.String("pk = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v": sAttr("anything"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), out.Count)
	assert.Empty(t, out.Items)

	var rnf *ddbtypes.ResourceNotFoundException

	_, err = client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("ghost-table"),
		KeyConditionExpression: aws.String("pk = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v": sAttr("x"),
		},
	})
	require.ErrorAs(t, err, &rnf, "Query on missing table")

	_, err = client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("empty"),
		IndexName:              aws.String("no-such-index"),
		KeyConditionExpression: aws.String("pk = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v": sAttr("x"),
		},
	})
	require.ErrorAs(t, err, &rnf, "Query with unknown IndexName")
}

// TestE2ECampaignDDBScanWithFilters: AND-combined scan filters with =, !=,
// numeric >, <= and the emulator's infix CONTAINS dialect.
func TestE2ECampaignDDBScanWithFilters(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "products", "sku", "")

	for _, p := range []struct {
		sku, cat, name string
		price          string
	}{
		{"sku-1", "electronics", "Blue Widget", "120"},
		{"sku-2", "electronics", "Red Widget", "80"},
		{"sku-3", "electronics", "Gadget Pro", "250"},
		{"sku-4", "books", "Go in Action", "35"},
		{"sku-5", "books", "Widget Design Patterns", "9"},
	} {
		campaignDDBPut(t, client, "products", map[string]ddbtypes.AttributeValue{
			"sku":      sAttr(p.sku),
			"category": sAttr(p.cat),
			"name":     sAttr(p.name),
			"price":    nAttr(p.price),
		})
	}

	scan := func(filter string, vals map[string]ddbtypes.AttributeValue) *dynamodb.ScanOutput {
		in := &dynamodb.ScanInput{TableName: aws.String("products")}
		if filter != "" {
			in.FilterExpression = aws.String(filter)
			in.ExpressionAttributeValues = vals
		}

		out, err := client.Scan(ctx, in)
		require.NoError(t, err, "Scan filter=%q", filter)

		return out
	}

	assert.Equal(t, int32(5), scan("", nil).Count, "unfiltered scan sees all items")

	assert.Equal(t, int32(3), scan("category = :c",
		map[string]ddbtypes.AttributeValue{":c": sAttr("electronics")}).Count)

	assert.Equal(t, int32(2), scan("category != :c",
		map[string]ddbtypes.AttributeValue{":c": sAttr("electronics")}).Count)

	// Numeric comparison: both sides parse as float64.
	out := scan("category = :c AND price > :p", map[string]ddbtypes.AttributeValue{
		":c": sAttr("electronics"), ":p": nAttr("100"),
	})
	require.Equal(t, int32(2), out.Count)

	assert.Equal(t, int32(2), scan("price <= :p",
		map[string]ddbtypes.AttributeValue{":p": nAttr("35")}).Count)

	// Emulator dialect: infix CONTAINS (real DynamoDB uses contains(name, :s)).
	assert.Equal(t, int32(3), scan("name CONTAINS :s",
		map[string]ddbtypes.AttributeValue{":s": sAttr("Widget")}).Count)
}

// TestE2ECampaignDDBScanDefaultReturnsAll: 30 items fit within the driver's
// default limit of 100, so a single unfiltered scan sees every item.
func TestE2ECampaignDDBScanDefaultReturnsAll(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "bulk", "id", "")

	for i := 0; i < 30; i++ {
		campaignDDBPut(t, client, "bulk", map[string]ddbtypes.AttributeValue{
			"id":  sAttr(fmt.Sprintf("item-%02d", i)),
			"idx": nAttr(fmt.Sprintf("%d", i)),
		})
	}

	out, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("bulk")})
	require.NoError(t, err)
	assert.Equal(t, int32(30), out.Count)
	assert.Len(t, out.Items, 30)

	ids := map[string]bool{}
	for _, item := range out.Items {
		ids[attrS(t, item, "id")] = true
	}

	assert.Len(t, ids, 30, "all 30 distinct ids present")

	// Limit caps the page size.
	limited, err := client.Scan(ctx, &dynamodb.ScanInput{
		TableName: aws.String("bulk"),
		Limit:     aws.Int32(10),
	})
	require.NoError(t, err)
	assert.Len(t, limited.Items, 10)
}

// TestE2ECampaignDDBScanPaginationContinuation: a real SDK user pages through
// 30 items with Limit=10 by following LastEvaluatedKey / ExclusiveStartKey
// until exhaustion — the standard DynamoDB pagination contract.
//
// NOTE: the emulator's DynamoDB handler never emits LastEvaluatedKey and
// ignores ExclusiveStartKey (driver-level PageTokens are not wired to the
// HTTP surface), so this journey is expected to surface that divergence.
func TestE2ECampaignDDBScanPaginationContinuation(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "paged", "id", "")

	for i := 0; i < 30; i++ {
		campaignDDBPut(t, client, "paged", map[string]ddbtypes.AttributeValue{
			"id": sAttr(fmt.Sprintf("row-%02d", i)),
		})
	}

	seen := map[string]bool{}

	var startKey map[string]ddbtypes.AttributeValue

	for page := 0; page < 10; page++ {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String("paged"),
			Limit:             aws.Int32(10),
			ExclusiveStartKey: startKey,
		})
		require.NoError(t, err)

		for _, item := range out.Items {
			seen[attrS(t, item, "id")] = true
		}

		if out.LastEvaluatedKey == nil {
			break
		}

		startKey = out.LastEvaluatedKey
	}

	assert.Len(t, seen, 30,
		"paging Limit=10 via LastEvaluatedKey should eventually visit all 30 items")
}

// TestE2ECampaignDDBUnicode: unicode partition keys, sort keys, and values
// round-trip through put/get/query/delete.
func TestE2ECampaignDDBUnicode(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "i18n", "pk", "sk")

	rows := []struct{ pk, sk, val string }{
		{"user-😀", "профиль", "héllo wörld 🌍"},
		{"用户-一", "ソート#1", "日本語のテキスト"},
		{"user-😀", "настройки", "çğüş öëï"},
	}

	for _, r := range rows {
		campaignDDBPut(t, client, "i18n", map[string]ddbtypes.AttributeValue{
			"pk":  sAttr(r.pk),
			"sk":  sAttr(r.sk),
			"val": sAttr(r.val),
		})
	}

	got := campaignDDBGet(t, client, "i18n", map[string]ddbtypes.AttributeValue{
		"pk": sAttr("用户-一"), "sk": sAttr("ソート#1"),
	})
	require.NotNil(t, got.Item)
	assert.Equal(t, "日本語のテキスト", attrS(t, got.Item, "val"))

	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("i18n"),
		KeyConditionExpression: aws.String("pk = :p"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":p": sAttr("user-😀"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), out.Count)

	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("i18n"),
		Key: map[string]ddbtypes.AttributeValue{
			"pk": sAttr("user-😀"), "sk": sAttr("профиль"),
		},
	})
	require.NoError(t, err)

	got = campaignDDBGet(t, client, "i18n", map[string]ddbtypes.AttributeValue{
		"pk": sAttr("user-😀"), "sk": sAttr("профиль"),
	})
	assert.Nil(t, got.Item)
}

// TestE2ECampaignDDBBatchOps: BatchWriteItem puts, BatchGetItem with a
// missing key (silently skipped), then BatchWriteItem deletes.
func TestE2ECampaignDDBBatchOps(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "batch", "id", "")

	var puts []ddbtypes.WriteRequest
	for i := 1; i <= 5; i++ {
		puts = append(puts, ddbtypes.WriteRequest{
			PutRequest: &ddbtypes.PutRequest{Item: map[string]ddbtypes.AttributeValue{
				"id": sAttr(fmt.Sprintf("b-%d", i)),
				"n":  nAttr(fmt.Sprintf("%d", i)),
			}},
		})
	}

	_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{"batch": puts},
	})
	require.NoError(t, err)

	got, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{
			"batch": {Keys: []map[string]ddbtypes.AttributeValue{
				{"id": sAttr("b-1")},
				{"id": sAttr("b-3")},
				{"id": sAttr("b-5")},
				{"id": sAttr("b-404")}, // missing: silently skipped
			}},
		},
	})
	require.NoError(t, err)
	assert.Len(t, got.Responses["batch"], 3, "missing key skipped, no error")
	assert.Empty(t, got.UnprocessedKeys)

	_, err = client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{
			"batch": {
				{DeleteRequest: &ddbtypes.DeleteRequest{Key: map[string]ddbtypes.AttributeValue{"id": sAttr("b-2")}}},
				{DeleteRequest: &ddbtypes.DeleteRequest{Key: map[string]ddbtypes.AttributeValue{"id": sAttr("b-4")}}},
			},
		},
	})
	require.NoError(t, err)

	scan, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("batch")})
	require.NoError(t, err)
	assert.Equal(t, int32(3), scan.Count)
}

// TestE2ECampaignDDBTransactAcrossTables: a single TransactWriteItems mixing
// puts and a delete across two tables.
func TestE2ECampaignDDBTransactAcrossTables(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "accounts", "id", "")
	campaignDDBCreateTable(t, client, "audit", "id", "")

	campaignDDBPut(t, client, "accounts", map[string]ddbtypes.AttributeValue{
		"id": sAttr("stale"), "state": sAttr("old"),
	})

	_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{
				TableName: aws.String("accounts"),
				Item: map[string]ddbtypes.AttributeValue{
					"id": sAttr("acct-1"), "balance": nAttr("500"),
				},
			}},
			{Put: &ddbtypes.Put{
				TableName: aws.String("audit"),
				Item: map[string]ddbtypes.AttributeValue{
					"id": sAttr("evt-1"), "action": sAttr("credit"),
				},
			}},
			{Delete: &ddbtypes.Delete{
				TableName: aws.String("accounts"),
				Key:       map[string]ddbtypes.AttributeValue{"id": sAttr("stale")},
			}},
		},
	})
	require.NoError(t, err)

	got := campaignDDBGet(t, client, "accounts", map[string]ddbtypes.AttributeValue{"id": sAttr("acct-1")})
	require.NotNil(t, got.Item)
	assert.Equal(t, "500", attrN(t, got.Item, "balance"))

	got = campaignDDBGet(t, client, "audit", map[string]ddbtypes.AttributeValue{"id": sAttr("evt-1")})
	require.NotNil(t, got.Item)

	got = campaignDDBGet(t, client, "accounts", map[string]ddbtypes.AttributeValue{"id": sAttr("stale")})
	assert.Nil(t, got.Item, "delete inside transaction applied")
}

// TestE2ECampaignDDBTypedErrors: the SDK-visible typed errors on the main
// failure paths, plus emulator-specific quirks that diverge from AWS.
func TestE2ECampaignDDBTypedErrors(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "errs", "pk", "")

	var (
		rnf   *ddbtypes.ResourceNotFoundException
		inUse *ddbtypes.ResourceInUseException
	)

	t.Run("duplicate CreateTable is ResourceInUseException", func(t *testing.T) {
		_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
			TableName: aws.String("errs"),
			KeySchema: []ddbtypes.KeySchemaElement{
				{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
			},
			AttributeDefinitions: []ddbtypes.AttributeDefinition{
				{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			},
			BillingMode: ddbtypes.BillingModePayPerRequest,
		})
		require.ErrorAs(t, err, &inUse)
	})

	t.Run("DeleteTable on missing table is ResourceNotFoundException", func(t *testing.T) {
		_, err := client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("ghost")})
		require.ErrorAs(t, err, &rnf)
	})

	t.Run("PutItem into missing table is ResourceNotFoundException", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String("ghost"),
			Item:      map[string]ddbtypes.AttributeValue{"pk": sAttr("x")},
		})
		require.ErrorAs(t, err, &rnf)
	})

	t.Run("DeleteItem on missing table is ResourceNotFoundException", func(t *testing.T) {
		_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String("ghost"),
			Key:       map[string]ddbtypes.AttributeValue{"pk": sAttr("x")},
		})
		require.ErrorAs(t, err, &rnf)
	})

	t.Run("DeleteItem on missing item is idempotent", func(t *testing.T) {
		_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String("errs"),
			Key:       map[string]ddbtypes.AttributeValue{"pk": sAttr("never-existed")},
		})
		require.NoError(t, err, "DeleteItem is idempotent like real DynamoDB")
	})

	t.Run("UpdateItem on missing item is ResourceNotFoundException", func(t *testing.T) {
		// Documented emulator divergence: real DynamoDB upserts here.
		_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        aws.String("errs"),
			Key:              map[string]ddbtypes.AttributeValue{"pk": sAttr("missing")},
			UpdateExpression: aws.String("SET v = :v"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":v": nAttr("1"),
			},
		})
		require.ErrorAs(t, err, &rnf)
	})

	t.Run("GetItem on missing table flattens to empty response", func(t *testing.T) {
		// Documented emulator quirk: the handler converts ANY driver NotFound
		// (missing item OR missing table) into an empty 200, so the SDK sees
		// Item == nil instead of ResourceNotFoundException.
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String("ghost"),
			Key:       map[string]ddbtypes.AttributeValue{"pk": sAttr("x")},
		})
		require.NoError(t, err)
		assert.Nil(t, out.Item)
	})

	t.Run("unrouted operation is UnknownOperationException", func(t *testing.T) {
		// UpdateTimeToLive has no HTTP surface in the emulator.
		_, err := client.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
			TableName: aws.String("errs"),
			TimeToLiveSpecification: &ddbtypes.TimeToLiveSpecification{
				AttributeName: aws.String("ttl"),
				Enabled:       aws.Bool(true),
			},
		})
		require.Error(t, err)

		var apiErr smithy.APIError

		require.True(t, errors.As(err, &apiErr))
		assert.Equal(t, "UnknownOperationException", apiErr.ErrorCode())
	})
}

// TestE2ECampaignDDBConditionalWrites: a conditional put succeeds when the
// item is absent, and violating the condition must yield
// ConditionalCheckFailedException like real DynamoDB.
//
// NOTE: the emulator does not parse ConditionExpression at all (PutItem is a
// blind upsert with no ConditionalCheckFailedException path), so the
// violation subtest is expected to surface that divergence.
func TestE2ECampaignDDBConditionalWrites(t *testing.T) {
	client, _ := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "cond", "pk", "")

	t.Run("condition passes on absent item", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String("cond"),
			Item:                map[string]ddbtypes.AttributeValue{"pk": sAttr("c1"), "v": nAttr("1")},
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		})
		require.NoError(t, err)
	})

	t.Run("condition violation fails the write", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:           aws.String("cond"),
			Item:                map[string]ddbtypes.AttributeValue{"pk": sAttr("c1"), "v": nAttr("2")},
			ConditionExpression: aws.String("attribute_not_exists(pk)"),
		})

		var ccf *ddbtypes.ConditionalCheckFailedException

		require.ErrorAs(t, err, &ccf,
			"PutItem with attribute_not_exists on an existing item must fail")

		// The original item must be untouched by the rejected write.
		got := campaignDDBGet(t, client, "cond", map[string]ddbtypes.AttributeValue{"pk": sAttr("c1")})
		require.NotNil(t, got.Item)
		assert.Equal(t, "1", attrN(t, got.Item, "v"))
	})
}

// TestE2ECampaignDDBTTLExpiry: deterministic TTL expiry with the injectable
// fake clock. TTL configuration is driver-only (no HTTP surface), so it is
// set on the provider directly; all item traffic uses the real SDK.
func TestE2ECampaignDDBTTLExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := emuconfig.NewFakeClock(base)
	client, provider := newCampaignDDBEnv(t, emuconfig.WithClock(clock))
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "sessions", "id", "")

	campaignDDBPut(t, client, "sessions", map[string]ddbtypes.AttributeValue{
		"id":        sAttr("sess-1"),
		"expiresAt": nAttr(fmt.Sprintf("%d", base.Add(5*time.Minute).Unix())),
	})
	campaignDDBPut(t, client, "sessions", map[string]ddbtypes.AttributeValue{
		"id": sAttr("sess-forever"), // no TTL attribute: never expires
	})

	require.NoError(t, provider.DynamoDB.UpdateTTL(ctx, "sessions", dbdriver.TTLConfig{
		Enabled:       true,
		AttributeName: "expiresAt",
	}))

	ttlCfg, err := provider.DynamoDB.DescribeTTL(ctx, "sessions")
	require.NoError(t, err)
	assert.True(t, ttlCfg.Enabled)
	assert.Equal(t, "expiresAt", ttlCfg.AttributeName)

	// Before expiry, the item is visible through the SDK.
	got := campaignDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	require.NotNil(t, got.Item, "not yet expired")

	// Advance the fake clock past the TTL.
	clock.Advance(10 * time.Minute)

	got = campaignDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	assert.Nil(t, got.Item, "expired item reads as a miss")

	got = campaignDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-forever")})
	assert.NotNil(t, got.Item, "item without TTL attribute survives")

	// Scan also skips expired items.
	scan, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("sessions")})
	require.NoError(t, err)
	assert.Equal(t, int32(1), scan.Count)

	// The expired GetItem lazily deleted the item: even after disabling TTL
	// it stays gone.
	require.NoError(t, provider.DynamoDB.UpdateTTL(ctx, "sessions", dbdriver.TTLConfig{Enabled: false}))

	got = campaignDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	assert.Nil(t, got.Item, "lazy deletion is physical")
}

// TestE2ECampaignDDBStreams: SDK writes produce driver-level stream records
// (INSERT/MODIFY/REMOVE) with monotonic sequence numbers, clock-driven
// timestamps, view-type images, and token-based continuation. Streams have no
// HTTP surface, so records are read from the driver.
func TestE2ECampaignDDBStreams(t *testing.T) {
	base := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	clock := emuconfig.NewFakeClock(base)
	client, provider := newCampaignDDBEnv(t, emuconfig.WithClock(clock))
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "events", "id", "")

	// Streams disabled: reading records is a failed precondition.
	_, err := provider.DynamoDB.GetStreamRecords(ctx, "events", 10, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	require.NoError(t, provider.DynamoDB.UpdateStreamConfig(ctx, "events", dbdriver.StreamConfig{
		Enabled:  true,
		ViewType: "NEW_AND_OLD_IMAGES",
	}))

	// SDK traffic: insert, overwrite, update, delete.
	campaignDDBPut(t, client, "events", map[string]ddbtypes.AttributeValue{
		"id": sAttr("e1"), "v": nAttr("1"),
	})
	campaignDDBPut(t, client, "events", map[string]ddbtypes.AttributeValue{
		"id": sAttr("e1"), "v": nAttr("2"),
	})

	_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String("events"),
		Key:              map[string]ddbtypes.AttributeValue{"id": sAttr("e1")},
		UpdateExpression: aws.String("SET v = :v"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":v": nAttr("3"),
		},
	})
	require.NoError(t, err)

	_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String("events"),
		Key:       map[string]ddbtypes.AttributeValue{"id": sAttr("e1")},
	})
	require.NoError(t, err)

	it, err := provider.DynamoDB.GetStreamRecords(ctx, "events", 100, "")
	require.NoError(t, err)
	assert.Equal(t, "shard-000", it.ShardID)
	require.Len(t, it.Records, 4)

	types := make([]string, 0, len(it.Records))
	seqs := make([]string, 0, len(it.Records))

	for _, r := range it.Records {
		types = append(types, r.EventType)
		seqs = append(seqs, r.SequenceNumber)

		assert.Equal(t, "events", r.Table)
		assert.Equal(t, base, r.Timestamp, "timestamps come from the fake clock")
		assert.Equal(t, "e1", fmt.Sprintf("%v", r.Keys["id"]))
	}

	assert.Equal(t, []string{"INSERT", "MODIFY", "MODIFY", "REMOVE"}, types)
	assert.Equal(t, []string{"1", "2", "3", "4"}, seqs)

	// NEW_AND_OLD_IMAGES captures both sides. Wire N values decode to float64.
	assert.Nil(t, it.Records[0].OldImage, "INSERT has no old image")
	assert.EqualValues(t, 1, it.Records[0].NewImage["v"])
	assert.EqualValues(t, 1, it.Records[1].OldImage["v"])
	assert.EqualValues(t, 2, it.Records[1].NewImage["v"])
	assert.EqualValues(t, 3, it.Records[3].OldImage["v"], "REMOVE carries the old image")
	assert.Nil(t, it.Records[3].NewImage, "REMOVE has no new image")

	// Token-based continuation: limit 2, then resume from the returned token.
	page1, err := provider.DynamoDB.GetStreamRecords(ctx, "events", 2, "")
	require.NoError(t, err)
	require.Len(t, page1.Records, 2)
	assert.Equal(t, "2", page1.NextToken)

	page2, err := provider.DynamoDB.GetStreamRecords(ctx, "events", 100, page1.NextToken)
	require.NoError(t, err)
	require.Len(t, page2.Records, 2)
	assert.Equal(t, "3", page2.Records[0].SequenceNumber)
	assert.Empty(t, page2.NextToken, "no more records after the last page")
}

// TestE2ECampaignDDBQueryGSI: query through a Global Secondary Index. GSI
// creation has no HTTP surface (CreateTable ignores
// GlobalSecondaryIndexes), so the index is created on the driver; queries go
// through the real SDK with IndexName.
func TestE2ECampaignDDBQueryGSI(t *testing.T) {
	client, provider := newCampaignDDBEnv(t)
	ctx := context.Background()

	campaignDDBCreateTable(t, client, "users", "id", "")

	info, err := provider.DynamoDB.CreateIndex(ctx, "users", dbdriver.GSIConfig{
		Name:         "by-email",
		PartitionKey: "email",
	})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE", info.Status)

	for _, u := range []struct{ id, email string }{
		{"u1", "shared@example.com"},
		{"u2", "shared@example.com"},
		{"u3", "solo@example.com"},
	} {
		campaignDDBPut(t, client, "users", map[string]ddbtypes.AttributeValue{
			"id": sAttr(u.id), "email": sAttr(u.email),
		})
	}

	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("users"),
		IndexName:              aws.String("by-email"),
		KeyConditionExpression: aws.String("email = :e"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":e": sAttr("shared@example.com"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), out.Count)

	ids := map[string]bool{}
	for _, item := range out.Items {
		ids[attrS(t, item, "id")] = true
	}

	assert.True(t, ids["u1"] && ids["u2"], "GSI query returns both matching users")
}
