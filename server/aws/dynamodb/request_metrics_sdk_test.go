package dynamodb_test

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mondriver "github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// TestDDBRequestMetricsPerWireOperation pins that every wire operation is
// metered once per request (per table) under its own Operation name
// (BatchWriteItem, BatchGetItem, TransactWriteItems, TransactGetItems) rather
// than as the per-item PutItem/GetItem calls it fans out into, and that a
// GetItem miss (HTTP 200, no Item) is still a metered successful request.
func TestDDBRequestMetricsPerWireOperation(t *testing.T) {
	client, provider := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "rm", "pk", "")

	puts := make([]ddbtypes.WriteRequest, 0, 3)
	for _, k := range []string{"a", "b", "c"} {
		puts = append(puts, ddbtypes.WriteRequest{PutRequest: &ddbtypes.PutRequest{
			Item: map[string]ddbtypes.AttributeValue{"pk": sAttr(k)},
		}})
	}

	_, err := client.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]ddbtypes.WriteRequest{"rm": puts},
	})
	require.NoError(t, err)

	_, err = client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
		RequestItems: map[string]ddbtypes.KeysAndAttributes{"rm": {Keys: []map[string]ddbtypes.AttributeValue{
			{"pk": sAttr("a")}, {"pk": sAttr("b")},
		}}},
	})
	require.NoError(t, err)

	_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{TableName: aws.String("rm"), Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("t1")}}},
			{Put: &ddbtypes.Put{TableName: aws.String("rm"), Item: map[string]ddbtypes.AttributeValue{"pk": sAttr("t2")}}},
		},
	})
	require.NoError(t, err)

	_, err = client.TransactGetItems(ctx, &dynamodb.TransactGetItemsInput{
		TransactItems: []ddbtypes.TransactGetItem{
			{Get: &ddbtypes.Get{TableName: aws.String("rm"), Key: map[string]ddbtypes.AttributeValue{"pk": sAttr("a")}}},
			{Get: &ddbtypes.Get{TableName: aws.String("rm"), Key: map[string]ddbtypes.AttributeValue{"pk": sAttr("t1")}}},
		},
	})
	require.NoError(t, err)

	miss, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("rm"), Key: map[string]ddbtypes.AttributeValue{"pk": sAttr("absent")},
	})
	require.NoError(t, err)
	assert.Empty(t, miss.Item)

	now := time.Now()
	samples := func(op string) float64 {
		t.Helper()

		res, gerr := provider.CloudWatch.GetMetricData(ctx, mondriver.GetMetricInput{
			Namespace: "AWS/DynamoDB", MetricName: "SuccessfulRequestLatency",
			Dimensions: map[string]string{"TableName": "rm", "Operation": op},
			StartTime:  now.Add(-time.Hour), EndTime: now.Add(time.Hour),
			Period: 7200, Stat: "SampleCount",
		})
		require.NoError(t, gerr)

		if len(res.Values) == 0 {
			return 0
		}

		assert.Equal(t, "Milliseconds", res.Unit)

		return res.Values[0]
	}

	for op, want := range map[string]float64{
		"BatchWriteItem":     1,
		"BatchGetItem":       1,
		"TransactWriteItems": 1,
		"TransactGetItems":   1,
		"GetItem":            1, // the miss
		"PutItem":            0, // batch items are not separate PutItem requests
	} {
		assert.InDelta(t, want, samples(op), 0, "SuccessfulRequestLatency{%s} SampleCount", op)
	}

	wcu, err := provider.CloudWatch.GetMetricData(ctx, mondriver.GetMetricInput{
		Namespace: "AWS/DynamoDB", MetricName: "ConsumedWriteCapacityUnits",
		Dimensions: map[string]string{"TableName": "rm"},
		StartTime:  now.Add(-time.Hour), EndTime: now.Add(time.Hour),
		Period: 7200, Stat: "Sum",
	})
	require.NoError(t, err)
	require.Len(t, wcu.Values, 1)
	// 3 batch puts at 1 WCU each + 2 transactional puts at 2 WCU each.
	assert.InDelta(t, 7, wcu.Values[0], 0)
}
