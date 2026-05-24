// Real-SDK round-trip test: the live aws-sdk-go-v2 Resource Groups Tagging
// API client drives the in-memory handler end-to-end.

package resourcegroupstaggingapi_test

import (
	"context"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	rgta "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgtatypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	awsserver "github.com/stackshy/cloudemu/server/aws"
)

func TestSDKResourceGroupsTagging(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	// Seed a small inventory across services.
	require.NoError(t, cloud.S3.CreateBucket(ctx, "audit-logs"))
	require.NoError(t, cloud.S3.PutBucketTagging(ctx, "audit-logs",
		map[string]string{"env": "prod", "team": "security"}))

	require.NoError(t, cloud.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{Name: "events", PartitionKey: "pk"}))
	require.NoError(t, cloud.DynamoDB.TagResource(ctx, "events", map[string]string{"env": "prod"}))

	vpcInfo, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "stage"},
	})
	require.NoError(t, err)

	srv := awsserver.New(awsserver.Drivers{
		S3:                cloud.S3,
		DynamoDB:          cloud.DynamoDB,
		VPC:               cloud.VPC,
		ResourceDiscovery: cloud.ResourceDiscovery,
		AccountID:         "123456789012",
		Region:            "us-east-1",
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newRGTAClient(t, ts.URL)

	t.Run("GetResources returns everything", func(t *testing.T) {
		out, err := client.GetResources(ctx, &rgta.GetResourcesInput{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.ResourceTagMappingList), 3,
			"expect bucket + table + vpc at minimum")

		// Spot-check that ARNs and tags are present.
		seenBucket := false
		for _, m := range out.ResourceTagMappingList {
			if aws.ToString(m.ResourceARN) == "arn:aws:s3:::audit-logs" {
				seenBucket = true
				tags := tagsToMap(m.Tags)
				assert.Equal(t, "prod", tags["env"])
				assert.Equal(t, "security", tags["team"])
			}
		}
		assert.True(t, seenBucket, "audit-logs bucket should appear in inventory")
	})

	t.Run("GetResources with TagFilters", func(t *testing.T) {
		out, err := client.GetResources(ctx, &rgta.GetResourcesInput{
			TagFilters: []rgtatypes.TagFilter{{
				Key:    aws.String("env"),
				Values: []string{"prod"},
			}},
		})
		require.NoError(t, err)
		// audit-logs (s3) + events (dynamodb) both have env=prod; vpc has env=stage.
		assert.Len(t, out.ResourceTagMappingList, 2)
	})

	t.Run("GetTagKeys returns deduplicated keys", func(t *testing.T) {
		out, err := client.GetTagKeys(ctx, &rgta.GetTagKeysInput{})
		require.NoError(t, err)
		sort.Strings(out.TagKeys)
		assert.Equal(t, []string{"env", "team"}, out.TagKeys)
	})

	t.Run("GetTagValues for a key", func(t *testing.T) {
		out, err := client.GetTagValues(ctx, &rgta.GetTagValuesInput{Key: aws.String("env")})
		require.NoError(t, err)
		sort.Strings(out.TagValues)
		assert.Equal(t, []string{"prod", "stage"}, out.TagValues)
	})

	t.Run("TagResources adds tags and is visible in subsequent reads", func(t *testing.T) {
		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{"arn:aws:s3:::audit-logs"},
			Tags:            map[string]string{"compliance": "soc2"},
		})
		require.NoError(t, err)
		assert.Empty(t, out.FailedResourcesMap)

		got, err := cloud.S3.GetBucketTagging(ctx, "audit-logs")
		require.NoError(t, err)
		assert.Equal(t, "soc2", got["compliance"])
		assert.Equal(t, "prod", got["env"], "existing tags must survive the merge")
	})

	t.Run("UntagResources removes the listed keys", func(t *testing.T) {
		arn := "arn:aws:ec2:us-east-1:123456789012:vpc/" + vpcInfo.ID
		out, err := client.UntagResources(ctx, &rgta.UntagResourcesInput{
			ResourceARNList: []string{arn},
			TagKeys:         []string{"env"},
		})
		require.NoError(t, err)
		assert.Empty(t, out.FailedResourcesMap)

		got, err := cloud.VPC.DescribeVPCs(ctx, []string{vpcInfo.ID})
		require.NoError(t, err)
		_, has := got[0].Tags["env"]
		assert.False(t, has, "env tag should be gone after UntagResources")
	})

	t.Run("TagResources reports failures for unsupported ARNs", func(t *testing.T) {
		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{"arn:aws:lambda:us-east-1:111:function:nope"},
			Tags:            map[string]string{"k": "v"},
		})
		require.NoError(t, err)
		assert.Len(t, out.FailedResourcesMap, 1)
	})
}

func newRGTAClient(t *testing.T, baseURL string) *rgta.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
	)
	if err != nil {
		t.Fatalf("awsconfig: %v", err)
	}

	return rgta.NewFromConfig(cfg, func(o *rgta.Options) {
		o.BaseEndpoint = aws.String(baseURL)
	})
}

func tagsToMap(tags []rgtatypes.Tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}
