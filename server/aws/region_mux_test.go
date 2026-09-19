package aws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsdynamodb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsiam "github.com/aws/aws-sdk-go-v2/service/iam"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	awsprovider "github.com/stackshy/cloudemu/v2/providers/aws"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const (
	regionWest = "us-west-2"
	regionEast = "us-east-1"
)

// newMuxServer builds a multi-region mux exactly as the assembly layer does — a
// default-region base provider owning the shared global services, and a factory
// that builds a fresh regional provider sharing them on first touch — and fronts
// it with an httptest server.
func newMuxServer(t *testing.T) *httptest.Server {
	t.Helper()

	base := cloudemu.NewAWS()
	globals := base.Globals()

	buildServer := func(p *awsprovider.Provider) http.Handler {
		return awsserver.New(awsserver.DriversFrom(p))
	}
	newRegional := func(region string) awsserver.RegionEntry {
		p := awsprovider.NewRegional(globals, config.WithRegion(region))

		return awsserver.RegionEntry{Server: buildServer(p), Provider: p}
	}

	mux := awsserver.NewRegionMux(base.Region,
		awsserver.RegionEntry{Server: buildServer(base), Provider: base}, newRegional)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return ts
}

// regionCfg returns an aws.Config signed for region and pointed at url. The SDK
// stamps region into the SigV4 credential scope, which the mux reads to route.
func regionCfg(t *testing.T, url, region string) aws.Config {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	cfg.BaseEndpoint = aws.String(url)

	return cfg
}

func TestWireRegionIsolationDynamoDB(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	west := awsdynamodb.NewFromConfig(regionCfg(t, ts.URL, regionWest))
	east := awsdynamodb.NewFromConfig(regionCfg(t, ts.URL, regionEast))

	if _, err := west.CreateTable(ctx, &awsdynamodb.CreateTableInput{
		TableName:   aws.String("orders"),
		BillingMode: ddbtypes.BillingModePayPerRequest,
		AttributeDefinitions: []ddbtypes.AttributeDefinition{
			{AttributeName: aws.String("id"), AttributeType: ddbtypes.ScalarAttributeTypeS},
		},
		KeySchema: []ddbtypes.KeySchemaElement{
			{AttributeName: aws.String("id"), KeyType: ddbtypes.KeyTypeHash},
		},
	}); err != nil {
		t.Fatalf("west CreateTable: %v", err)
	}

	eastTables, err := east.ListTables(ctx, &awsdynamodb.ListTablesInput{})
	if err != nil {
		t.Fatalf("east ListTables: %v", err)
	}
	if len(eastTables.TableNames) != 0 {
		t.Fatalf("east ListTables = %v, want empty (isolation)", eastTables.TableNames)
	}

	westTables, err := west.ListTables(ctx, &awsdynamodb.ListTablesInput{})
	if err != nil {
		t.Fatalf("west ListTables: %v", err)
	}
	if len(westTables.TableNames) != 1 || westTables.TableNames[0] != "orders" {
		t.Fatalf("west ListTables = %v, want [orders]", westTables.TableNames)
	}
}

func TestWireRegionIsolationSQS(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	west := awssqs.NewFromConfig(regionCfg(t, ts.URL, regionWest))
	east := awssqs.NewFromConfig(regionCfg(t, ts.URL, regionEast))

	if _, err := west.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String("jobs")}); err != nil {
		t.Fatalf("west CreateQueue: %v", err)
	}

	if _, err := east.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("jobs")}); err == nil {
		t.Fatal("east GetQueueUrl(jobs) succeeded, want error (isolation)")
	}

	if _, err := west.GetQueueUrl(ctx, &awssqs.GetQueueUrlInput{QueueName: aws.String("jobs")}); err != nil {
		t.Fatalf("west GetQueueUrl(jobs): %v", err)
	}
}

func TestWireRegionIsolationEC2(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	west := awsec2.NewFromConfig(regionCfg(t, ts.URL, regionWest))
	east := awsec2.NewFromConfig(regionCfg(t, ts.URL, regionEast))

	if _, err := west.RunInstances(ctx, &awsec2.RunInstancesInput{
		ImageId: aws.String("ami-123"), MinCount: aws.Int32(1), MaxCount: aws.Int32(1),
	}); err != nil {
		t.Fatalf("west RunInstances: %v", err)
	}

	eastOut, err := east.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("east DescribeInstances: %v", err)
	}
	if n := countInstances(eastOut); n != 0 {
		t.Fatalf("east DescribeInstances = %d instances, want 0 (isolation)", n)
	}

	westOut, err := west.DescribeInstances(ctx, &awsec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("west DescribeInstances: %v", err)
	}
	if n := countInstances(westOut); n != 1 {
		t.Fatalf("west DescribeInstances = %d instances, want 1", n)
	}
}

func countInstances(out *awsec2.DescribeInstancesOutput) int {
	n := 0
	for _, r := range out.Reservations {
		n += len(r.Instances)
	}

	return n
}

func TestWireRegionIsolationLambda(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	west := awslambda.NewFromConfig(regionCfg(t, ts.URL, regionWest))
	east := awslambda.NewFromConfig(regionCfg(t, ts.URL, regionEast))

	if _, err := west.CreateFunction(ctx, &awslambda.CreateFunctionInput{
		FunctionName: aws.String("fn"),
		Runtime:      lambdatypes.RuntimePython312,
		Role:         aws.String("arn:aws:iam::123456789012:role/r"),
		Handler:      aws.String("index.handler"),
		Code:         &lambdatypes.FunctionCode{ZipFile: []byte("x")},
	}); err != nil {
		t.Fatalf("west CreateFunction: %v", err)
	}

	eastFns, err := east.ListFunctions(ctx, &awslambda.ListFunctionsInput{})
	if err != nil {
		t.Fatalf("east ListFunctions: %v", err)
	}
	if len(eastFns.Functions) != 0 {
		t.Fatalf("east ListFunctions = %d, want 0 (isolation)", len(eastFns.Functions))
	}

	westFns, err := west.ListFunctions(ctx, &awslambda.ListFunctionsInput{})
	if err != nil {
		t.Fatalf("west ListFunctions: %v", err)
	}
	if len(westFns.Functions) != 1 {
		t.Fatalf("west ListFunctions = %d, want 1", len(westFns.Functions))
	}
}

// TestWireGlobalSharedIAM proves IAM is global: a user created via one region is
// visible via another.
func TestWireGlobalSharedIAM(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	east := awsiam.NewFromConfig(regionCfg(t, ts.URL, regionEast))
	eu := awsiam.NewFromConfig(regionCfg(t, ts.URL, "eu-west-1"))

	if _, err := east.CreateUser(ctx, &awsiam.CreateUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("east CreateUser: %v", err)
	}

	if _, err := eu.GetUser(ctx, &awsiam.GetUserInput{UserName: aws.String("alice")}); err != nil {
		t.Fatalf("eu GetUser(alice) = %v, want found (IAM is global)", err)
	}
}

// TestWireS3GlobalNameUniqueness proves a bucket name taken in one region cannot
// be created in another (S3's global namespace).
func TestWireS3GlobalNameUniqueness(t *testing.T) {
	ts := newMuxServer(t)
	ctx := context.Background()

	s3opt := func(o *awss3.Options) { o.UsePathStyle = true }
	east := awss3.NewFromConfig(regionCfg(t, ts.URL, regionEast), s3opt)
	west := awss3.NewFromConfig(regionCfg(t, ts.URL, regionWest), s3opt)

	if _, err := east.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("dup-name")}); err != nil {
		t.Fatalf("east CreateBucket: %v", err)
	}

	if _, err := west.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String("dup-name")}); err == nil {
		t.Fatal("west CreateBucket(dup-name) succeeded, want BucketAlreadyExists (global namespace)")
	}
}
