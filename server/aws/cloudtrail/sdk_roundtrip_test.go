package cloudtrail_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsct "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

func newCTClient(t *testing.T) *awsct.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{CloudTrail: cloud.CloudTrail})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return awsct.NewFromConfig(cfg, func(o *awsct.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
	})
}

func TestSDKTrailLifecycle(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	create, err := c.CreateTrail(ctx, &awsct.CreateTrailInput{
		Name:                    aws.String("my-trail"),
		S3BucketName:            aws.String("my-bucket"),
		IsMultiRegionTrail:      aws.Bool(true),
		EnableLogFileValidation: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("CreateTrail: %v", err)
	}

	if !strings.Contains(aws.ToString(create.TrailARN), ":cloudtrail:") {
		t.Fatalf("unexpected ARN: %s", aws.ToString(create.TrailARN))
	}

	get, err := c.GetTrail(ctx, &awsct.GetTrailInput{Name: aws.String("my-trail")})
	if err != nil {
		t.Fatalf("GetTrail: %v", err)
	}

	if aws.ToString(get.Trail.S3BucketName) != "my-bucket" {
		t.Fatalf("bucket = %s", aws.ToString(get.Trail.S3BucketName))
	}

	if !aws.ToBool(get.Trail.IsMultiRegionTrail) {
		t.Fatal("expected multi-region trail")
	}

	// Update.
	if _, err := c.UpdateTrail(ctx, &awsct.UpdateTrailInput{
		Name: aws.String("my-trail"), S3KeyPrefix: aws.String("logs/"),
	}); err != nil {
		t.Fatalf("UpdateTrail: %v", err)
	}

	// Logging lifecycle reflects in GetTrailStatus.
	status, _ := c.GetTrailStatus(ctx, &awsct.GetTrailStatusInput{Name: aws.String("my-trail")})
	if aws.ToBool(status.IsLogging) {
		t.Fatal("trail should not be logging before StartLogging")
	}

	if _, err := c.StartLogging(ctx, &awsct.StartLoggingInput{Name: aws.String("my-trail")}); err != nil {
		t.Fatalf("StartLogging: %v", err)
	}

	status, _ = c.GetTrailStatus(ctx, &awsct.GetTrailStatusInput{Name: aws.String("my-trail")})
	if !aws.ToBool(status.IsLogging) {
		t.Fatal("trail should be logging after StartLogging")
	}

	if _, err := c.StopLogging(ctx, &awsct.StopLoggingInput{Name: aws.String("my-trail")}); err != nil {
		t.Fatalf("StopLogging: %v", err)
	}

	status, _ = c.GetTrailStatus(ctx, &awsct.GetTrailStatusInput{Name: aws.String("my-trail")})
	if aws.ToBool(status.IsLogging) {
		t.Fatal("trail should not be logging after StopLogging")
	}

	// ListTrails / DescribeTrails.
	list, err := c.ListTrails(ctx, &awsct.ListTrailsInput{})
	if err != nil {
		t.Fatalf("ListTrails: %v", err)
	}

	if len(list.Trails) != 1 {
		t.Fatalf("want 1 trail, got %d", len(list.Trails))
	}

	desc, err := c.DescribeTrails(ctx, &awsct.DescribeTrailsInput{})
	if err != nil {
		t.Fatalf("DescribeTrails: %v", err)
	}

	if len(desc.TrailList) != 1 {
		t.Fatalf("want 1 in DescribeTrails, got %d", len(desc.TrailList))
	}

	if _, err := c.DeleteTrail(ctx, &awsct.DeleteTrailInput{Name: aws.String("my-trail")}); err != nil {
		t.Fatalf("DeleteTrail: %v", err)
	}
}

func TestSDKTrailDuplicate(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	in := &awsct.CreateTrailInput{Name: aws.String("dup-trail"), S3BucketName: aws.String("b")}
	if _, err := c.CreateTrail(ctx, in); err != nil {
		t.Fatalf("first CreateTrail: %v", err)
	}

	_, err := c.CreateTrail(ctx, in)
	if err == nil {
		t.Fatal("expected duplicate error")
	}

	var dup *cttypes.TrailAlreadyExistsException
	if !errors.As(err, &dup) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want TrailAlreadyExistsException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want TrailAlreadyExistsException, got %v", err)
	}
}

func TestSDKTrailInvalidName(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	_, err := c.CreateTrail(ctx, &awsct.CreateTrailInput{Name: aws.String("a"), S3BucketName: aws.String("b")})
	if err == nil {
		t.Fatal("expected invalid name error")
	}

	var inv *cttypes.InvalidTrailNameException
	if !errors.As(err, &inv) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want InvalidTrailNameException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want InvalidTrailNameException, got %v", err)
	}
}

func TestSDKGetTrailNotFound(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	_, err := c.GetTrailStatus(ctx, &awsct.GetTrailStatusInput{Name: aws.String("missing-trail")})
	if err == nil {
		t.Fatal("expected not-found error")
	}

	var nf *cttypes.TrailNotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want TrailNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want TrailNotFoundException, got %v", err)
	}
}

func TestSDKEventDataStoreCRUD(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	create, err := c.CreateEventDataStore(ctx, &awsct.CreateEventDataStoreInput{
		Name:                         aws.String("my-eds"),
		TerminationProtectionEnabled: aws.Bool(false),
		RetentionPeriod:              aws.Int32(90),
	})
	if err != nil {
		t.Fatalf("CreateEventDataStore: %v", err)
	}

	arn := aws.ToString(create.EventDataStoreArn)
	if !strings.Contains(arn, ":eventdatastore/") {
		t.Fatalf("unexpected EDS ARN: %s", arn)
	}

	if create.Status != cttypes.EventDataStoreStatusEnabled {
		t.Fatalf("EDS status = %s, want ENABLED", create.Status)
	}

	get, err := c.GetEventDataStore(ctx, &awsct.GetEventDataStoreInput{EventDataStore: aws.String(arn)})
	if err != nil {
		t.Fatalf("GetEventDataStore: %v", err)
	}

	if aws.ToInt32(get.RetentionPeriod) != 90 {
		t.Fatalf("retention = %d", aws.ToInt32(get.RetentionPeriod))
	}

	if _, err := c.UpdateEventDataStore(ctx, &awsct.UpdateEventDataStoreInput{
		EventDataStore: aws.String(arn), RetentionPeriod: aws.Int32(120),
	}); err != nil {
		t.Fatalf("UpdateEventDataStore: %v", err)
	}

	listEDS, err := c.ListEventDataStores(ctx, &awsct.ListEventDataStoresInput{})
	if err != nil {
		t.Fatalf("ListEventDataStores: %v", err)
	}

	if len(listEDS.EventDataStores) != 1 {
		t.Fatalf("want 1 EDS, got %d", len(listEDS.EventDataStores))
	}

	if _, err := c.DeleteEventDataStore(ctx, &awsct.DeleteEventDataStoreInput{
		EventDataStore: aws.String(arn),
	}); err != nil {
		t.Fatalf("DeleteEventDataStore: %v", err)
	}
}

// TestSDKRestoreInvalidStatus verifies restoring an ENABLED (non-pending) store
// surfaces the typed InvalidEventDataStoreStatusException over the wire.
func TestSDKRestoreInvalidStatus(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	create, err := c.CreateEventDataStore(ctx, &awsct.CreateEventDataStoreInput{
		Name:                         aws.String("restore-eds"),
		TerminationProtectionEnabled: aws.Bool(false),
	})
	if err != nil {
		t.Fatalf("CreateEventDataStore: %v", err)
	}

	arn := aws.ToString(create.EventDataStoreArn)

	_, err = c.RestoreEventDataStore(ctx, &awsct.RestoreEventDataStoreInput{
		EventDataStore: aws.String(arn),
	})
	if err == nil {
		t.Fatal("expected InvalidEventDataStoreStatusException")
	}

	var inv *cttypes.InvalidEventDataStoreStatusException
	if !errors.As(err, &inv) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want InvalidEventDataStoreStatusException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want InvalidEventDataStoreStatusException, got %v", err)
	}
}

func TestSDKEventDataStoreNotFound(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	arn := "arn:aws:cloudtrail:us-east-1:000000000000:eventdatastore/missing"
	_, err := c.GetEventDataStore(ctx, &awsct.GetEventDataStoreInput{EventDataStore: aws.String(arn)})
	if err == nil {
		t.Fatal("expected not-found error")
	}

	var nf *cttypes.EventDataStoreNotFoundException
	if !errors.As(err, &nf) {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			t.Fatalf("want EventDataStoreNotFoundException, got %q", apiErr.ErrorCode())
		}

		t.Fatalf("want EventDataStoreNotFoundException, got %v", err)
	}
}

func TestSDKTags(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	create, err := c.CreateTrail(ctx, &awsct.CreateTrailInput{
		Name: aws.String("tag-trail"), S3BucketName: aws.String("b"),
	})
	if err != nil {
		t.Fatalf("CreateTrail: %v", err)
	}

	arn := create.TrailARN

	if _, err := c.AddTags(ctx, &awsct.AddTagsInput{
		ResourceId: arn, TagsList: []cttypes.Tag{{Key: aws.String("env"), Value: aws.String("prod")}},
	}); err != nil {
		t.Fatalf("AddTags: %v", err)
	}

	list, err := c.ListTags(ctx, &awsct.ListTagsInput{ResourceIdList: []string{aws.ToString(arn)}})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}

	if len(list.ResourceTagList) != 1 || len(list.ResourceTagList[0].TagsList) != 1 {
		t.Fatalf("unexpected tags: %+v", list.ResourceTagList)
	}

	if _, err := c.RemoveTags(ctx, &awsct.RemoveTagsInput{
		ResourceId: arn, TagsList: []cttypes.Tag{{Key: aws.String("env")}},
	}); err != nil {
		t.Fatalf("RemoveTags: %v", err)
	}
}

func TestSDKEventSelectors(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	if _, err := c.CreateTrail(ctx, &awsct.CreateTrailInput{
		Name: aws.String("sel-trail"), S3BucketName: aws.String("b"),
	}); err != nil {
		t.Fatalf("CreateTrail: %v", err)
	}

	put, err := c.PutEventSelectors(ctx, &awsct.PutEventSelectorsInput{
		TrailName: aws.String("sel-trail"),
		EventSelectors: []cttypes.EventSelector{{
			ReadWriteType:           cttypes.ReadWriteTypeAll,
			IncludeManagementEvents: aws.Bool(true),
		}},
	})
	if err != nil {
		t.Fatalf("PutEventSelectors: %v", err)
	}

	if len(put.EventSelectors) != 1 {
		t.Fatalf("want 1 selector back, got %d", len(put.EventSelectors))
	}

	get, err := c.GetEventSelectors(ctx, &awsct.GetEventSelectorsInput{TrailName: aws.String("sel-trail")})
	if err != nil {
		t.Fatalf("GetEventSelectors: %v", err)
	}

	if len(get.EventSelectors) != 1 || get.EventSelectors[0].ReadWriteType != cttypes.ReadWriteTypeAll {
		t.Fatalf("unexpected selectors: %+v", get.EventSelectors)
	}
}

func TestSDKLookupEventsEmpty(t *testing.T) {
	ctx := context.Background()
	c := newCTClient(t)

	out, err := c.LookupEvents(ctx, &awsct.LookupEventsInput{})
	if err != nil {
		t.Fatalf("LookupEvents: %v", err)
	}

	if len(out.Events) != 0 {
		t.Fatalf("expected synthesized empty events, got %d", len(out.Events))
	}
}
