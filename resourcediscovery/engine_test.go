package resourcediscovery

import (
	"context"
	"strings"
	"testing"
	"time"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/config"
	dbdriver "github.com/stackshy/cloudemu/database/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/providers/aws/dynamodb"
	"github.com/stackshy/cloudemu/providers/aws/ec2"
	"github.com/stackshy/cloudemu/providers/aws/lambda"
	"github.com/stackshy/cloudemu/providers/aws/s3"
	"github.com/stackshy/cloudemu/providers/aws/vpc"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	engine *Engine
	ec2    *ec2.Mock
	vpc    *vpc.Mock
	s3     *s3.Mock
	ddb    *dynamodb.Mock
	lambda *lambda.Mock
}

func newAWSFixture(t *testing.T) *fixture {
	t.Helper()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))

	ec2Mock := ec2.New(opts)
	vpcMock := vpc.New(opts)
	s3Mock := s3.New(opts)
	ddbMock := dynamodb.New(opts)
	lambdaMock := lambda.New(opts)

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{
		Compute:    ec2Mock,
		Networking: vpcMock,
		Storage:    s3Mock,
		Database:   ddbMock,
		Serverless: lambdaMock,
	})

	return &fixture{
		engine: eng,
		ec2:    ec2Mock,
		vpc:    vpcMock,
		s3:     s3Mock,
		ddb:    ddbMock,
		lambda: lambdaMock,
	}
}

func TestNew(t *testing.T) {
	eng := New(ProviderAWS, "acct", "us-east-1", nil)
	require.NotNil(t, eng)
	assert.Equal(t, ProviderAWS, eng.provider)
}

func TestListAllEmpty(t *testing.T) {
	f := newAWSFixture(t)

	out, err := f.engine.ListAll(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestListAllAcrossServices(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedCompute(t, f, map[string]string{"env": "prod"})
	seedVPC(t, f, map[string]string{"env": "prod"})
	seedS3(t, f, "data-bucket", map[string]string{"env": "stage"})
	seedDDB(t, f, "users", map[string]string{"env": "prod", "team": "core"})
	seedLambda(t, f, "handler", map[string]string{"env": "stage"})

	out, err := f.engine.ListAll(ctx)
	require.NoError(t, err)

	byType := groupByType(out)
	assert.Len(t, byType[TypeInstance], 1, "compute instance")
	assert.Len(t, byType[TypeVPC], 1, "vpc")
	assert.Len(t, byType[TypeBucket], 1, "bucket")
	assert.Len(t, byType[TypeTable], 1, "table")
	assert.Len(t, byType[TypeFunction], 1, "function")
}

func TestListWithQueryFilters(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedCompute(t, f, map[string]string{"env": "prod"})
	seedDDB(t, f, "users", map[string]string{"env": "prod"})
	seedDDB(t, f, "stage-cache", map[string]string{"env": "stage"})

	t.Run("by service", func(t *testing.T) {
		got, err := f.engine.List(ctx, Query{Service: ServiceDatabase})
		require.NoError(t, err)
		assert.Len(t, got, 2)
		for _, r := range got {
			assert.Equal(t, ServiceDatabase, r.Service)
		}
	})

	t.Run("by type", func(t *testing.T) {
		got, err := f.engine.List(ctx, Query{Type: TypeInstance})
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, TypeInstance, got[0].Type)
	})

	t.Run("by tag key+value", func(t *testing.T) {
		got, err := f.engine.List(ctx, Query{Tags: map[string]string{"env": "prod"}})
		require.NoError(t, err)
		assert.Len(t, got, 2, "expected compute + users table")
	})

	t.Run("by tag key only", func(t *testing.T) {
		got, err := f.engine.List(ctx, Query{Tags: map[string]string{"env": ""}})
		require.NoError(t, err)
		assert.Len(t, got, 3, "all 3 carry env tag")
	})
}

func TestSearchByTag(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedDDB(t, f, "prod-tbl", map[string]string{"env": "prod"})
	seedDDB(t, f, "stage-tbl", map[string]string{"env": "stage"})

	got, err := f.engine.SearchByTag(ctx, "env", "prod")
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "prod-tbl", got[0].ID)
}

func TestGetTagKeysAndValues(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedCompute(t, f, map[string]string{"env": "prod", "team": "core"})
	seedDDB(t, f, "tbl", map[string]string{"env": "stage"})

	keys, err := f.engine.GetTagKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"env", "team"}, keys)

	envValues, err := f.engine.GetTagValues(ctx, "env")
	require.NoError(t, err)
	assert.Equal(t, []string{"prod", "stage"}, envValues)

	missing, err := f.engine.GetTagValues(ctx, "missing")
	require.NoError(t, err)
	assert.Empty(t, missing)
}

func TestARNShapes(t *testing.T) {
	ctx := context.Background()
	f := newAWSFixture(t)

	seedCompute(t, f, nil)
	seedVPC(t, f, nil)
	seedS3(t, f, "my-bkt", nil)
	seedDDB(t, f, "my-tbl", nil)
	seedLambda(t, f, "my-fn", nil)

	out, err := f.engine.ListAll(ctx)
	require.NoError(t, err)

	for _, r := range out {
		switch r.Type {
		case TypeInstance:
			assert.True(t, strings.HasPrefix(r.ARN, "arn:aws:ec2:us-east-1:123456789012:instance/"),
				"unexpected instance ARN: %s", r.ARN)
		case TypeVPC:
			assert.True(t, strings.HasPrefix(r.ARN, "arn:aws:ec2:us-east-1:123456789012:vpc/"),
				"unexpected VPC ARN: %s", r.ARN)
		case TypeBucket:
			assert.Equal(t, "arn:aws:s3:::my-bkt", r.ARN)
		case TypeTable:
			assert.Equal(t, "arn:aws:dynamodb:us-east-1:123456789012:table/my-tbl", r.ARN)
		case TypeFunction:
			// Lambda mock builds its own ARN; we just require non-empty.
			assert.NotEmpty(t, r.ARN)
		default:
			t.Fatalf("unexpected resource type: %s", r.Type)
		}
	}
}

func TestNilDriversSkipped(t *testing.T) {
	ctx := context.Background()

	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"))
	ddbMock := dynamodb.New(opts)

	eng := New(ProviderAWS, "acct", "us-east-1", &Drivers{Database: ddbMock})
	require.NoError(t, ddbMock.CreateTable(ctx, dbdriver.TableConfig{Name: "only", PartitionKey: "pk"}))

	out, err := eng.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, ServiceDatabase, out[0].Service)
}

func seedCompute(t *testing.T, f *fixture, tags map[string]string) {
	t.Helper()
	_, err := f.ec2.RunInstances(context.Background(), computedriver.InstanceConfig{
		ImageID: "ami-1", InstanceType: "t2.micro", Tags: tags,
	}, 1)
	require.NoError(t, err)
}

func seedVPC(t *testing.T, f *fixture, tags map[string]string) {
	t.Helper()
	_, err := f.vpc.CreateVPC(context.Background(), netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: tags,
	})
	require.NoError(t, err)
}

func seedS3(t *testing.T, f *fixture, name string, tags map[string]string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.s3.CreateBucket(ctx, name))
	if len(tags) > 0 {
		require.NoError(t, f.s3.PutBucketTagging(ctx, name, tags))
	}
}

func seedDDB(t *testing.T, f *fixture, name string, tags map[string]string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.ddb.CreateTable(ctx, dbdriver.TableConfig{Name: name, PartitionKey: "pk"}))
	if len(tags) > 0 {
		require.NoError(t, f.ddb.TagResource(ctx, name, tags))
	}
}

func seedLambda(t *testing.T, f *fixture, name string, tags map[string]string) {
	t.Helper()
	_, err := f.lambda.CreateFunction(context.Background(), serverlessdriver.FunctionConfig{
		Name: name, Runtime: "go1.x", Handler: "main", Memory: 128, Timeout: 30, Tags: tags,
	})
	require.NoError(t, err)
}

func groupByType(rs []Resource) map[string][]Resource {
	out := make(map[string][]Resource)
	for _, r := range rs {
		out[r.Type] = append(out[r.Type], r)
	}
	return out
}
