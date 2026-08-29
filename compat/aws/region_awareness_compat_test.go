package aws

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awsecr "github.com/aws/aws-sdk-go-v2/service/ecr"
	awsecs "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	awselasticache "github.com/aws/aws-sdk-go-v2/service/elasticache"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awskinesis "github.com/aws/aws-sdk-go-v2/service/kinesis"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awsrds "github.com/aws/aws-sdk-go-v2/service/rds"
	awssfn "github.com/aws/aws-sdk-go-v2/service/sfn"
	awssns "github.com/aws/aws-sdk-go-v2/service/sns"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// The two regions exercised: one non-default region a client actually addresses,
// and the emulator's default. A regional resource must carry whichever the
// client used, not the fixed default.
const (
	regionTokyo   = "ap-northeast-1"
	regionDefault = "us-east-1"
)

// assertRegion fails when arn does not carry want in its region field. It is the
// single assertion every regional case shares.
func assertRegion(t *testing.T, service, arn, want string) {
	t.Helper()

	if arn == "" {
		t.Fatalf("%s: empty ARN", service)
	}

	if !strings.Contains(arn, ":"+want+":") {
		t.Fatalf("%s: ARN %q does not carry region %q", service, arn, want)
	}
}

// TestRegionAwarenessAWSCompat drives real aws-sdk-go-v2 clients configured for
// ap-northeast-1 against CloudEmu's in-process wire server and asserts each
// regional resource is stamped with the request's region (not the fixed
// us-east-1 default). It also asserts a us-east-1 client still gets us-east-1,
// and that a global service (IAM) stays region-less.
func TestRegionAwarenessAWSCompat(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{
		ECR: cloud.ECR, SQS: cloud.SQS, SNS: cloud.SNS, Lambda: cloud.Lambda,
		DynamoDB: cloud.DynamoDB, KMS: cloud.KMS, CloudWatchLogs: cloud.CloudWatchLogs,
		Kinesis: cloud.Kinesis, RDS: cloud.RDS, ElastiCache: cloud.ElastiCache,
		SFN: cloud.SFN, ECS: cloud.ECS, EKS: cloud.EKS, IAM: cloud.IAM,
	})

	ctx := context.Background()
	cfg := sess.Config()
	endpoint := sess.Endpoint()

	t.Run("ecr", func(t *testing.T) {
		c := awsecr.NewFromConfig(cfg, func(o *awsecr.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateRepository(ctx, &awsecr.CreateRepositoryInput{
			RepositoryName: aws.String("region-repo"),
		})
		if err != nil {
			t.Fatalf("CreateRepository: %v", err)
		}
		assertRegion(t, "ecr", aws.ToString(out.Repository.RepositoryArn), regionTokyo)

		// Read-back must reflect the stored request region, not the read's.
		desc, err := c.DescribeRepositories(ctx, &awsecr.DescribeRepositoriesInput{
			RepositoryNames: []string{"region-repo"},
		})
		if err != nil {
			t.Fatalf("DescribeRepositories: %v", err)
		}
		assertRegion(t, "ecr read-back", aws.ToString(desc.Repositories[0].RepositoryArn), regionTokyo)
	})

	t.Run("sqs", func(t *testing.T) {
		c := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("region-queue")})
		if err != nil {
			t.Fatalf("CreateQueue: %v", err)
		}
		if url := aws.ToString(out.QueueUrl); !strings.Contains(url, regionTokyo) {
			t.Fatalf("sqs: QueueUrl %q does not carry region %q", url, regionTokyo)
		}

		attrs, err := c.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl:       out.QueueUrl,
			AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
		})
		if err != nil {
			t.Fatalf("GetQueueAttributes: %v", err)
		}
		assertRegion(t, "sqs", attrs.Attributes["QueueArn"], regionTokyo)
	})

	t.Run("sns", func(t *testing.T) {
		c := awssns.NewFromConfig(cfg, func(o *awssns.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("region-topic")})
		if err != nil {
			t.Fatalf("CreateTopic: %v", err)
		}
		assertRegion(t, "sns", aws.ToString(out.TopicArn), regionTokyo)
	})

	t.Run("lambda", func(t *testing.T) {
		c := awslambda.NewFromConfig(cfg, func(o *awslambda.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateFunction(ctx, &awslambda.CreateFunctionInput{
			FunctionName: aws.String("region-fn"),
			Runtime:      lambdatypes.RuntimeGo1x,
			Role:         aws.String("arn:aws:iam::000000000000:role/test"),
			Handler:      aws.String("main"),
			Code:         &lambdatypes.FunctionCode{ZipFile: []byte("fake-zip")},
		})
		if err != nil {
			t.Fatalf("CreateFunction: %v", err)
		}
		assertRegion(t, "lambda", aws.ToString(out.FunctionArn), regionTokyo)
	})

	t.Run("dynamodb", func(t *testing.T) {
		c := awsdynamodb.NewFromConfig(cfg, func(o *awsdynamodb.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateTable(ctx, &awsdynamodb.CreateTableInput{
			TableName:   aws.String("region-table"),
			BillingMode: dynamodbtypes.BillingModePayPerRequest,
			AttributeDefinitions: []dynamodbtypes.AttributeDefinition{
				{AttributeName: aws.String("id"), AttributeType: dynamodbtypes.ScalarAttributeTypeS},
			},
			KeySchema: []dynamodbtypes.KeySchemaElement{
				{AttributeName: aws.String("id"), KeyType: dynamodbtypes.KeyTypeHash},
			},
		})
		if err != nil {
			t.Fatalf("CreateTable: %v", err)
		}
		assertRegion(t, "dynamodb", aws.ToString(out.TableDescription.TableArn), regionTokyo)
	})

	t.Run("kms", func(t *testing.T) {
		c := awskms.NewFromConfig(cfg, func(o *awskms.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateKey(ctx, &awskms.CreateKeyInput{})
		if err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
		assertRegion(t, "kms", aws.ToString(out.KeyMetadata.Arn), regionTokyo)
	})

	t.Run("cloudwatchlogs", func(t *testing.T) {
		c := awscwl.NewFromConfig(cfg, func(o *awscwl.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		if _, err := c.CreateLogGroup(ctx, &awscwl.CreateLogGroupInput{
			LogGroupName: aws.String("region-lg"),
		}); err != nil {
			t.Fatalf("CreateLogGroup: %v", err)
		}
		out, err := c.DescribeLogGroups(ctx, &awscwl.DescribeLogGroupsInput{
			LogGroupNamePrefix: aws.String("region-lg"),
		})
		if err != nil {
			t.Fatalf("DescribeLogGroups: %v", err)
		}
		assertRegion(t, "logs", aws.ToString(out.LogGroups[0].Arn), regionTokyo)
	})

	t.Run("kinesis", func(t *testing.T) {
		c := awskinesis.NewFromConfig(cfg, func(o *awskinesis.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		if _, err := c.CreateStream(ctx, &awskinesis.CreateStreamInput{
			StreamName: aws.String("region-stream"), ShardCount: aws.Int32(1),
		}); err != nil {
			t.Fatalf("CreateStream: %v", err)
		}
		out, err := c.DescribeStreamSummary(ctx, &awskinesis.DescribeStreamSummaryInput{
			StreamName: aws.String("region-stream"),
		})
		if err != nil {
			t.Fatalf("DescribeStreamSummary: %v", err)
		}
		assertRegion(t, "kinesis", aws.ToString(out.StreamDescriptionSummary.StreamARN), regionTokyo)
	})

	t.Run("rds", func(t *testing.T) {
		c := awsrds.NewFromConfig(cfg, func(o *awsrds.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateDBInstance(ctx, &awsrds.CreateDBInstanceInput{
			DBInstanceIdentifier: aws.String("region-db"),
			Engine:               aws.String("mysql"),
			DBInstanceClass:      aws.String("db.t3.micro"),
			AllocatedStorage:     aws.Int32(20),
			MasterUsername:       aws.String("admin"),
			MasterUserPassword:   aws.String("password123"),
		})
		if err != nil {
			t.Fatalf("CreateDBInstance: %v", err)
		}
		assertRegion(t, "rds", aws.ToString(out.DBInstance.DBInstanceArn), regionTokyo)
	})

	t.Run("elasticache", func(t *testing.T) {
		c := awselasticache.NewFromConfig(cfg, func(o *awselasticache.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
			CacheClusterId: aws.String("region-cache"),
			Engine:         aws.String("redis"),
			CacheNodeType:  aws.String("cache.t3.micro"),
			NumCacheNodes:  aws.Int32(1),
		})
		if err != nil {
			t.Fatalf("CreateCacheCluster: %v", err)
		}
		assertRegion(t, "elasticache", aws.ToString(out.CacheCluster.ARN), regionTokyo)
	})

	t.Run("sfn", func(t *testing.T) {
		c := awssfn.NewFromConfig(cfg, func(o *awssfn.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateStateMachine(ctx, &awssfn.CreateStateMachineInput{
			Name:       aws.String("region-sm"),
			Definition: aws.String(`{"StartAt":"a","States":{"a":{"Type":"Pass","End":true}}}`),
			RoleArn:    aws.String("arn:aws:iam::000000000000:role/test"),
		})
		if err != nil {
			t.Fatalf("CreateStateMachine: %v", err)
		}
		assertRegion(t, "sfn", aws.ToString(out.StateMachineArn), regionTokyo)
	})

	t.Run("ecs", func(t *testing.T) {
		c := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateCluster(ctx, &awsecs.CreateClusterInput{ClusterName: aws.String("region-cluster")})
		if err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		assertRegion(t, "ecs", aws.ToString(out.Cluster.ClusterArn), regionTokyo)
	})

	t.Run("eks", func(t *testing.T) {
		c := awseks.NewFromConfig(cfg, func(o *awseks.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateCluster(ctx, &awseks.CreateClusterInput{
			Name:    aws.String("region-eks"),
			RoleArn: aws.String("arn:aws:iam::000000000000:role/test"),
			ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
				SubnetIds: []string{"subnet-1", "subnet-2"},
			},
		})
		if err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		assertRegion(t, "eks", aws.ToString(out.Cluster.Arn), regionTokyo)
	})

	// Default region unchanged: a us-east-1 client still gets us-east-1.
	t.Run("default_region_unchanged", func(t *testing.T) {
		c := awssns.NewFromConfig(cfg, func(o *awssns.Options) {
			o.Region = regionDefault
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateTopic(ctx, &awssns.CreateTopicInput{Name: aws.String("default-topic")})
		if err != nil {
			t.Fatalf("CreateTopic: %v", err)
		}
		assertRegion(t, "sns default", aws.ToString(out.TopicArn), regionDefault)
		if strings.Contains(aws.ToString(out.TopicArn), regionTokyo) {
			t.Fatalf("sns default: ARN %q leaked non-default region", aws.ToString(out.TopicArn))
		}
	})

	// Global service unchanged: IAM ARNs are region-less regardless of the
	// client's region. Stamping a region here would be wrong.
	t.Run("global_iam_region_less", func(t *testing.T) {
		c := awsiam.NewFromConfig(cfg, func(o *awsiam.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := c.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("region-user")})
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		arn := aws.ToString(out.User.Arn)
		if !strings.HasPrefix(arn, "arn:aws:iam::") {
			t.Fatalf("iam: ARN %q is not a region-less IAM ARN", arn)
		}
		if strings.Contains(arn, regionTokyo) {
			t.Fatalf("iam: global ARN %q was wrongly stamped with a region", arn)
		}
	})
}

// TestRegionAwarenessCrossRegionChild is the adversarial case the same-region
// suite cannot catch: it creates a PARENT with a client in one region, then
// issues the CHILD-creating call with a client signed for a DIFFERENT region,
// and asserts the child's ARN carries the PARENT's region, not the request's.
// A child must inherit its parent's region (from the parent's stored ARN), per
// the PR's child-ARN-inheritance contract. These cases FAIL against code that
// reconstructs the child region from the request.
func TestRegionAwarenessCrossRegionChild(t *testing.T) {
	cloud := cloudemu.NewAWS()
	sess := compat.BootAWS(t, awsserver.Drivers{ECS: cloud.ECS, ElastiCache: cloud.ElastiCache})

	ctx := context.Background()
	cfg := sess.Config()
	endpoint := sess.Endpoint()

	// ECS: a task created in ap-northeast-1 against a cluster that lives in
	// us-east-1 must carry the cluster's region on both its cluster and task ARN.
	t.Run("ecs_runtask_inherits_cluster_region", func(t *testing.T) {
		home := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) {
			o.Region = regionDefault
			o.BaseEndpoint = aws.String(endpoint)
		})
		if _, err := home.CreateCluster(ctx, &awsecs.CreateClusterInput{
			ClusterName: aws.String("xr-cluster"),
		}); err != nil {
			t.Fatalf("CreateCluster: %v", err)
		}
		if _, err := home.RegisterTaskDefinition(ctx, &awsecs.RegisterTaskDefinitionInput{
			Family:                  aws.String("xr-td"),
			NetworkMode:             ecstypes.NetworkModeAwsvpc,
			RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
			Cpu:                     aws.String("256"),
			Memory:                  aws.String("512"),
			ContainerDefinitions: []ecstypes.ContainerDefinition{{
				Name: aws.String("app"), Image: aws.String("nginx:latest"), Essential: aws.Bool(true),
			}},
		}); err != nil {
			t.Fatalf("RegisterTaskDefinition: %v", err)
		}

		// RunTask from a DIFFERENT region than the cluster was created in.
		away := awsecs.NewFromConfig(cfg, func(o *awsecs.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		run, err := away.RunTask(ctx, &awsecs.RunTaskInput{
			Cluster:        aws.String("xr-cluster"),
			TaskDefinition: aws.String("xr-td"),
			LaunchType:     ecstypes.LaunchTypeFargate,
			NetworkConfiguration: &ecstypes.NetworkConfiguration{
				AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{Subnets: []string{"subnet-1"}},
			},
		})
		if err != nil {
			t.Fatalf("RunTask: %v", err)
		}
		if len(run.Tasks) != 1 {
			t.Fatalf("RunTask returned %d tasks, want 1", len(run.Tasks))
		}

		clusterARN := aws.ToString(run.Tasks[0].ClusterArn)
		taskARN := aws.ToString(run.Tasks[0].TaskArn)
		assertRegion(t, "ecs task's clusterArn", clusterARN, regionDefault)
		assertRegion(t, "ecs task ARN", taskARN, regionDefault)
		if strings.Contains(clusterARN, regionTokyo) || strings.Contains(taskARN, regionTokyo) {
			t.Fatalf("ecs: child ARNs leaked the request region: clusterArn=%q taskArn=%q", clusterARN, taskARN)
		}
	})

	// ElastiCache: a snapshot taken in ap-northeast-1 of a cache cluster that
	// lives in us-east-1 must carry the cluster's region.
	t.Run("elasticache_snapshot_inherits_cluster_region", func(t *testing.T) {
		home := awselasticache.NewFromConfig(cfg, func(o *awselasticache.Options) {
			o.Region = regionDefault
			o.BaseEndpoint = aws.String(endpoint)
		})
		if _, err := home.CreateCacheCluster(ctx, &awselasticache.CreateCacheClusterInput{
			CacheClusterId: aws.String("xr-cache"),
			Engine:         aws.String("redis"),
			CacheNodeType:  aws.String("cache.t3.micro"),
			NumCacheNodes:  aws.Int32(1),
		}); err != nil {
			t.Fatalf("CreateCacheCluster: %v", err)
		}

		// CreateSnapshot from a DIFFERENT region than the cache cluster.
		away := awselasticache.NewFromConfig(cfg, func(o *awselasticache.Options) {
			o.Region = regionTokyo
			o.BaseEndpoint = aws.String(endpoint)
		})
		out, err := away.CreateSnapshot(ctx, &awselasticache.CreateSnapshotInput{
			CacheClusterId: aws.String("xr-cache"),
			SnapshotName:   aws.String("xr-snap"),
		})
		if err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		arn := aws.ToString(out.Snapshot.ARN)
		assertRegion(t, "elasticache snapshot", arn, regionDefault)
		if strings.Contains(arn, regionTokyo) {
			t.Fatalf("elasticache: snapshot ARN %q leaked the request region", arn)
		}
	})
}
