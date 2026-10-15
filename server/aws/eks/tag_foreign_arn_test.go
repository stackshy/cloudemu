package eks_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awseks "github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/smithy-go"

	cloudemu "github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// TestSDKEKSTaggingForeignARNNamesNothing locks that a tagging ARN of another
// account or region, or a value that is not an EKS ARN, does not act on the
// local resource of the same name.
func TestSDKEKSTaggingForeignARNNamesNothing(t *testing.T) {
	cloud := cloudemu.NewAWS()
	d := awsserver.DriversFrom(cloud)

	ts := httptest.NewServer(awsserver.New(d))
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")))
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	client := awseks.NewFromConfig(cfg, func(o *awseks.Options) { o.BaseEndpoint = aws.String(ts.URL) })
	ctx := context.Background()

	out, err := client.CreateCluster(ctx, &awseks.CreateClusterInput{
		Name:               aws.String("tagged"),
		RoleArn:            aws.String("arn:aws:iam::123456789012:role/eks"),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{SubnetIds: []string{"subnet-1"}},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	arn := aws.ToString(out.Cluster.Arn)
	region := ":" + d.Region + ":"

	for _, tc := range []struct{ ref, code string }{
		{strings.Replace(arn, ":"+d.AccountID+":", ":999999999999:", 1), "NotFoundException"},
		{strings.Replace(arn, region, ":eu-west-3:", 1), "NotFoundException"},
		{"arn:aws:s3:::bucket", "BadRequestException"},
		{"tagged", "BadRequestException"},
	} {
		_, err := client.TagResource(ctx, &awseks.TagResourceInput{ResourceArn: aws.String(tc.ref), Tags: map[string]string{"k": "v"}})

		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != tc.code {
			t.Errorf("TagResource %s: err = %v, want %s", tc.ref, err, tc.code)
		}
	}

	tags, err := client.ListTagsForResource(ctx, &awseks.ListTagsForResourceInput{ResourceArn: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 0 {
		t.Fatalf("cluster tags after foreign-ARN requests = %v, want none", tags.Tags)
	}

	if _, err := client.TagResource(ctx, &awseks.TagResourceInput{ResourceArn: aws.String(arn), Tags: map[string]string{"k": "v"}}); err != nil {
		t.Fatalf("TagResource on the cluster ARN: %v", err)
	}
}
