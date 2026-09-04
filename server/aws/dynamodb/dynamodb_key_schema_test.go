// dynamodb_key_schema_test.go — real aws-sdk-go-v2 round-trips proving that
// GetItem/DeleteItem/UpdateItem/BatchGetItem/TransactGetItems reject a Key
// parameter that doesn't name exactly the table's key schema (missing the
// sort key, or an extra unrecognized attribute), on both a hash-only and a
// composite (hash+range) table — and that the exact correct key still
// succeeds, and that Query/Scan (which take no standalone Key map) are
// unaffected by this validation.
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

// keySchemaOp is one of the five operations that take a standalone Key
// parameter (as opposed to Query/Scan, which resolve keys from a
// KeyConditionExpression or not at all).
type keySchemaOp struct {
	name string
	run  func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error
}

// allKeySchemaOps returns the five operations under test, freshly built per
// call so none of them is a mutable package-level global.
func allKeySchemaOps() []keySchemaOp {
	return []keySchemaOp{
		{
			name: "GetItem",
			run: func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error {
				_, err := client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(table), Key: key})
				return err
			},
		},
		{
			name: "DeleteItem",
			run: func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error {
				_, err := client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(table), Key: key})
				return err
			},
		},
		{
			name: "UpdateItem",
			run: func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error {
				_, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
					TableName:                 aws.String(table),
					Key:                       key,
					UpdateExpression:          aws.String("SET extra = :v"),
					ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":v": sAttr("x")},
				})
				return err
			},
		},
		{
			name: "BatchGetItem",
			run: func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error {
				_, err := client.BatchGetItem(ctx, &dynamodb.BatchGetItemInput{
					RequestItems: map[string]ddbtypes.KeysAndAttributes{
						table: {Keys: []map[string]ddbtypes.AttributeValue{key}},
					},
				})
				return err
			},
		},
		{
			name: "TransactGetItems",
			run: func(ctx context.Context, client *dynamodb.Client, table string, key map[string]ddbtypes.AttributeValue) error {
				_, err := client.TransactGetItems(ctx, &dynamodb.TransactGetItemsInput{
					TransactItems: []ddbtypes.TransactGetItem{{Get: &ddbtypes.Get{TableName: aws.String(table), Key: key}}},
				})
				return err
			},
		},
	}
}

// TestDDBKeySchemaValidation drives every keySchemaOp against a hash-only and
// a composite table with an exact-correct key (must succeed), a key missing
// the sort key (composite only; must be rejected), and a key carrying an
// extra unrecognized attribute (both table kinds; must be rejected).
func TestDDBKeySchemaValidation(t *testing.T) {
	type tableKind struct {
		name   string
		pk, sk string
	}

	tableKinds := []tableKind{
		{name: "hash-only", pk: "pk", sk: ""},
		{name: "composite", pk: "pk", sk: "sk"},
	}

	type variant struct {
		name          string
		compositeOnly bool
		key           func(pk, sk string) map[string]ddbtypes.AttributeValue
		wantErr       bool
	}

	variants := []variant{
		{
			name: "exact key succeeds",
			key: func(pk, sk string) map[string]ddbtypes.AttributeValue {
				k := map[string]ddbtypes.AttributeValue{pk: sAttr("k1")}
				if sk != "" {
					k[sk] = sAttr("s1")
				}

				return k
			},
			wantErr: false,
		},
		{
			name:          "missing range key rejected",
			compositeOnly: true,
			key: func(pk, _ string) map[string]ddbtypes.AttributeValue {
				return map[string]ddbtypes.AttributeValue{pk: sAttr("k1")}
			},
			wantErr: true,
		},
		{
			name: "extra attribute rejected",
			key: func(pk, sk string) map[string]ddbtypes.AttributeValue {
				k := map[string]ddbtypes.AttributeValue{pk: sAttr("k1"), "bogus": sAttr("x")}
				if sk != "" {
					k[sk] = sAttr("s1")
				}

				return k
			},
			wantErr: true,
		},
	}

	idx := 0

	for _, tk := range tableKinds {
		for _, v := range variants {
			if v.compositeOnly && tk.sk == "" {
				continue
			}

			for _, op := range allKeySchemaOps() {
				idx++

				t.Run(tk.name+"/"+v.name+"/"+op.name, func(t *testing.T) {
					client, _ := newSuiteDDBEnv(t)
					ctx := context.Background()
					table := fmt.Sprintf("kst%d", idx)

					suiteDDBCreateTable(t, client, table, tk.pk, tk.sk)

					item := map[string]ddbtypes.AttributeValue{tk.pk: sAttr("k1")}
					if tk.sk != "" {
						item[tk.sk] = sAttr("s1")
					}

					suiteDDBPut(t, client, table, item)

					err := op.run(ctx, client, table, v.key(tk.pk, tk.sk))

					if v.wantErr {
						require.Error(t, err)
						assert.Equal(t, "ValidationException", apiErrorCode(t, err))
						assert.Contains(t, err.Error(), "does not match the schema")
					} else {
						assert.NoError(t, err)
					}
				})
			}
		}
	}
}

// TestDDBKeySchemaValidationDoesNotAffectQueryOrScan guards against the
// GetItem/DeleteItem/UpdateItem/BatchGetItem/TransactGetItems key-schema
// validation leaking into Query/Scan, which resolve keys from a
// KeyConditionExpression (or take no key at all) rather than a standalone Key
// map, and so must never be key-schema validated the same way.
func TestDDBKeySchemaValidationDoesNotAffectQueryOrScan(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "kst_qs", "pk", "sk")
	suiteDDBPut(t, client, "kst_qs", map[string]ddbtypes.AttributeValue{"pk": sAttr("a"), "sk": sAttr("b")})

	_, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String("kst_qs"),
		KeyConditionExpression:    aws.String("pk = :p"),
		ExpressionAttributeValues: map[string]ddbtypes.AttributeValue{":p": sAttr("a")},
	})
	require.NoError(t, err)

	_, err = client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("kst_qs")})
	require.NoError(t, err)
}
