package seed_test

import (
	"context"
	"embed"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/seed"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

//go:embed testdata/fixtures.json
var fixturesFS embed.FS

// TestSeedAWSReadViaSDK is the #250 acceptance: embed a fixtures dir, seed the
// AWS drivers, and read the resources back through real SDK clients.
func TestSeedAWSReadViaSDK(t *testing.T) {
	ctx := context.Background()

	f, err := seed.LoadFS(fixturesFS, "testdata/fixtures.json")
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}

	cloud := cloudemu.NewAWS()
	if err := seed.Apply(ctx, f, seed.Target{
		Storage:  cloud.S3,
		Database: cloud.DynamoDB,
		Secrets:  cloud.SecretsManager,
		Compute:  cloud.EC2,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	ts := httptest.NewServer(awsserver.New(awsserver.Drivers{S3: cloud.S3, DynamoDB: cloud.DynamoDB}))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("t", "t", "")))
	if err != nil {
		t.Fatal(err)
	}

	// Object storage: the seeded object comes back through the real S3 client.
	s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
	})
	obj, err := s3c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("app-data"), Key: aws.String("config.yaml")})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	body, _ := io.ReadAll(obj.Body)
	if string(body) != "port: 8080" {
		t.Fatalf("seeded object body = %q, want %q", body, "port: 8080")
	}

	// NoSQL: the seeded item comes back through the real DynamoDB client.
	ddb := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	got, err := ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String("users"),
		Key:       map[string]ddbtypes.AttributeValue{"id": &ddbtypes.AttributeValueMemberS{Value: "u1"}},
	})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	name, ok := got.Item["name"].(*ddbtypes.AttributeValueMemberS)
	if !ok || name.Value != "Ada" {
		t.Fatalf("seeded item = %+v, want name=Ada", got.Item)
	}

	// Secrets and instances applied through their drivers.
	if _, err := cloud.SecretsManager.GetSecret(ctx, "db-password"); err != nil {
		t.Fatalf("seeded secret not found: %v", err)
	}
	insts, err := cloud.EC2.DescribeInstances(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 2 {
		t.Fatalf("seeded instances = %d, want 2", len(insts))
	}
}

func TestLoadMalformed(t *testing.T) {
	if _, err := seed.Load([]byte(`{not json`)); err == nil {
		t.Fatal("Load of malformed JSON returned nil error")
	}
}

// TestApplyValidationRejectsBeforeWriting is the important guard: an invalid
// fixture (here, a table with no partition key — whose items would otherwise
// silently collapse to one key) must be rejected, and nothing created.
func TestApplyValidationRejectsBeforeWriting(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()
	target := seed.Target{Storage: cloud.S3, Database: cloud.DynamoDB}

	f := seed.Fixtures{
		Buckets: []seed.Bucket{{Name: "made-first"}},
		Tables:  []seed.Table{{Name: "no-pk" /* PartitionKey omitted */, Items: []map[string]any{{"a": 1}}}},
	}
	if err := seed.Apply(ctx, f, target); err == nil {
		t.Fatal("Apply accepted a table with no partitionKey")
	}
	// Validation runs before any writes, so the earlier bucket must NOT exist.
	buckets, err := cloud.S3.ListBuckets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 0 {
		t.Fatalf("a bucket was created despite the fixture being invalid (%d buckets) — validation should precede writes", len(buckets))
	}
}

func TestApplyNilDriver(t *testing.T) {
	ctx := context.Background()
	// Buckets declared but Storage is nil → clear error, not a panic.
	err := seed.Apply(ctx, seed.Fixtures{Buckets: []seed.Bucket{{Name: "b"}}}, seed.Target{})
	if err == nil {
		t.Fatal("Apply with buckets but nil Storage returned nil error")
	}
}

func TestResourceCount(t *testing.T) {
	f := seed.Fixtures{
		Buckets:   []seed.Bucket{{Name: "b", Objects: []seed.Object{{Key: "1"}, {Key: "2"}}}},          // 1 + 2
		Tables:    []seed.Table{{Name: "t", PartitionKey: "id", Items: []map[string]any{{"id": "x"}}}}, // 1 + 1
		Secrets:   []seed.Secret{{Name: "s"}},                                                          // 1
		Instances: []seed.Instance{{ImageID: "ami", Count: 3}},                                         // 3
	}
	if got := f.ResourceCount(); got != 9 {
		t.Fatalf("ResourceCount = %d, want 9", got)
	}
}
