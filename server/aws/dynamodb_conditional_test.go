package aws_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDDBHashTable creates a single-partition-key on-demand table for the
// conditional-write reproductions.
func createDDBHashTable(t *testing.T, client *dynamodb.Client, name string) {
	t.Helper()

	_, err := client.CreateTable(context.Background(), &dynamodb.CreateTableInput{
		TableName: aws.String(name),
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash},
		},
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		BillingMode: ddbtypes.BillingModePayPerRequest,
	})
	require.NoError(t, err)
}

func ddbStr(v string) ddbtypes.AttributeValue { return &ddbtypes.AttributeValueMemberS{Value: v} }

// TestDDBConditionalPutIfAbsentRace drives the real aws-sdk-go-v2 client: many
// concurrent PutItem(attribute_not_exists) calls race to create the same key.
// Real DynamoDB guarantees exactly one succeeds and the rest get
// ConditionalCheckFailedException — asserted across many rounds so the (logical)
// TOCTOU that let two writers both succeed cannot pass.
func TestDDBConditionalPutIfAbsentRace(t *testing.T) {
	const (
		rounds     = 40
		goroutines = 6
	)

	client := newDDBClient(t)
	createDDBHashTable(t, client, "cwrite")

	ctx := context.Background()

	for r := 0; r < rounds; r++ {
		pk := fmt.Sprintf("k-%d", r)

		var (
			winners   atomic.Int64
			condFails atomic.Int64
			wg        sync.WaitGroup
		)

		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()

				_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
					TableName:           aws.String("cwrite"),
					Item:                map[string]ddbtypes.AttributeValue{"pk": ddbStr(pk)},
					ConditionExpression: aws.String("attribute_not_exists(pk)"),
				})
				if err == nil {
					winners.Add(1)
					return
				}

				var ccf *ddbtypes.ConditionalCheckFailedException
				if errors.As(err, &ccf) {
					condFails.Add(1)
					return
				}

				t.Errorf("round %d: unexpected error: %v", r, err)
			}()
		}

		wg.Wait()

		require.Equalf(t, int64(1), winners.Load(),
			"round %d: exactly one put-if-absent must succeed", r)
		require.Equalf(t, int64(goroutines-1), condFails.Load(),
			"round %d: every other writer must get ConditionalCheckFailedException", r)
	}
}

// TestDDBConditionalOptimisticLock exercises the UpdateItem conditional path over
// the wire: a version-guarded update succeeds once, and a replay guarded on the
// now-stale version is rejected with ConditionalCheckFailedException.
func TestDDBConditionalOptimisticLock(t *testing.T) {
	client := newDDBClient(t)
	createDDBHashTable(t, client, "optlock")

	ctx := context.Background()

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("optlock"),
		Item: map[string]ddbtypes.AttributeValue{
			"pk":  ddbStr("x"),
			"ver": &ddbtypes.AttributeValueMemberN{Value: "0"},
		},
	})
	require.NoError(t, err)

	bump := func() error {
		_, uerr := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName:           aws.String("optlock"),
			Key:                 map[string]ddbtypes.AttributeValue{"pk": ddbStr("x")},
			UpdateExpression:    aws.String("SET ver = :new"),
			ConditionExpression: aws.String("ver = :cur"),
			ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{
				":cur": &ddbtypes.AttributeValueMemberN{Value: "0"},
				":new": &ddbtypes.AttributeValueMemberN{Value: "1"},
			},
		})

		return uerr
	}

	require.NoError(t, bump(), "first version-guarded update should win")

	var ccf *ddbtypes.ConditionalCheckFailedException
	require.ErrorAs(t, bump(), &ccf, "replay on stale version must fail the condition")
}

// TestDDBTransactWriteAllOrNothing drives TransactWriteItems over the wire: a
// transaction whose middle Put fails its condition is entirely canceled — the
// CancellationReasons name the failing op and NEITHER unconditional put is
// applied.
func TestDDBTransactWriteAllOrNothing(t *testing.T) {
	client := newDDBClient(t)
	createDDBHashTable(t, client, "txall")

	ctx := context.Background()

	_, err := client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String("txall"),
		Item:      map[string]ddbtypes.AttributeValue{"pk": ddbStr("exists")},
	})
	require.NoError(t, err)

	_, err = client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []ddbtypes.TransactWriteItem{
			{Put: &ddbtypes.Put{TableName: aws.String("txall"),
				Item: map[string]ddbtypes.AttributeValue{"pk": ddbStr("new1")}}},
			{Put: &ddbtypes.Put{TableName: aws.String("txall"),
				Item:                map[string]ddbtypes.AttributeValue{"pk": ddbStr("exists")},
				ConditionExpression: aws.String("attribute_not_exists(pk)")}},
			{Put: &ddbtypes.Put{TableName: aws.String("txall"),
				Item: map[string]ddbtypes.AttributeValue{"pk": ddbStr("new2")}}},
		},
	})

	var canceled *ddbtypes.TransactionCanceledException
	require.ErrorAs(t, err, &canceled)
	require.Len(t, canceled.CancellationReasons, 3)
	assert.Equal(t, "None", aws.ToString(canceled.CancellationReasons[0].Code))
	assert.Equal(t, "ConditionalCheckFailed", aws.ToString(canceled.CancellationReasons[1].Code))
	assert.Equal(t, "None", aws.ToString(canceled.CancellationReasons[2].Code))

	for _, pk := range []string{"new1", "new2"} {
		out, gerr := client.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String("txall"),
			Key:       map[string]ddbtypes.AttributeValue{"pk": ddbStr(pk)},
		})
		require.NoError(t, gerr)
		assert.Nilf(t, out.Item, "put %q applied despite cancellation — not all-or-nothing", pk)
	}
}

// TestDDBTransactWriteConcurrentConflict runs concurrent put-if-absent
// transactions on one key over the wire; exactly one commits per round.
func TestDDBTransactWriteConcurrentConflict(t *testing.T) {
	const (
		rounds     = 30
		goroutines = 6
	)

	client := newDDBClient(t)
	createDDBHashTable(t, client, "txrace")

	ctx := context.Background()

	for r := 0; r < rounds; r++ {
		pk := fmt.Sprintf("k-%d", r)

		var (
			winners atomic.Int64
			wg      sync.WaitGroup
		)

		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()

				_, err := client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
					TransactItems: []ddbtypes.TransactWriteItem{
						{Put: &ddbtypes.Put{TableName: aws.String("txrace"),
							Item:                map[string]ddbtypes.AttributeValue{"pk": ddbStr(pk)},
							ConditionExpression: aws.String("attribute_not_exists(pk)")}},
					},
				})
				if err == nil {
					winners.Add(1)
					return
				}

				var canceled *ddbtypes.TransactionCanceledException
				if !errors.As(err, &canceled) {
					t.Errorf("round %d: unexpected error: %v", r, err)
				}
			}()
		}

		wg.Wait()

		require.Equalf(t, int64(1), winners.Load(),
			"round %d: exactly one conflicting transaction must commit", r)
	}
}
