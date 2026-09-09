// update_table_attribute_defs_sdk_test.go — real aws-sdk-go-v2 coverage of the
// attribute-definition reconciliation an UpdateTable must perform when it adds
// or removes a GSI. Real DynamoDB keeps AttributeDefinitions equal to exactly
// the attributes the table key and its surviving indexes reference: adding a GSI
// grows the set with the new index's key attribute, deleting one prunes any
// attribute no remaining index uses. Without it DescribeTable omits a newly
// indexed attribute (or keeps a stale one) and an IaC client sees a perpetual
// diff.
package dynamodb_test

import (
	"context"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func attrNames(defs []ddbtypes.AttributeDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, aws.ToString(d.AttributeName))
	}

	sort.Strings(names)

	return names
}

func TestUpdateTableAddGSIMergesAttributeDefinitions(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("orders"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("OrderId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("OrderId"), KeyType: ddbtypes.KeyTypeHash},
		},
	})
	require.NoError(t, err)

	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName: aws.String("orders"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("CustomerId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{
			{Create: &ddbtypes.CreateGlobalSecondaryIndexAction{
				IndexName: aws.String("CustomerIndex"),
				KeySchema: []ddbtypes.KeySchemaElement{
					{AttributeName: aws.String("CustomerId"), KeyType: ddbtypes.KeyTypeHash},
				},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeAll},
			}},
		},
	})
	require.NoError(t, err)

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("orders")})
	require.NoError(t, err)
	// The newly indexed attribute must now be part of AttributeDefinitions, with
	// its declared type preserved.
	assert.Equal(t, []string{"CustomerId", "OrderId"}, attrNames(desc.Table.AttributeDefinitions))

	for _, d := range desc.Table.AttributeDefinitions {
		assert.Equal(t, ddbtypes.ScalarAttributeTypeS, d.AttributeType,
			"attribute %s type", aws.ToString(d.AttributeName))
	}
}

func TestUpdateTableDeleteGSIPrunesAttributeDefinitions(t *testing.T) {
	t.Parallel()

	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String("orders2"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("OrderId"), AttributeType: ddbtypes.ScalarAttributeTypeS},
			{AttributeName: aws.String("Status"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("OrderId"), KeyType: ddbtypes.KeyTypeHash},
		},
		GlobalSecondaryIndexes: []ddbtypes.GlobalSecondaryIndex{
			{
				IndexName: aws.String("StatusIndex"),
				KeySchema: []ddbtypes.KeySchemaElement{
					{AttributeName: aws.String("Status"), KeyType: ddbtypes.KeyTypeHash},
				},
				Projection: &ddbtypes.Projection{ProjectionType: ddbtypes.ProjectionTypeKeysOnly},
			},
		},
	})
	require.NoError(t, err)

	_, err = client.UpdateTable(ctx, &dynamodb.UpdateTableInput{
		TableName: aws.String("orders2"),
		GlobalSecondaryIndexUpdates: []ddbtypes.GlobalSecondaryIndexUpdate{
			{Delete: &ddbtypes.DeleteGlobalSecondaryIndexAction{IndexName: aws.String("StatusIndex")}},
		},
	})
	require.NoError(t, err)

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("orders2")})
	require.NoError(t, err)
	// Status was only referenced by the deleted index, so it must be pruned.
	assert.Equal(t, []string{"OrderId"}, attrNames(desc.Table.AttributeDefinitions))
}
