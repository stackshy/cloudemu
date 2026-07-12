// Real-SDK round-trip test: the live aws-sdk-go-v2 Resource Explorer 2
// client drives the in-memory handler end-to-end.

package resourceexplorer2_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	rex "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
	rextypes "github.com/aws/aws-sdk-go-v2/service/resourceexplorer2/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func TestSDKResourceExplorer2(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	require.NoError(t, cloud.S3.CreateBucket(ctx, "prod-bucket"))
	require.NoError(t, cloud.S3.PutBucketTagging(ctx, "prod-bucket", map[string]string{"env": "prod"}))

	require.NoError(t, cloud.S3.CreateBucket(ctx, "stage-bucket"))
	require.NoError(t, cloud.S3.PutBucketTagging(ctx, "stage-bucket", map[string]string{"env": "stage"}))

	require.NoError(t, cloud.DynamoDB.CreateTable(ctx, dbdriver.TableConfig{Name: "events", PartitionKey: "pk"}))
	require.NoError(t, cloud.DynamoDB.TagResource(ctx, "events", map[string]string{"env": "prod"}))

	_, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"},
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

	client := newREXClient(t, ts.URL)

	t.Run("GetIndex returns bootstrap index", func(t *testing.T) {
		out, err := client.GetIndex(ctx, &rex.GetIndexInput{})
		require.NoError(t, err)
		assert.NotEmpty(t, aws.ToString(out.Arn))
		assert.Equal(t, rextypes.IndexTypeLocal, out.Type)
	})

	t.Run("ListIndexes returns at least the local index", func(t *testing.T) {
		out, err := client.ListIndexes(ctx, &rex.ListIndexesInput{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.Indexes), 1)
	})

	t.Run("Search with no view returns all resources", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			QueryString: aws.String(""),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(out.Resources), 4, "expect 2 buckets + 1 table + 1 vpc")
	})

	t.Run("Search filters by tag", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			QueryString: aws.String("tag.env:prod"),
		})
		require.NoError(t, err)
		// prod-bucket, events table, vpc — 3 prod resources
		assert.Len(t, out.Resources, 3)
	})

	t.Run("Search filters by service", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			QueryString: aws.String("service:s3"),
		})
		require.NoError(t, err)
		assert.Len(t, out.Resources, 2, "two s3 buckets")
	})

	t.Run("Search combines service + tag", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			QueryString: aws.String("service:s3 tag.env:prod"),
		})
		require.NoError(t, err)
		assert.Len(t, out.Resources, 1)
		assert.Equal(t, "arn:aws:s3:::prod-bucket", aws.ToString(out.Resources[0].Arn))
	})

	var viewArn string

	t.Run("CreateView", func(t *testing.T) {
		out, err := client.CreateView(ctx, &rex.CreateViewInput{
			ViewName: aws.String("prod-only"),
			Filters: &rextypes.SearchFilter{
				FilterString: aws.String("tag.env:prod"),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, out.View)
		viewArn = aws.ToString(out.View.ViewArn)
		assert.NotEmpty(t, viewArn)
	})

	t.Run("ListViews includes the new view", func(t *testing.T) {
		out, err := client.ListViews(ctx, &rex.ListViewsInput{})
		require.NoError(t, err)
		assert.Contains(t, out.Views, viewArn)
	})

	t.Run("GetView returns the filter", func(t *testing.T) {
		out, err := client.GetView(ctx, &rex.GetViewInput{ViewArn: aws.String(viewArn)})
		require.NoError(t, err)
		require.NotNil(t, out.View)
		require.NotNil(t, out.View.Filters)
		assert.Equal(t, "tag.env:prod", aws.ToString(out.View.Filters.FilterString))
	})

	t.Run("Search via view filters server-side", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			ViewArn:     aws.String(viewArn),
			QueryString: aws.String(""),
		})
		require.NoError(t, err)
		assert.Len(t, out.Resources, 3, "view's tag.env:prod filter applies")
	})

	t.Run("DeleteView", func(t *testing.T) {
		_, err := client.DeleteView(ctx, &rex.DeleteViewInput{ViewArn: aws.String(viewArn)})
		require.NoError(t, err)

		listOut, err := client.ListViews(ctx, &rex.ListViewsInput{})
		require.NoError(t, err)
		assert.NotContains(t, listOut.Views, viewArn)
	})
}

// TestSDKResourceExplorer2_BugFixes exercises the four review-fix paths in
// isolation so each can fail with a clear signal if it regresses.
func TestSDKResourceExplorer2_BugFixes(t *testing.T) {
	ctx := context.Background()
	cloud := cloudemu.NewAWS()

	// Seed one VPC (networking) and one EC2 instance via the engine surface.
	_, err := cloud.VPC.CreateVPC(ctx, netdriver.VPCConfig{
		CIDRBlock: "10.0.0.0/16", Tags: map[string]string{"env": "prod"},
	})
	require.NoError(t, err)

	require.NoError(t, cloud.S3.CreateBucket(ctx, "log-bucket"))
	require.NoError(t, cloud.S3.PutBucketTagging(ctx, "log-bucket", map[string]string{"env": "prod"}))

	srv := awsserver.New(awsserver.Drivers{
		S3:                cloud.S3,
		VPC:               cloud.VPC,
		ResourceDiscovery: cloud.ResourceDiscovery,
		AccountID:         "123456789012",
		Region:            "us-east-1",
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := newREXClient(t, ts.URL)

	t.Run("service:ec2 matches networking (not s3)", func(t *testing.T) {
		out, err := client.Search(ctx, &rex.SearchInput{
			QueryString: aws.String("service:ec2"),
		})
		require.NoError(t, err)
		assert.Len(t, out.Resources, 1, "service:ec2 should expand to compute+networking; only the VPC matches")
		assert.Equal(t, "ec2", aws.ToString(out.Resources[0].Service))
	})

	t.Run("CreateView rejects duplicate name", func(t *testing.T) {
		_, err := client.CreateView(ctx, &rex.CreateViewInput{ViewName: aws.String("dup")})
		require.NoError(t, err)

		_, err = client.CreateView(ctx, &rex.CreateViewInput{ViewName: aws.String("dup")})
		require.Error(t, err, "second CreateView with the same name must fail")
		assert.Contains(t, err.Error(), "ConflictException")
	})

	t.Run("View Owner field carries the account ID", func(t *testing.T) {
		created, err := client.CreateView(ctx, &rex.CreateViewInput{ViewName: aws.String("owned-view")})
		require.NoError(t, err)

		got, err := client.GetView(ctx, &rex.GetViewInput{ViewArn: created.View.ViewArn})
		require.NoError(t, err)
		assert.Equal(t, "123456789012", aws.ToString(got.View.Owner))
	})
}

func newREXClient(t *testing.T, baseURL string) *rex.Client {
	t.Helper()

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("k", "s", "")),
	)
	if err != nil {
		t.Fatalf("awsconfig: %v", err)
	}

	return rex.NewFromConfig(cfg, func(o *rex.Options) {
		o.BaseEndpoint = aws.String(baseURL)
	})
}
