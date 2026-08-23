// dynamodb_lifecycle_test.go — //
// Real-user-journey  tests that drive the genuine aws-sdk-go-v2 DynamoDB
// client against the emulator's HTTP server (httptest). Assertions are made
// on SDK-decoded responses and SDK-visible typed errors, not raw HTTP.
//
// TTL and streams have no DynamoDB HTTP surface in the emulator (the handler
// returns UnknownOperationException for UpdateTimeToLive etc.), so those
// journeys configure TTL/stream settings on the driver directly while item
// traffic still flows through the real SDK.
package dynamodb_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
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

// retryClosedNetConn marks the transient "use of closed network connection"
// that httptest surfaces during response-body reads under parallel load as
// retryable; every other error defers to the standard retryer's classifiers.
type retryClosedNetConn struct{}

func (retryClosedNetConn) IsErrorRetryable(err error) aws.Ternary {
	// The teardown race surfaces as net.ErrClosed ("use of closed network
	// connection"), wrapped by the SDK's deserialization layer. Match the typed
	// sentinel (robust to wording changes) with a string fallback (robust to a
	// wrapper that breaks the Unwrap chain).
	if err != nil && (errors.Is(err, net.ErrClosed) ||
		strings.Contains(err.Error(), "use of closed network connection")) {
		return aws.TrueTernary
	}

	return aws.UnknownTernary
}

func newSuiteDDBEnv(t *testing.T, opts ...emuconfig.Option) (*dynamodb.Client, *awsprovider.Provider) {
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
		// httptest servers under parallel CI load occasionally close the TCP
		// connection while the SDK is still reading a (200) response body,
		// surfacing as "use of closed network connection". Retry ONLY that
		// transient transport error — the retryables list is replaced (not
		// extended), so API errors and the emulator's 500s are still observed on
		// exactly one attempt, as the negative-path assertions expect.
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.Retryables = []retry.IsErrorRetryable{retryClosedNetConn{}}
		})
		// Fresh connection per request avoids reusing a since-closed keep-alive.
		o.HTTPClient = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	})

	return client, provider
}

// suiteDDBCreateTable creates a table with a string HASH key and an
// optional string RANGE key through the real SDK.
func suiteDDBCreateTable(t *testing.T, client *dynamodb.Client, table, pk, sk string) {
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

func suiteDDBPut(t *testing.T, client *dynamodb.Client, table string, item map[string]ddbtypes.AttributeValue) {
	t.Helper()

	_, err := client.PutItem(context.Background(), &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item:      item,
	})
	require.NoError(t, err, "PutItem into %q", table)
}

func suiteDDBGet(t *testing.T, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) *dynamodb.GetItemOutput {
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

// TestDDBTableLifecycle: create a composite-key table, describe it,
// see it in ListTables, delete it, and observe the typed error once gone.
func TestDDBTableLifecycle(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "journeys", "pk", "sk")

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

// TestDDBTagging is a regression guard for issue #319: TagResource /
// UntagResource / ListTagsOfResource returned UnknownOperationException.
func TestDDBTagging(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tagged", "pk", "sk")

	arn := "arn:aws:dynamodb:us-east-1:000000000000:table/tagged"

	if _, err := client.TagResource(ctx, &dynamodb.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags: []ddbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("data")},
		},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	list, err := client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
	require.NoError(t, err)
	require.Len(t, list.Tags, 2)

	if _, err := client.UntagResource(ctx, &dynamodb.UntagResourceInput{
		ResourceArn: aws.String(arn), TagKeys: []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	list, err = client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
	require.NoError(t, err)
	require.Len(t, list.Tags, 1)
	assert.Equal(t, "team", aws.ToString(list.Tags[0].Key))
}

// TestDDBItemJourney: put an item with varied attribute types
// (S, N incl. negative decimal, BOOL, NULL, L, M, empty string, ~100KB blob),
// read it back through the SDK, update with SET+REMOVE (ReturnValues ALL_NEW),
// then delete and confirm the miss surfaces as Item == nil, not an error.
func TestDDBItemJourney(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "profiles", "id", "")

	blob := strings.Repeat("x", 100*1024)

	suiteDDBPut(t, client, "profiles", map[string]ddbtypes.AttributeValue{
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

	got := suiteDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
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

	got = suiteDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
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

	got = suiteDDBGet(t, client, "profiles", map[string]ddbtypes.AttributeValue{"id": sAttr("user-1")})
	assert.Nil(t, got.Item)

	_, err = client.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String("profiles")})
	require.NoError(t, err)
}

// TestDDBQueryPartitionAndSort: query by partition key with sort
// conditions =, >, <=, >=, BETWEEN and begins_with(), plus an
// ExpressionAttributeNames alias.
func TestDDBQueryPartitionAndSort(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "orders", "customer", "orderDate")

	for _, o := range []struct{ cust, date, total string }{
		{"alice", "2024-01-01", "10"},
		{"alice", "2024-02-15", "25"},
		{"alice", "2024-03-10", "40"},
		{"bob", "2024-01-05", "99"},
	} {
		suiteDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{
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

	// BETWEEN is inclusive on both bounds.
	out = query("customer = :c AND orderDate BETWEEN :lo AND :hi", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":lo": sAttr("2024-01-01"), ":hi": sAttr("2024-02-28"),
	}, nil)
	assert.Equal(t, int32(2), out.Count, "BETWEEN spans Jan 1 and Feb 15 inclusive, excludes Mar 10")

	// begins_with matches the sort-key prefix.
	out = query("customer = :c AND begins_with(orderDate, :p)", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":p": sAttr("2024-02"),
	}, nil)
	require.Equal(t, int32(1), out.Count)
	assert.Equal(t, "2024-02-15", attrS(t, out.Items[0], "orderDate"))

	// A wider begins_with prefix matches every alice order.
	out = query("customer = :c AND begins_with(orderDate, :p)", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":p": sAttr("2024"),
	}, nil)
	assert.Equal(t, int32(3), out.Count)

	// The parser tolerates missing spaces around the operators.
	out = query("customer=:c AND orderDate>:d", map[string]ddbtypes.AttributeValue{
		":c": sAttr("alice"), ":d": sAttr("2024-01-31"),
	}, nil)
	assert.Equal(t, int32(2), out.Count, "space-less operators parse correctly")
}

// TestDDBTimeToLive is a regression guard for issue #319:
// DescribeTimeToLive / UpdateTimeToLive returned UnknownOperationException.
func TestDDBTimeToLive(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "ttl-table", "pk", "")

	desc, err := client.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{
		TableName: aws.String("ttl-table"),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.TimeToLiveStatusDisabled, desc.TimeToLiveDescription.TimeToLiveStatus)

	if _, err := client.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
		TableName: aws.String("ttl-table"),
		TimeToLiveSpecification: &ddbtypes.TimeToLiveSpecification{
			Enabled: aws.Bool(true), AttributeName: aws.String("expiresAt"),
		},
	}); err != nil {
		t.Fatalf("UpdateTimeToLive: %v", err)
	}

	desc, err = client.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{
		TableName: aws.String("ttl-table"),
	})
	require.NoError(t, err)
	assert.Equal(t, ddbtypes.TimeToLiveStatusEnabled, desc.TimeToLiveDescription.TimeToLiveStatus)
	assert.Equal(t, "expiresAt", aws.ToString(desc.TimeToLiveDescription.AttributeName))
}

// TestDDBQueryWithFilterExpression is a regression guard for issue #319: Query
// ignored FilterExpression and returned the full key-matched set (silent wrong
// data), while Scan applied it correctly.
func TestDDBQueryWithFilterExpression(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "orders", "customer", "orderDate")

	for _, o := range []struct{ date, total string }{
		{"2024-01-01", "10"},
		{"2024-02-15", "70"},
		{"2024-03-10", "40"},
	} {
		suiteDDBPut(t, client, "orders", map[string]ddbtypes.AttributeValue{
			"customer":  sAttr("alice"),
			"orderDate": sAttr(o.date),
			"total":     nAttr(o.total),
		})
	}

	// Key matches 3 rows; the filter (total > 50) should leave only 1.
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("orders"),
		KeyConditionExpression: aws.String("customer = :c"),
		FilterExpression:       aws.String("total > :m"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":c": sAttr("alice"), ":m": nAttr("50"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), out.Count, "FilterExpression must prune the key-matched set")
	assert.Equal(t, "70", attrN(t, out.Items[0], "total"))
}

// TestDDBQueryEdges: query on an empty table returns zero items;
// query against a missing table or unknown index yields the typed error.
func TestDDBQueryEdges(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "empty", "pk", "")

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

// TestDDBScanWithFilters: AND-combined scan filters with =, <>,
// numeric >, <= and the real contains() function form.
func TestDDBScanWithFilters(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "products", "sku", "")

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
		suiteDDBPut(t, client, "products", map[string]ddbtypes.AttributeValue{
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

	assert.Equal(t, int32(2), scan("category <> :c",
		map[string]ddbtypes.AttributeValue{":c": sAttr("electronics")}).Count)

	// Numeric comparison: both sides parse as float64.
	out := scan("category = :c AND price > :p", map[string]ddbtypes.AttributeValue{
		":c": sAttr("electronics"), ":p": nAttr("100"),
	})
	require.Equal(t, int32(2), out.Count)

	assert.Equal(t, int32(2), scan("price <= :p",
		map[string]ddbtypes.AttributeValue{":p": nAttr("35")}).Count)

	// Real DynamoDB contains(path, operand) function form.
	assert.Equal(t, int32(3), scan("contains(name, :s)",
		map[string]ddbtypes.AttributeValue{":s": sAttr("Widget")}).Count)
}

// TestDDBScanDefaultReturnsAll: 30 items fit within the driver's
// default limit of 100, so a single unfiltered scan sees every item.
func TestDDBScanDefaultReturnsAll(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "bulk", "id", "")

	for i := 0; i < 30; i++ {
		suiteDDBPut(t, client, "bulk", map[string]ddbtypes.AttributeValue{
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

// TestDDBScanPaginationContinuation: a real SDK user pages through
// 30 items with Limit=10 by following LastEvaluatedKey / ExclusiveStartKey
// until exhaustion — the standard DynamoDB pagination contract.
//
// NOTE: the emulator's DynamoDB handler never emits LastEvaluatedKey and
// ignores ExclusiveStartKey (driver-level PageTokens are not wired to the
// HTTP surface), so this journey is expected to surface that divergence.
func TestDDBScanPaginationContinuation(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "paged", "id", "")

	for i := 0; i < 30; i++ {
		suiteDDBPut(t, client, "paged", map[string]ddbtypes.AttributeValue{
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

// TestDDBUnicode: unicode partition keys, sort keys, and values
// round-trip through put/get/query/delete.
func TestDDBUnicode(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "i18n", "pk", "sk")

	rows := []struct{ pk, sk, val string }{
		{"user-😀", "профиль", "héllo wörld 🌍"},
		{"用户-一", "ソート#1", "日本語のテキスト"},
		{"user-😀", "настройки", "çğüş öëï"},
	}

	for _, r := range rows {
		suiteDDBPut(t, client, "i18n", map[string]ddbtypes.AttributeValue{
			"pk":  sAttr(r.pk),
			"sk":  sAttr(r.sk),
			"val": sAttr(r.val),
		})
	}

	got := suiteDDBGet(t, client, "i18n", map[string]ddbtypes.AttributeValue{
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

	got = suiteDDBGet(t, client, "i18n", map[string]ddbtypes.AttributeValue{
		"pk": sAttr("user-😀"), "sk": sAttr("профиль"),
	})
	assert.Nil(t, got.Item)
}

// TestDDBBatchOps: BatchWriteItem puts, BatchGetItem with a
// missing key (silently skipped), then BatchWriteItem deletes.
func TestDDBBatchOps(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "batch", "id", "")

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

// TestDDBTransactAcrossTables: a single TransactWriteItems mixing
// puts and a delete across two tables.
func TestDDBTransactAcrossTables(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "accounts", "id", "")
	suiteDDBCreateTable(t, client, "audit", "id", "")

	suiteDDBPut(t, client, "accounts", map[string]ddbtypes.AttributeValue{
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

	got := suiteDDBGet(t, client, "accounts", map[string]ddbtypes.AttributeValue{"id": sAttr("acct-1")})
	require.NotNil(t, got.Item)
	assert.Equal(t, "500", attrN(t, got.Item, "balance"))

	got = suiteDDBGet(t, client, "audit", map[string]ddbtypes.AttributeValue{"id": sAttr("evt-1")})
	require.NotNil(t, got.Item)

	got = suiteDDBGet(t, client, "accounts", map[string]ddbtypes.AttributeValue{"id": sAttr("stale")})
	assert.Nil(t, got.Item, "delete inside transaction applied")
}

// TestDDBTypedErrors: the SDK-visible typed errors on the main
// failure paths, plus emulator-specific quirks that diverge from AWS.
func TestDDBTypedErrors(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "errs", "pk", "")

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
		// DescribeLimits has no HTTP surface in the emulator.
		_, err := client.DescribeLimits(ctx, &dynamodb.DescribeLimitsInput{})
		require.Error(t, err)

		var apiErr smithy.APIError

		require.True(t, errors.As(err, &apiErr))
		assert.Equal(t, "UnknownOperationException", apiErr.ErrorCode())
	})
}

// TestDDBConditionalWrites: a conditional put succeeds when the
// item is absent, and violating the condition must yield
// ConditionalCheckFailedException like real DynamoDB.
func TestDDBConditionalWrites(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "cond", "pk", "")

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
		got := suiteDDBGet(t, client, "cond", map[string]ddbtypes.AttributeValue{"pk": sAttr("c1")})
		require.NotNil(t, got.Item)
		assert.Equal(t, "1", attrN(t, got.Item, "v"))
	})
}

// TestDDBTTLExpiry: deterministic TTL expiry with the injectable
// fake clock. TTL configuration is driver-only (no HTTP surface), so it is
// set on the provider directly; all item traffic uses the real SDK.
func TestDDBTTLExpiry(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	clock := emuconfig.NewFakeClock(base)
	client, provider := newSuiteDDBEnv(t, emuconfig.WithClock(clock))
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "sessions", "id", "")

	suiteDDBPut(t, client, "sessions", map[string]ddbtypes.AttributeValue{
		"id":        sAttr("sess-1"),
		"expiresAt": nAttr(fmt.Sprintf("%d", base.Add(5*time.Minute).Unix())),
	})
	suiteDDBPut(t, client, "sessions", map[string]ddbtypes.AttributeValue{
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
	got := suiteDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	require.NotNil(t, got.Item, "not yet expired")

	// Advance the fake clock past the TTL.
	clock.Advance(10 * time.Minute)

	got = suiteDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	assert.Nil(t, got.Item, "expired item reads as a miss")

	got = suiteDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-forever")})
	assert.NotNil(t, got.Item, "item without TTL attribute survives")

	// Scan also skips expired items.
	scan, err := client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("sessions")})
	require.NoError(t, err)
	assert.Equal(t, int32(1), scan.Count)

	// The expired GetItem lazily deleted the item: even after disabling TTL
	// it stays gone.
	require.NoError(t, provider.DynamoDB.UpdateTTL(ctx, "sessions", dbdriver.TTLConfig{Enabled: false}))

	got = suiteDDBGet(t, client, "sessions", map[string]ddbtypes.AttributeValue{"id": sAttr("sess-1")})
	assert.Nil(t, got.Item, "lazy deletion is physical")
}

// TestDDBStreams: SDK writes produce driver-level stream records
// (INSERT/MODIFY/REMOVE) with monotonic sequence numbers, clock-driven
// timestamps, view-type images, and token-based continuation. Streams have no
// HTTP surface, so records are read from the driver.
func TestDDBStreams(t *testing.T) {
	base := time.Date(2026, 2, 2, 8, 0, 0, 0, time.UTC)
	clock := emuconfig.NewFakeClock(base)
	client, provider := newSuiteDDBEnv(t, emuconfig.WithClock(clock))
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "events", "id", "")

	// Streams disabled: reading records is a failed precondition.
	_, err := provider.DynamoDB.GetStreamRecords(ctx, "events", 10, "")
	require.Error(t, err)
	assert.True(t, cerrors.IsFailedPrecondition(err))

	require.NoError(t, provider.DynamoDB.UpdateStreamConfig(ctx, "events", dbdriver.StreamConfig{
		Enabled:  true,
		ViewType: "NEW_AND_OLD_IMAGES",
	}))

	// SDK traffic: insert, overwrite, update, delete.
	suiteDDBPut(t, client, "events", map[string]ddbtypes.AttributeValue{
		"id": sAttr("e1"), "v": nAttr("1"),
	})
	suiteDDBPut(t, client, "events", map[string]ddbtypes.AttributeValue{
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

// TestDDBQueryGSI: query through a Global Secondary Index. GSI
// creation has no HTTP surface (CreateTable ignores
// GlobalSecondaryIndexes), so the index is created on the driver; queries go
// through the real SDK with IndexName.
func TestDDBQueryGSI(t *testing.T) {
	client, provider := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "users", "id", "")

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
		suiteDDBPut(t, client, "users", map[string]ddbtypes.AttributeValue{
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

// lAttr builds a DynamoDB list AttributeValue from element AttributeValues.
func lAttr(elems ...ddbtypes.AttributeValue) ddbtypes.AttributeValue {
	return &ddbtypes.AttributeValueMemberL{Value: elems}
}

// TestDDBScanFilterExpressionGrammar drives the full FilterExpression grammar
// through a real-SDK Scan: functions (attribute_exists/_not_exists, contains,
// size), boolean operators (OR, NOT), IN, BETWEEN, and type-aware equality
// (a numeric literal must not match a value stored as a string).
func TestDDBScanFilterExpressionGrammar(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "people", "id", "")

	suiteDDBPut(t, client, "people", map[string]ddbtypes.AttributeValue{
		"id": sAttr("p1"), "email": sAttr("a@x.com"), "name": sAttr("Alice"),
		"tags": lAttr(sAttr("red"), sAttr("blue")), "n": nAttr("10"), "status": sAttr("active"),
	})
	suiteDDBPut(t, client, "people", map[string]ddbtypes.AttributeValue{
		"id": sAttr("p2"), "email": sAttr("b@x.com"), "name": sAttr("Bob"),
		"tags": lAttr(sAttr("green")), "n": nAttr("20"), "status": sAttr("inactive"),
	})
	suiteDDBPut(t, client, "people", map[string]ddbtypes.AttributeValue{
		"id": sAttr("p3"), "name": sAttr("Carol"), // no email
		"tags": lAttr(sAttr("red")), "n": nAttr("30"), "status": sAttr("pending"),
	})
	suiteDDBPut(t, client, "people", map[string]ddbtypes.AttributeValue{
		"id": sAttr("p4"), "email": sAttr("d@x.com"), "name": sAttr("Dave"),
		"n": sAttr("25"), "status": sAttr("active"), // n stored as a STRING
	})

	scanF := func(filter string, names map[string]string, vals map[string]ddbtypes.AttributeValue) int32 {
		in := &dynamodb.ScanInput{
			TableName:                 aws.String("people"),
			FilterExpression:          aws.String(filter),
			ExpressionAttributeValues: vals,
			ExpressionAttributeNames:  names,
		}

		out, err := client.Scan(ctx, in)
		require.NoError(t, err, "Scan filter=%q", filter)

		return out.Count
	}

	assert.Equal(t, int32(3), scanF("attribute_exists(email)", nil, nil),
		"attribute_exists selects rows that have the attribute")
	assert.Equal(t, int32(4), scanF("attribute_not_exists(x)", nil, nil),
		"attribute_not_exists(x) matches every row (x is never present)")
	assert.Equal(t, int32(2), scanF("contains(tags, :t)", nil,
		map[string]ddbtypes.AttributeValue{":t": sAttr("red")}),
		"contains() tests list membership")
	assert.Equal(t, int32(2), scanF("size(#nm) > :len",
		map[string]string{"#nm": "name"},
		map[string]ddbtypes.AttributeValue{":len": nAttr("4")}),
		"size() of a string attribute compared numerically")
	assert.Equal(t, int32(3), scanF("status = :s1 OR n = :n1", nil,
		map[string]ddbtypes.AttributeValue{":s1": sAttr("active"), ":n1": nAttr("30")}),
		"OR unions two single-attribute comparisons")
	assert.Equal(t, int32(2), scanF("NOT status = :s", nil,
		map[string]ddbtypes.AttributeValue{":s": sAttr("active")}),
		"NOT negates the inner comparison")
	assert.Equal(t, int32(3), scanF("status IN (:s1, :s2)", nil,
		map[string]ddbtypes.AttributeValue{":s1": sAttr("active"), ":s2": sAttr("pending")}),
		"IN matches any listed value")
	assert.Equal(t, int32(2), scanF("n BETWEEN :lo AND :hi", nil,
		map[string]ddbtypes.AttributeValue{":lo": nAttr("15"), ":hi": nAttr("30")}),
		"BETWEEN is inclusive and numeric (the string-typed n is excluded)")

	// Type-aware equality: :numeric (N 25) must NOT match p4's n stored as the
	// string "25", but a numeric equality on a numerically-stored value does.
	assert.Equal(t, int32(0), scanF("n = :numeric", nil,
		map[string]ddbtypes.AttributeValue{":numeric": nAttr("25")}),
		"a number literal must not equal a string-typed attribute")
	assert.Equal(t, int32(1), scanF("n = :ten", nil,
		map[string]ddbtypes.AttributeValue{":ten": nAttr("10")}),
		"numeric equality still matches a numerically-stored value")
}

// TestDDBQueryFilterBeginsWith proves begins_with() works as a Query
// FilterExpression (post key-condition), distinct from the KeyCondition path.
func TestDDBQueryFilterBeginsWith(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "logs", "app", "ts")

	for _, ts := range []string{"2023-12-01", "2024-01-01", "2024-02-01"} {
		suiteDDBPut(t, client, "logs", map[string]ddbtypes.AttributeValue{
			"app": sAttr("web"), "ts": sAttr(ts),
		})
	}

	// The sort key (ts) is filtered with begins_with in the FilterExpression —
	// distinct from the KeyConditionExpression, which only matches the
	// partition key here.
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String("logs"),
		KeyConditionExpression: aws.String("app = :a"),
		FilterExpression:       aws.String("begins_with(ts, :p)"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
			":a": sAttr("web"), ":p": sAttr("2024"),
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(2), out.Count, "begins_with filter keeps only the 2024 rows")
	for _, item := range out.Items {
		assert.True(t, strings.HasPrefix(attrS(t, item, "ts"), "2024"))
	}
}

// TestDDBConditionExpressionGrammar covers ConditionExpression on PutItem
// (compound attribute_not_exists + size), UpdateItem and DeleteItem
// (attribute_exists guards), asserting ConditionalCheckFailedException on
// violation and success when the condition holds.
func TestDDBConditionExpressionGrammar(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "cond2", "pk", "")

	var ccf *ddbtypes.ConditionalCheckFailedException

	// Seed an item so the guards below have something to evaluate against.
	suiteDDBPut(t, client, "cond2", map[string]ddbtypes.AttributeValue{
		"pk": sAttr("X"), "name": sAttr("hi"),
	})

	t.Run("PutItem compound condition fails on an existing item", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:                aws.String("cond2"),
			Item:                     map[string]ddbtypes.AttributeValue{"pk": sAttr("X"), "name": sAttr("new")},
			ConditionExpression:      aws.String("attribute_not_exists(pk) AND size(#n) < :m"),
			ExpressionAttributeNames: map[string]string{"#n": "name"},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":m": nAttr("10"),
			},
		})
		require.ErrorAs(t, err, &ccf, "attribute_not_exists(pk) must fail when pk exists")
	})

	t.Run("PutItem compound condition passes when it holds", func(t *testing.T) {
		_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
			TableName:                aws.String("cond2"),
			Item:                     map[string]ddbtypes.AttributeValue{"pk": sAttr("X"), "name": sAttr("hey")},
			ConditionExpression:      aws.String("attribute_exists(pk) AND size(#n) < :m"),
			ExpressionAttributeNames: map[string]string{"#n": "name"},
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":m": nAttr("10"),
			},
		})
		require.NoError(t, err, "attribute_exists(pk) AND size(name) < 10 holds")

		got := suiteDDBGet(t, client, "cond2", map[string]ddbtypes.AttributeValue{"pk": sAttr("X")})
		require.NotNil(t, got.Item)
		assert.Equal(t, "hey", attrS(t, got.Item, "name"))
	})

	t.Run("UpdateItem guarded by attribute_exists", func(t *testing.T) {
		_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String("cond2"),
			Key:                       map[string]ddbtypes.AttributeValue{"pk": sAttr("X")},
			UpdateExpression:          aws.String("SET v = :v"),
			ConditionExpression:       aws.String("attribute_exists(pk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": nAttr("1")},
		})
		require.NoError(t, err, "guard holds on the existing item")

		_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String("cond2"),
			Key:                       map[string]ddbtypes.AttributeValue{"pk": sAttr("missing")},
			UpdateExpression:          aws.String("SET v = :v"),
			ConditionExpression:       aws.String("attribute_exists(pk)"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": nAttr("1")},
		})
		require.ErrorAs(t, err, &ccf, "attribute_exists guard fails on a missing item")
	})

	t.Run("DeleteItem guarded by attribute_exists", func(t *testing.T) {
		_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:           aws.String("cond2"),
			Key:                 map[string]ddbtypes.AttributeValue{"pk": sAttr("missing")},
			ConditionExpression: aws.String("attribute_exists(pk)"),
		})
		require.ErrorAs(t, err, &ccf, "delete guard fails on a missing item")

		_, err = client.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName:           aws.String("cond2"),
			Key:                 map[string]ddbtypes.AttributeValue{"pk": sAttr("X")},
			ConditionExpression: aws.String("attribute_exists(pk)"),
		})
		require.NoError(t, err, "delete guard holds on the existing item")

		got := suiteDDBGet(t, client, "cond2", map[string]ddbtypes.AttributeValue{"pk": sAttr("X")})
		assert.Nil(t, got.Item)
	})
}

// TestDDBProjectionExpression covers ProjectionExpression on GetItem, Query
// and Scan: only the requested paths are returned, nested map sub-paths keep
// their structure, #aliases resolve, and a projected-but-absent path is
// omitted.
func TestDDBProjectionExpression(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "profiles", "id", "")

	suiteDDBPut(t, client, "profiles", map[string]ddbtypes.AttributeValue{
		"id":   sAttr("u1"),
		"name": sAttr("alice"),
		"age":  nAttr("30"),
		"tags": &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{
			sAttr("x"), sAttr("y"), sAttr("z"),
		}},
		"address": &ddbtypes.AttributeValueMemberM{Value: map[string]ddbtypes.AttributeValue{
			"city": sAttr("Paris"),
			"zip":  sAttr("75001"),
		}},
	})

	t.Run("GetItem projects a subset via #alias", func(t *testing.T) {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:                aws.String("profiles"),
			Key:                      map[string]ddbtypes.AttributeValue{"id": sAttr("u1")},
			ProjectionExpression:     aws.String("id, #n"),
			ExpressionAttributeNames: map[string]string{"#n": "name"},
		})
		require.NoError(t, err)
		require.Len(t, out.Item, 2, "only id and name are returned")
		assert.Equal(t, "u1", attrS(t, out.Item, "id"))
		assert.Equal(t, "alice", attrS(t, out.Item, "name"))
	})

	t.Run("GetItem projects a nested map path", func(t *testing.T) {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:            aws.String("profiles"),
			Key:                  map[string]ddbtypes.AttributeValue{"id": sAttr("u1")},
			ProjectionExpression: aws.String("address.city"),
		})
		require.NoError(t, err)
		require.Len(t, out.Item, 1)

		m, ok := out.Item["address"].(*ddbtypes.AttributeValueMemberM)
		require.True(t, ok, "address should be a map, got %T", out.Item["address"])
		require.Len(t, m.Value, 1, "only the projected sub-path survives, zip is dropped")
		assert.Equal(t, "Paris", attrS(t, m.Value, "city"))
	})

	t.Run("GetItem omits an absent projected path", func(t *testing.T) {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:            aws.String("profiles"),
			Key:                  map[string]ddbtypes.AttributeValue{"id": sAttr("u1")},
			ProjectionExpression: aws.String("id, missing"),
		})
		require.NoError(t, err)
		require.Len(t, out.Item, 1, "the absent attribute is silently omitted")
		assert.Equal(t, "u1", attrS(t, out.Item, "id"))
	})

	t.Run("Query projects each item", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("profiles"),
			KeyConditionExpression:    aws.String("id = :i"),
			ProjectionExpression:      aws.String("age"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":i": sAttr("u1")},
		})
		require.NoError(t, err)
		require.Equal(t, int32(1), out.Count)
		require.Len(t, out.Items[0], 1)
		assert.Equal(t, "30", attrN(t, out.Items[0], "age"))
	})

	t.Run("Scan projects each item", func(t *testing.T) {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:            aws.String("profiles"),
			ProjectionExpression: aws.String("id, age"),
		})
		require.NoError(t, err)
		require.Equal(t, int32(1), out.Count)
		require.Len(t, out.Items[0], 2)
		assert.Equal(t, "u1", attrS(t, out.Items[0], "id"))
		assert.Equal(t, "30", attrN(t, out.Items[0], "age"))
	})

	t.Run("GetItem projects a list index, compacted", func(t *testing.T) {
		out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:            aws.String("profiles"),
			Key:                  map[string]ddbtypes.AttributeValue{"id": sAttr("u1")},
			ProjectionExpression: aws.String("tags[1]"),
		})
		require.NoError(t, err)
		require.Len(t, out.Item, 1)

		l, ok := out.Item["tags"].(*ddbtypes.AttributeValueMemberL)
		require.True(t, ok, "tags should be a list, got %T", out.Item["tags"])
		require.Len(t, l.Value, 1, "the projected index compacts to a one-element list")
		assert.Equal(t, "y", attrS(t, map[string]ddbtypes.AttributeValue{"v": l.Value[0]}, "v"))
	})

	t.Run("Scan composes ProjectionExpression with FilterExpression", func(t *testing.T) {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String("profiles"),
			FilterExpression:          aws.String("age = :a"),
			ProjectionExpression:      aws.String("id"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":a": nAttr("30")},
		})
		require.NoError(t, err)
		require.Equal(t, int32(1), out.Count, "the filter keeps the matching row")
		require.Len(t, out.Items[0], 1, "projection trims it to id only")
		assert.Equal(t, "u1", attrS(t, out.Items[0], "id"))

		out, err = client.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 aws.String("profiles"),
			FilterExpression:          aws.String("age = :a"),
			ProjectionExpression:      aws.String("id"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":a": nAttr("99")},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(0), out.Count, "a non-matching filter yields nothing to project")
	})

	t.Run("overlapping projected paths are rejected", func(t *testing.T) {
		_, err := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName:            aws.String("profiles"),
			Key:                  map[string]ddbtypes.AttributeValue{"id": sAttr("u1")},
			ProjectionExpression: aws.String("address, address.city"),
		})
		require.Error(t, err, "overlapping document paths must be a ValidationException")
	})
}

// TestDDBUpdateExpressionGrammar drives the real SDK through the rich
// UpdateExpression grammar: SET arithmetic, if_not_exists and list_append, ADD
// on a number and on a set (union), and DELETE on a set (difference, then
// emptying removes the attribute). Results are asserted via ReturnValues=ALL_NEW.
func TestDDBUpdateExpressionGrammar(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "acct", "id", "")
	suiteDDBPut(t, client, "acct", map[string]ddbtypes.AttributeValue{
		"id":      sAttr("a1"),
		"balance": nAttr("100"),
		"items":   &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{sAttr("x")}},
		"tags":    &ddbtypes.AttributeValueMemberSS{Value: []string{"red", "blue"}},
	})

	update := func(exprStr string, vals map[string]ddbtypes.AttributeValue) map[string]ddbtypes.AttributeValue {
		out, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:                 aws.String("acct"),
			Key:                       map[string]ddbtypes.AttributeValue{"id": sAttr("a1")},
			UpdateExpression:          aws.String(exprStr),
			ExpressionAttributeValues: vals,
			ReturnValues:              ddbtypes.ReturnValueAllNew,
		})
		require.NoError(t, err, "UpdateItem %q", exprStr)

		return out.Attributes
	}

	t.Run("SET arithmetic, if_not_exists, list_append and ADD number", func(t *testing.T) {
		attrs := update(
			"SET balance = balance + :d, nickname = if_not_exists(nickname, :nn), "+
				"items = list_append(items, :more) ADD visits :one",
			map[string]ddbtypes.AttributeValue{
				":d":    nAttr("50"),
				":nn":   sAttr("ace"),
				":more": &ddbtypes.AttributeValueMemberL{Value: []ddbtypes.AttributeValue{sAttr("y")}},
				":one":  nAttr("1"),
			})

		assert.Equal(t, "150", attrN(t, attrs, "balance"), "balance + 50")
		assert.Equal(t, "ace", attrS(t, attrs, "nickname"), "if_not_exists set the default")
		assert.Equal(t, "1", attrN(t, attrs, "visits"), "ADD created and incremented from zero")

		l, ok := attrs["items"].(*ddbtypes.AttributeValueMemberL)
		require.True(t, ok)
		require.Len(t, l.Value, 2, "list_append grew the list")
	})

	t.Run("ADD unions a string set", func(t *testing.T) {
		attrs := update("ADD tags :t", map[string]ddbtypes.AttributeValue{
			":t": &ddbtypes.AttributeValueMemberSS{Value: []string{"green", "blue"}},
		})

		ss, ok := attrs["tags"].(*ddbtypes.AttributeValueMemberSS)
		require.True(t, ok, "tags should be a string set, got %T", attrs["tags"])
		assert.ElementsMatch(t, []string{"red", "blue", "green"}, ss.Value, "union, deduplicated")
	})

	t.Run("DELETE removes set members then empties the attribute", func(t *testing.T) {
		attrs := update("DELETE tags :t", map[string]ddbtypes.AttributeValue{
			":t": &ddbtypes.AttributeValueMemberSS{Value: []string{"red"}},
		})
		ss, ok := attrs["tags"].(*ddbtypes.AttributeValueMemberSS)
		require.True(t, ok)
		assert.ElementsMatch(t, []string{"blue", "green"}, ss.Value)

		attrs = update("DELETE tags :t", map[string]ddbtypes.AttributeValue{
			":t": &ddbtypes.AttributeValueMemberSS{Value: []string{"blue", "green"}},
		})
		_, has := attrs["tags"]
		assert.False(t, has, "an emptied set attribute is removed")
	})

	t.Run("ADD with a mismatched type is rejected", func(t *testing.T) {
		_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:        aws.String("acct"),
			Key:              map[string]ddbtypes.AttributeValue{"id": sAttr("a1")},
			UpdateExpression: aws.String("ADD balance :s"), // balance is a number
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":s": &ddbtypes.AttributeValueMemberSS{Value: []string{"x"}},
			},
		})
		require.Error(t, err, "ADD a set to a numeric attribute must be a ValidationException")
	})
}

// TestDDBSetTypesRoundTrip proves the wire codec decodes and re-encodes all
// three DynamoDB set types (SS/NS/BS) plus the binary scalar (B) through the
// real SDK.
func TestDDBSetTypesRoundTrip(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)

	suiteDDBCreateTable(t, client, "sets", "id", "")
	suiteDDBPut(t, client, "sets", map[string]ddbtypes.AttributeValue{
		"id":  sAttr("s1"),
		"ss":  &ddbtypes.AttributeValueMemberSS{Value: []string{"a", "b"}},
		"ns":  &ddbtypes.AttributeValueMemberNS{Value: []string{"1", "2"}},
		"bs":  &ddbtypes.AttributeValueMemberBS{Value: [][]byte{{0x01, 0x02}, {0x03}}},
		"bin": &ddbtypes.AttributeValueMemberB{Value: []byte{0xDE, 0xAD}},
	})

	out := suiteDDBGet(t, client, "sets", map[string]ddbtypes.AttributeValue{"id": sAttr("s1")})
	require.NotNil(t, out.Item)

	ss, ok := out.Item["ss"].(*ddbtypes.AttributeValueMemberSS)
	require.True(t, ok, "ss should round-trip as SS, got %T", out.Item["ss"])
	assert.ElementsMatch(t, []string{"a", "b"}, ss.Value)

	ns, ok := out.Item["ns"].(*ddbtypes.AttributeValueMemberNS)
	require.True(t, ok, "ns should round-trip as NS, got %T", out.Item["ns"])
	assert.ElementsMatch(t, []string{"1", "2"}, ns.Value)

	bs, ok := out.Item["bs"].(*ddbtypes.AttributeValueMemberBS)
	require.True(t, ok, "bs should round-trip as BS, got %T", out.Item["bs"])
	require.Len(t, bs.Value, 2)

	bin, ok := out.Item["bin"].(*ddbtypes.AttributeValueMemberB)
	require.True(t, ok, "binary scalar should round-trip as B, got %T", out.Item["bin"])
	assert.Equal(t, []byte{0xDE, 0xAD}, bin.Value)
}
