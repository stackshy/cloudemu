package healthlake_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awshl "github.com/aws/aws-sdk-go-v2/service/healthlake"
	hltypes "github.com/aws/aws-sdk-go-v2/service/healthlake/types"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newClient(t *testing.T) *awshl.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{HealthLake: cloud.HealthLake})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awshl.NewFromConfig(cfg, func(o *awshl.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKDatastoreLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	const name = "clinical"

	create, err := c.CreateFHIRDatastore(ctx, &awshl.CreateFHIRDatastoreInput{
		DatastoreName:        aws.String(name),
		DatastoreTypeVersion: hltypes.FHIRVersionR4,
	})
	if err != nil {
		t.Fatalf("CreateFHIRDatastore: %v", err)
	}

	id := aws.ToString(create.DatastoreId)
	if id == "" {
		t.Fatalf("expected a datastore id")
	}

	// A data store is ACTIVE synchronously, so an IaC waiter never hangs.
	if create.DatastoreStatus != hltypes.DatastoreStatusActive {
		t.Fatalf("create status = %q, want ACTIVE", create.DatastoreStatus)
	}

	wantArn := "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/" + id
	if aws.ToString(create.DatastoreArn) != wantArn {
		t.Fatalf("datastore arn = %q, want %q", aws.ToString(create.DatastoreArn), wantArn)
	}

	wantEndpoint := "https://healthlake.us-east-1.amazonaws.com/datastore/" + id + "/r4/"
	if aws.ToString(create.DatastoreEndpoint) != wantEndpoint {
		t.Fatalf("datastore endpoint = %q, want %q", aws.ToString(create.DatastoreEndpoint), wantEndpoint)
	}

	// The computed fields are byte-stable across a Describe read.
	desc, err := c.DescribeFHIRDatastore(ctx, &awshl.DescribeFHIRDatastoreInput{DatastoreId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeFHIRDatastore: %v", err)
	}

	props := desc.DatastoreProperties
	if aws.ToString(props.DatastoreArn) != wantArn ||
		aws.ToString(props.DatastoreEndpoint) != wantEndpoint ||
		aws.ToString(props.DatastoreName) != name ||
		props.DatastoreStatus != hltypes.DatastoreStatusActive ||
		props.DatastoreTypeVersion != hltypes.FHIRVersionR4 {
		t.Fatalf("computed datastore fields drifted across reads: %+v", props)
	}

	if props.CreatedAt == nil {
		t.Fatalf("expected a createdAt timestamp")
	}

	// An unspecified SSE config defaults to an AWS-owned KMS key, matching real
	// HealthLake.
	if props.SseConfiguration == nil || props.SseConfiguration.KmsEncryptionConfig == nil ||
		props.SseConfiguration.KmsEncryptionConfig.CmkType != hltypes.CmkTypeAoCmk {
		t.Fatalf("expected AWS-owned KMS SSE config, got %+v", props.SseConfiguration)
	}

	// ListFHIRDatastores returns the data store.
	list, err := c.ListFHIRDatastores(ctx, &awshl.ListFHIRDatastoresInput{})
	if err != nil {
		t.Fatalf("ListFHIRDatastores: %v", err)
	}

	if len(list.DatastorePropertiesList) != 1 || aws.ToString(list.DatastorePropertiesList[0].DatastoreId) != id {
		t.Fatalf("list = %d stores, want 1 with id %q", len(list.DatastorePropertiesList), id)
	}

	// DeleteFHIRDatastore reports DELETED and a subsequent describe 404s.
	del, err := c.DeleteFHIRDatastore(ctx, &awshl.DeleteFHIRDatastoreInput{DatastoreId: aws.String(id)})
	if err != nil {
		t.Fatalf("DeleteFHIRDatastore: %v", err)
	}

	if del.DatastoreStatus != hltypes.DatastoreStatusDeleted {
		t.Fatalf("delete status = %q, want DELETED", del.DatastoreStatus)
	}

	_, err = c.DescribeFHIRDatastore(ctx, &awshl.DescribeFHIRDatastoreInput{DatastoreId: aws.String(id)})

	var notFound *hltypes.ResourceNotFoundException
	if !errors.As(err, &notFound) {
		t.Fatalf("describe after delete err = %v, want ResourceNotFoundException", err)
	}
}

func TestSDKCustomerManagedKeyAndTags(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	const kmsKey = "arn:aws:kms:us-east-1:123456789012:key/abc-123"

	create, err := c.CreateFHIRDatastore(ctx, &awshl.CreateFHIRDatastoreInput{
		DatastoreName:        aws.String("encrypted"),
		DatastoreTypeVersion: hltypes.FHIRVersionR4,
		SseConfiguration: &hltypes.SseConfiguration{
			KmsEncryptionConfig: &hltypes.KmsEncryptionConfig{
				CmkType:  hltypes.CmkTypeCmCmk,
				KmsKeyId: aws.String(kmsKey),
			},
		},
		Tags: []hltypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	})
	if err != nil {
		t.Fatalf("CreateFHIRDatastore: %v", err)
	}

	id := aws.ToString(create.DatastoreId)
	arn := aws.ToString(create.DatastoreArn)

	// The customer-managed key round-trips verbatim.
	desc, err := c.DescribeFHIRDatastore(ctx, &awshl.DescribeFHIRDatastoreInput{DatastoreId: aws.String(id)})
	if err != nil {
		t.Fatalf("DescribeFHIRDatastore: %v", err)
	}

	kec := desc.DatastoreProperties.SseConfiguration.KmsEncryptionConfig
	if kec.CmkType != hltypes.CmkTypeCmCmk || aws.ToString(kec.KmsKeyId) != kmsKey {
		t.Fatalf("customer-managed key drifted: %+v", kec)
	}

	// Create-time tags are readable, and TagResource/UntagResource round-trip.
	tags, err := c.ListTagsForResource(ctx, &awshl.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(tags.Tags) != 1 || aws.ToString(tags.Tags[0].Key) != "env" {
		t.Fatalf("create tags = %+v, want env=prod", tags.Tags)
	}

	if _, err := c.TagResource(ctx, &awshl.TagResourceInput{
		ResourceARN: aws.String(arn),
		Tags:        []hltypes.Tag{{Key: aws.String("team"), Value: aws.String("data")}},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	if _, err := c.UntagResource(ctx, &awshl.UntagResourceInput{
		ResourceARN: aws.String(arn),
		TagKeys:     []string{"env"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	after, err := c.ListTagsForResource(ctx, &awshl.ListTagsForResourceInput{ResourceARN: aws.String(arn)})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if len(after.Tags) != 1 || aws.ToString(after.Tags[0].Key) != "team" {
		t.Fatalf("tags after mutation = %+v, want only team=data", after.Tags)
	}
}

func TestSDKCreateRejectsNonR4(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	_, err := c.CreateFHIRDatastore(ctx, &awshl.CreateFHIRDatastoreInput{
		DatastoreName:        aws.String("bad"),
		DatastoreTypeVersion: hltypes.FHIRVersion("R5"),
	})

	var ve *hltypes.ValidationException
	if !errors.As(err, &ve) {
		t.Fatalf("create with R5 err = %v, want ValidationException", err)
	}
}
