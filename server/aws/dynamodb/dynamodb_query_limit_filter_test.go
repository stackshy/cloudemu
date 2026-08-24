package dynamodb_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLimitFilterTable creates a single-partition table "lf" with 10 items
// (sk "0".."9", attribute n = index) for the Limit+FilterExpression cases.
func seedLimitFilterTable(t *testing.T, client *dynamodb.Client) {
	t.Helper()

	suiteDDBCreateTable(t, client, "lf", "pk", "sk")
	for i := range 10 {
		suiteDDBPut(t, client, "lf", map[string]ddbtypes.AttributeValue{
			"pk": sAttr("p"),
			"sk": sAttr(fmt.Sprintf("%d", i)),
			"n":  nAttr(fmt.Sprintf("%d", i)),
		})
	}
}

// TestDDBQueryLimitFilterInteraction covers the finding: Limit is the maximum
// number of items to EVALUATE, and the FilterExpression is applied AFTER
// reading that page. So ScannedCount is capped at Limit, Count counts matches
// only among those first Limit evaluated items, and LastEvaluatedKey is
// returned whenever Limit items were evaluated and more remain — including the
// documented case where a page returns an empty result set plus a
// LastEvaluatedKey because every item read was filtered out.
func TestDDBQueryLimitFilterInteraction(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()
	seedLimitFilterTable(t, client)

	keyCond := aws.String("pk = :p")

	t.Run("no filter, Limit=3", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:                 aws.String("lf"),
			KeyConditionExpression:    keyCond,
			Limit:                     aws.Int32(3),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":p": sAttr("p")},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(3), out.Count)
		assert.Equal(t, int32(3), out.ScannedCount, "Limit caps items evaluated")
		assert.NotNil(t, out.LastEvaluatedKey, "more items remain after the page")
	})

	t.Run("filter n<2, Limit=3 keeps 2 of first 3 evaluated", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String("lf"),
			KeyConditionExpression: keyCond,
			FilterExpression:       aws.String("n < :two"),
			Limit:                  aws.Int32(3),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":p": sAttr("p"), ":two": nAttr("2"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), out.Count, "sk 0,1 match among first 3 evaluated")
		assert.Equal(t, int32(3), out.ScannedCount, "3 items evaluated, not the full 10")
		assert.NotNil(t, out.LastEvaluatedKey, "3 evaluated and 7 remain -> continuation")
	})

	t.Run("filter n=9 only, Limit=3 -> empty page + LastEvaluatedKey", func(t *testing.T) {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String("lf"),
			KeyConditionExpression: keyCond,
			FilterExpression:       aws.String("n = :nine"),
			Limit:                  aws.Int32(3),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":p": sAttr("p"), ":nine": nAttr("9"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(0), out.Count, "sk 9 is never reached in the first 3 evaluated")
		assert.Equal(t, int32(3), out.ScannedCount)
		assert.Empty(t, out.Items)
		assert.NotNil(t, out.LastEvaluatedKey,
			"empty page still returns a continuation key when items remain")
	})
}

// TestDDBQueryLimitFilterPagination walks the whole table via LastEvaluatedKey
// with a Limit smaller than the partition, asserting the emulator never skips a
// matching item that a real Limit-bounded page would only reach on a later
// page. Filter n is even -> matches sk 0,2,4,6,8 across the pages.
func TestDDBQueryLimitFilterPagination(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()
	seedLimitFilterTable(t, client)

	var (
		startKey   map[string]ddbtypes.AttributeValue
		gotMatches int
		pages      int
		scanned    int32
	)

	for {
		out, err := client.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String("lf"),
			KeyConditionExpression: aws.String("pk = :p"),
			FilterExpression:       aws.String("n IN (:a, :b, :c, :d, :e)"),
			Limit:                  aws.Int32(3),
			ExclusiveStartKey:      startKey,
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":p": sAttr("p"),
				":a": nAttr("0"), ":b": nAttr("2"), ":c": nAttr("4"),
				":d": nAttr("6"), ":e": nAttr("8"),
			},
		})
		require.NoError(t, err)

		gotMatches += int(out.Count)
		scanned += out.ScannedCount
		pages++
		require.LessOrEqual(t, pages, 10, "paging must terminate")

		if out.LastEvaluatedKey == nil {
			break
		}
		startKey = out.LastEvaluatedKey
	}

	assert.Equal(t, 5, gotMatches, "all five even-n items visited across pages")
	assert.Equal(t, int32(10), scanned, "every one of the 10 items is evaluated exactly once")
}

// TestDDBScanLimitFilterInteraction mirrors the Query case for Scan: Limit
// bounds the number of items examined, the FilterExpression runs on that page,
// and a fully filtered-out page still reports ScannedCount and a
// LastEvaluatedKey.
func TestDDBScanLimitFilterInteraction(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()
	seedLimitFilterTable(t, client)

	t.Run("filter n=9 only, Limit=3 -> empty page + LastEvaluatedKey", func(t *testing.T) {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String("lf"),
			FilterExpression: aws.String("n = :nine"),
			Limit:            aws.Int32(3),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":nine": nAttr("9"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(0), out.Count)
		assert.Equal(t, int32(3), out.ScannedCount, "3 items examined before filtering")
		assert.NotNil(t, out.LastEvaluatedKey, "empty filtered page still paginates")
	})

	t.Run("filter n<2, Limit=3", func(t *testing.T) {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:        aws.String("lf"),
			FilterExpression: aws.String("n < :two"),
			Limit:            aws.Int32(3),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":two": nAttr("2"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, int32(2), out.Count)
		assert.Equal(t, int32(3), out.ScannedCount)
		assert.NotNil(t, out.LastEvaluatedKey)
	})
}
