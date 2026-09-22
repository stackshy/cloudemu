package dynamodb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	smithy "github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"
)

// TestQueryScanUnknownIndexIsValidationException covers that a Query or Scan
// naming an index the table doesn't have answers ValidationException (a bad
// request), not ResourceNotFoundException — while a missing table named with an
// IndexName is still ResourceNotFoundException.
func TestQueryScanUnknownIndexIsValidationException(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	_, err := client.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("idx"),
		AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: types.ScalarAttributeTypeS}},
		KeySchema:            []types.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: types.KeyTypeHash}},
		BillingMode:          types.BillingModePayPerRequest,
	})
	require.NoError(t, err)

	requireValidation := func(op string, err error) {
		t.Helper()

		var apiErr smithy.APIError
		require.True(t, errors.As(err, &apiErr), "%s: err = %v, want an API error", op, err)
		require.Equal(t, "ValidationException", apiErr.ErrorCode(), op)
		require.Equal(t, "The table does not have the specified index: nope", apiErr.ErrorMessage(), op)
	}

	_, err = client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String("idx"),
		IndexName:                 aws.String("nope"),
		KeyConditionExpression:    aws.String("pk = :v"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":v": &types.AttributeValueMemberS{Value: "x"}},
	})
	requireValidation("Query", err)

	_, err = client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("idx"), IndexName: aws.String("nope")})
	requireValidation("Scan", err)

	_, err = client.Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("ghost"), IndexName: aws.String("nope")})

	var rnf *types.ResourceNotFoundException
	require.ErrorAs(t, err, &rnf, "Scan on missing table with IndexName")
}
