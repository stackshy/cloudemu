// Real-SDK round-trip test: the live aws-sdk-go-v2 Resource Groups Tagging
// API client drives the in-memory handler end-to-end.

package resourcegroupstaggingapi_test

import (
	"context"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	rgta "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgtatypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	crdriver "github.com/stackshy/cloudemu/v2/services/containerregistry/driver"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	mqdriver "github.com/stackshy/cloudemu/v2/services/messagequeue/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
	serverlessdriver "github.com/stackshy/cloudemu/v2/services/serverless/driver"
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

	t.Run("TagResources across compute, lambda, sns, sqs and secrets", func(t *testing.T) {
		// Seed one resource of each newly-taggable service.
		insts, err := cloud.EC2.RunInstances(ctx, computedriver.InstanceConfig{
			ImageID: "ami-1", InstanceType: "t3.micro",
		}, 1)
		require.NoError(t, err)
		instanceID := insts[0].ID

		_, err = cloud.Lambda.CreateFunction(ctx, serverlessdriver.FunctionConfig{
			Name: "proc", Runtime: "go1.x", Handler: "main", Memory: 128, Timeout: 30,
		})
		require.NoError(t, err)

		_, err = cloud.SNS.CreateTopic(ctx, notifdriver.TopicConfig{Name: "events"})
		require.NoError(t, err)

		_, err = cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "tasks"})
		require.NoError(t, err)

		_, err = cloud.SecretsManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "api-key"}, []byte("v"))
		require.NoError(t, err)

		arns := []string{
			"arn:aws:ec2:us-east-1:123456789012:instance/" + instanceID,
			"arn:aws:lambda:us-east-1:123456789012:function:proc",
			"arn:aws:sns:us-east-1:123456789012:events",
			"arn:aws:sqs:us-east-1:123456789012:tasks",
			"arn:aws:secretsmanager:us-east-1:123456789012:secret:api-key",
		}

		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: arns,
			Tags:            map[string]string{"Environment": "prod"},
		})
		require.NoError(t, err)
		assert.Empty(t, out.FailedResourcesMap, "all five resource types should tag cleanly")

		// Verify each tag landed on its backing store.
		gotInst, err := cloud.EC2.DescribeInstances(ctx, []string{instanceID}, nil)
		require.NoError(t, err)
		assert.Equal(t, "prod", gotInst[0].Tags["Environment"])

		gotFn, err := cloud.Lambda.GetFunction(ctx, "proc")
		require.NoError(t, err)
		assert.Equal(t, "prod", gotFn.Tags["Environment"])

		topics, err := cloud.SNS.ListTopics(ctx, scope.Scope{})
		require.NoError(t, err)
		snsTags := map[string]string{}
		for i := range topics {
			if topics[i].Name == "events" {
				snsTags = topics[i].Tags
			}
		}
		assert.Equal(t, "prod", snsTags["Environment"], "sns topic tag should be set")

		queues, err := cloud.SQS.ListQueues(ctx, "")
		require.NoError(t, err)
		sqsTags := map[string]string{}
		for i := range queues {
			if queues[i].Name == "tasks" {
				sqsTags = queues[i].Tags
			}
		}
		assert.Equal(t, "prod", sqsTags["Environment"], "sqs queue tag should be set")

		gotSecret, err := cloud.SecretsManager.GetSecret(ctx, "api-key")
		require.NoError(t, err)
		assert.Equal(t, "prod", gotSecret.Tags["Environment"])

		// UntagResources reaches the same stores.
		_, err = client.UntagResources(ctx, &rgta.UntagResourcesInput{
			ResourceARNList: []string{arns[0]},
			TagKeys:         []string{"Environment"},
		})
		require.NoError(t, err)
		gotInst, err = cloud.EC2.DescribeInstances(ctx, []string{instanceID}, nil)
		require.NoError(t, err)
		_, has := gotInst[0].Tags["Environment"]
		assert.False(t, has)
	})

	t.Run("TagResources reports failures for a service without a tag store", func(t *testing.T) {
		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{"arn:aws:kms:us-east-1:123456789012:key/abc"},
			Tags:            map[string]string{"k": "v"},
		})
		require.NoError(t, err)
		require.Len(t, out.FailedResourcesMap, 1)
		fail := out.FailedResourcesMap["arn:aws:kms:us-east-1:123456789012:key/abc"]
		assert.Equal(t, "InvalidParameterException", string(fail.ErrorCode))
	})
}

func TestSDKResourceGroupsTaggingTypeFilterAndPaging(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	// F1/F2 fixtures: one resource of several aggregated-but-previously-unfilterable
	// services, each carrying a real ARN.
	_, err := cloud.SecretsManager.CreateSecret(ctx, secretsdriver.SecretConfig{Name: "db-cred"}, []byte("v"))
	require.NoError(t, err)

	_, err = cloud.SQS.CreateQueue(ctx, mqdriver.QueueConfig{Name: "jobs"})
	require.NoError(t, err)

	_, err = cloud.ElastiCache.CreateCache(ctx, cachedriver.CacheConfig{
		Name: "cache1", NodeType: "cache.t3.micro", Engine: "redis",
	})
	require.NoError(t, err)

	_, err = cloud.RDS.CreateInstance(ctx, rdsdriver.InstanceConfig{
		ID: "orders", Engine: "mysql", InstanceClass: "db.t3.micro",
	})
	require.NoError(t, err)

	_, err = cloud.ECR.CreateRepository(ctx, crdriver.RepositoryConfig{Name: "web"})
	require.NoError(t, err)

	srv := awsserver.New(awsserver.Drivers{
		ResourceDiscovery: cloud.ResourceDiscovery,
		AccountID:         "123456789012",
		Region:            "us-east-1",
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newRGTAClient(t, ts.URL)

	cacheARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:cache1"
	rdsARN := "arn:aws:rds:us-east-1:123456789012:db:orders"
	ecrARN := "arn:aws:ecr:us-east-1:123456789012:repository/web"

	t.Run("ResourceTypeFilters select the right service (F1)", func(t *testing.T) {
		// Each service filter must return exactly its one resource, whose ARN
		// carries that service token (segment 3). Secrets Manager appends a random
		// suffix to the secret name, so match on the ARN prefix rather than a
		// hardcoded id.
		for _, filter := range []string{"secretsmanager", "sqs", "elasticache", "rds", "ecr"} {
			out, err := client.GetResources(ctx, &rgta.GetResourcesInput{
				ResourceTypeFilters: []string{filter},
			})
			require.NoError(t, err, filter)
			arns := arnSet(out.ResourceTagMappingList)
			require.Len(t, arns, 1, "filter %q should return exactly its one resource", filter)
			assert.True(t, strings.HasPrefix(arns[0], "arn:aws:"+filter+":"),
				"filter %q returned %q, want an arn:aws:%s ARN", filter, arns[0], filter)
		}
	})

	t.Run("ElastiCache ResourceARN is an ARN, not an endpoint (F2)", func(t *testing.T) {
		out, err := client.GetResources(ctx, &rgta.GetResourcesInput{
			ResourceTypeFilters: []string{"elasticache"},
		})
		require.NoError(t, err)
		require.Len(t, out.ResourceTagMappingList, 1)
		assert.Equal(t, cacheARN, aws.ToString(out.ResourceTagMappingList[0].ResourceARN))
	})

	t.Run("TagResources round-trips on rds/elasticache/ecr (F3)", func(t *testing.T) {
		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{cacheARN, rdsARN, ecrARN},
			Tags:            map[string]string{"Owner": "platform"},
		})
		require.NoError(t, err)
		assert.Empty(t, out.FailedResourcesMap, "all three should tag cleanly")

		// Visible on each service's own read.
		caches, err := cloud.ElastiCache.ListTags(ctx, cacheARN)
		require.NoError(t, err)
		assert.Equal(t, "platform", caches["Owner"])

		rdsTags, err := cloud.RDS.ListTagsForResource(ctx, rdsARN)
		require.NoError(t, err)
		assert.Equal(t, "platform", rdsTags["Owner"])

		repos, err := cloud.ECR.ListRepositories(ctx)
		require.NoError(t, err)
		ecrTags := map[string]string{}
		for i := range repos {
			if repos[i].Name == "web" {
				ecrTags = repos[i].Tags
			}
		}
		assert.Equal(t, "platform", ecrTags["Owner"])

		// And visible on a subsequent GetResources for that ARN.
		got, err := client.GetResources(ctx, &rgta.GetResourcesInput{
			ResourceTypeFilters: []string{"rds"},
		})
		require.NoError(t, err)
		require.Len(t, got.ResourceTagMappingList, 1)
		assert.Equal(t, "platform", tagsToMap(got.ResourceTagMappingList[0].Tags)["Owner"])
	})

	t.Run("genuinely unsupported service still reports a failure (F3)", func(t *testing.T) {
		badARN := "arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu-high"
		out, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{badARN},
			Tags:            map[string]string{"k": "v"},
		})
		require.NoError(t, err)
		require.Len(t, out.FailedResourcesMap, 1)
		assert.Equal(t, "InvalidParameterException", string(out.FailedResourcesMap[badARN].ErrorCode))
	})

	t.Run("empty ResourceARNList is rejected", func(t *testing.T) {
		_, err := client.TagResources(ctx, &rgta.TagResourcesInput{
			ResourceARNList: []string{},
			Tags:            map[string]string{"k": "v"},
		})
		require.Error(t, err)
	})

	t.Run("ResourcesPerPage + PaginationToken (F4)", func(t *testing.T) {
		total := len(mustGetAll(ctx, t, client))
		require.GreaterOrEqual(t, total, 5, "need several resources to page")

		seen := map[string]bool{}
		token := ""
		pages := 0

		for {
			out, err := client.GetResources(ctx, &rgta.GetResourcesInput{
				ResourcesPerPage: aws.Int32(2),
				PaginationToken:  aws.String(token),
			})
			require.NoError(t, err)
			assert.LessOrEqual(t, len(out.ResourceTagMappingList), 2, "page must not exceed ResourcesPerPage")

			for _, m := range out.ResourceTagMappingList {
				arn := aws.ToString(m.ResourceARN)
				assert.False(t, seen[arn], "resource %q returned twice across pages", arn)
				seen[arn] = true
			}

			pages++
			token = aws.ToString(out.PaginationToken)
			if token == "" {
				break
			}

			require.Less(t, pages, total+2, "pagination did not terminate")
		}

		assert.Len(t, seen, total, "paging must cover every resource exactly once")
		assert.Greater(t, pages, 1, "a page size of 2 over 5+ resources must span multiple pages")
	})
}

func mustGetAll(ctx context.Context, t *testing.T, client *rgta.Client) []rgtatypes.ResourceTagMapping {
	t.Helper()

	out, err := client.GetResources(ctx, &rgta.GetResourcesInput{})
	require.NoError(t, err)

	return out.ResourceTagMappingList
}

func arnSet(list []rgtatypes.ResourceTagMapping) []string {
	out := make([]string, 0, len(list))
	for _, m := range list {
		out = append(out, aws.ToString(m.ResourceARN))
	}

	return out
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
