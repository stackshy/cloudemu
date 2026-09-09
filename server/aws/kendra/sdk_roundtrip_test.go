package kendra_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awskendra "github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

const roleArn = "arn:aws:iam::123456789012:role/kendra"

func newClient(t *testing.T) *awskendra.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Kendra: cloud.Kendra})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awskendra.NewFromConfig(cfg, func(o *awskendra.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKIndexAndDataSourceLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateIndex(ctx, &awskendra.CreateIndexInput{
		Name:        aws.String("docs"),
		Edition:     kendratypes.IndexEditionDeveloperEdition,
		RoleArn:     aws.String(roleArn),
		Description: aws.String("managed by test"),
	})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	id := aws.ToString(create.Id)
	if len(id) != 36 {
		t.Fatalf("expected a 36-char index id, got %q", id)
	}

	// DescribeIndex must report ACTIVE synchronously so an IaC waiter completes
	// instead of hanging on real Kendra's ~30-minute provisioning.
	desc, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeIndex: %v", err)
	}

	if desc.Status != kendratypes.IndexStatusActive {
		t.Fatalf("expected ACTIVE, got %s", desc.Status)
	}

	if desc.Edition != kendratypes.IndexEditionDeveloperEdition {
		t.Fatalf("edition = %s", desc.Edition)
	}

	if desc.UserContextPolicy != kendratypes.UserContextPolicyAttributeFilter {
		t.Fatalf("expected default ATTRIBUTE_FILTER, got %s", desc.UserContextPolicy)
	}

	// A second read is byte-stable on the computed fields.
	desc2, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeIndex#2: %v", err)
	}

	if aws.ToString(desc2.Id) != id || !desc2.CreatedAt.Equal(*desc.CreatedAt) {
		t.Fatalf("computed fields drifted across reads")
	}

	// A CUSTOM data source referencing the index.
	ds, err := c.CreateDataSource(ctx, &awskendra.CreateDataSourceInput{
		IndexId: aws.String(id),
		Name:    aws.String("custom-ds"),
		Type:    kendratypes.DataSourceTypeCustom,
	})
	if err != nil {
		t.Fatalf("CreateDataSource: %v", err)
	}

	dsID := aws.ToString(ds.Id)
	if dsID == "" {
		t.Fatalf("expected a data source id")
	}

	dsDesc, err := c.DescribeDataSource(ctx, &awskendra.DescribeDataSourceInput{
		Id: aws.String(dsID), IndexId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("DescribeDataSource: %v", err)
	}

	if dsDesc.Status != kendratypes.DataSourceStatusActive {
		t.Fatalf("expected data source ACTIVE, got %s", dsDesc.Status)
	}

	if dsDesc.Type != kendratypes.DataSourceTypeCustom {
		t.Fatalf("data source type = %s", dsDesc.Type)
	}

	// Update the index description; a later Describe reflects it with stable id.
	if _, err = c.UpdateIndex(ctx, &awskendra.UpdateIndexInput{
		Id: aws.String(id), Description: aws.String("updated"),
	}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	descAfter, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeIndex after update: %v", err)
	}

	if aws.ToString(descAfter.Description) != "updated" {
		t.Fatalf("description not updated")
	}

	if aws.ToString(descAfter.Id) != id || !descAfter.CreatedAt.Equal(*desc.CreatedAt) {
		t.Fatalf("id/createdAt drifted after update")
	}

	// Delete the data source, then the index.
	if _, err = c.DeleteDataSource(ctx, &awskendra.DeleteDataSourceInput{
		Id: aws.String(dsID), IndexId: aws.String(id),
	}); err != nil {
		t.Fatalf("DeleteDataSource: %v", err)
	}

	if _, err = c.DeleteIndex(ctx, &awskendra.DeleteIndexInput{Id: aws.String(id)}); err != nil {
		t.Fatalf("DeleteIndex: %v", err)
	}

	_, err = c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String(id)})
	var nfe *kendratypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException after delete, got %T: %v", err, err)
	}
}

func TestSDKListIndices(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	if _, err := c.CreateIndex(ctx, &awskendra.CreateIndexInput{
		Name: aws.String("docs"), Edition: kendratypes.IndexEditionDeveloperEdition, RoleArn: aws.String(roleArn),
	}); err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	list, err := c.ListIndices(ctx, &awskendra.ListIndicesInput{})
	if err != nil {
		t.Fatalf("ListIndices: %v", err)
	}

	if len(list.IndexConfigurationSummaryItems) != 1 {
		t.Fatalf("expected 1 index summary, got %d", len(list.IndexConfigurationSummaryItems))
	}
}

func TestSDKResourceNotFoundException(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.DescribeIndex(ctx, &awskendra.DescribeIndexInput{Id: aws.String("00000000-0000-4000-8000-000000000000")})
	if err == nil {
		t.Fatalf("expected an error for a missing index")
	}

	var nfe *kendratypes.ResourceNotFoundException
	if !errors.As(err, &nfe) {
		t.Fatalf("expected ResourceNotFoundException, got %T: %v", err, err)
	}
}

func TestSDKValidationExceptionOnCustomWithRole(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	create, err := c.CreateIndex(ctx, &awskendra.CreateIndexInput{
		Name: aws.String("docs"), Edition: kendratypes.IndexEditionDeveloperEdition, RoleArn: aws.String(roleArn),
	})
	if err != nil {
		t.Fatalf("CreateIndex: %v", err)
	}

	// A CUSTOM data source must not carry a RoleArn.
	_, err = c.CreateDataSource(ctx, &awskendra.CreateDataSourceInput{
		IndexId: create.Id, Name: aws.String("ds"), Type: kendratypes.DataSourceTypeCustom, RoleArn: aws.String(roleArn),
	})

	var ve *kendratypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationException, got %T: %v", err, err)
	}
}
