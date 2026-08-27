// list_tags_pagination_sdk_test.go — real aws-sdk-go-v2 journeys for
// ListTagsOfResource pagination: a resource's tags (well under the 50-tag cap)
// return in a single page with no NextToken, and a malformed NextToken is
// rejected with a ValidationException.
package dynamodb_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDDBListTagsSinglePageNoToken(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tagged", "pk", "")

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("tagged")})
	require.NoError(t, err)
	arn := aws.ToString(desc.Table.TableArn)

	_, err = client.TagResource(ctx, &dynamodb.TagResourceInput{
		ResourceArn: aws.String(arn),
		Tags: []ddbtypes.Tag{
			{Key: aws.String("env"), Value: aws.String("prod")},
			{Key: aws.String("team"), Value: aws.String("core")},
			{Key: aws.String("cost"), Value: aws.String("42")},
		},
	})
	require.NoError(t, err)

	out, err := client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{ResourceArn: aws.String(arn)})
	require.NoError(t, err)
	assert.Len(t, out.Tags, 3)
	assert.Nil(t, out.NextToken, "a sub-cap tag set must not emit a NextToken")

	got := map[string]string{}
	for _, tg := range out.Tags {
		got[aws.ToString(tg.Key)] = aws.ToString(tg.Value)
	}

	assert.Equal(t, map[string]string{"env": "prod", "team": "core", "cost": "42"}, got)
}

func TestDDBListTagsInvalidNextTokenRejected(t *testing.T) {
	client, _ := newSuiteDDBEnv(t)
	ctx := context.Background()

	suiteDDBCreateTable(t, client, "tagged2", "pk", "")

	desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String("tagged2")})
	require.NoError(t, err)

	_, err = client.ListTagsOfResource(ctx, &dynamodb.ListTagsOfResourceInput{
		ResourceArn: desc.Table.TableArn,
		NextToken:   aws.String("!!not-base64!!"),
	})
	require.Error(t, err)

	var apiErr smithy.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "ValidationException", apiErr.ErrorCode())
}
