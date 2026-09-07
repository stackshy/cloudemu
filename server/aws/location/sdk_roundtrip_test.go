package location_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	loc "github.com/aws/aws-sdk-go-v2/service/location"
	loctypes "github.com/aws/aws-sdk-go-v2/service/location/types"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/stackshy/cloudemu/v2"
	awsserver "github.com/stackshy/cloudemu/v2/server/aws"
)

// disableHostPrefix turns off the Location per-operation control-plane host
// prefix (cp.maps., cp.places., …), which a single httptest host cannot serve.
// The generated host-prefix middleware honors this context flag.
func disableHostPrefix(stack *middleware.Stack) error {
	return stack.Initialize.Add(
		middleware.InitializeMiddlewareFunc("disableHostPrefix",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error,
			) {
				return next.HandleInitialize(smithyhttp.DisableEndpointHostPrefix(ctx, true), in)
			}),
		middleware.Before,
	)
}

func newClient(t *testing.T) *loc.Client {
	t.Helper()

	cloud := cloudemu.NewAWS()
	srv := awsserver.New(awsserver.Drivers{Location: cloud.Location})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}

	return loc.NewFromConfig(cfg, func(o *loc.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.APIOptions = append(o.APIOptions, disableHostPrefix)
	})
}

func TestMapLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "example-map"

	created, err := c.CreateMap(ctx, &loc.CreateMapInput{
		MapName:       aws.String(name),
		Configuration: &loctypes.MapConfiguration{Style: aws.String("VectorEsriStreets")},
		Description:   aws.String("first"),
		Tags:          map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	wantARN := "arn:aws:geo:us-east-1:"
	if !strings.HasPrefix(aws.ToString(created.MapArn), wantARN) ||
		!strings.HasSuffix(aws.ToString(created.MapArn), ":map/"+name) {
		t.Fatalf("unexpected map ARN: %q", aws.ToString(created.MapArn))
	}

	if created.CreateTime == nil {
		t.Fatal("CreateTime is nil")
	}

	first, err := c.DescribeMap(ctx, &loc.DescribeMapInput{MapName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeMap: %v", err)
	}

	if aws.ToString(first.Configuration.Style) != "VectorEsriStreets" {
		t.Fatalf("style = %q, want VectorEsriStreets", aws.ToString(first.Configuration.Style))
	}

	if aws.ToString(first.DataSource) != "Esri" {
		t.Fatalf("DataSource = %q, want Esri", aws.ToString(first.DataSource))
	}

	// Computed fields are stable across repeated reads.
	second, err := c.DescribeMap(ctx, &loc.DescribeMapInput{MapName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeMap#2: %v", err)
	}

	if aws.ToString(first.MapArn) != aws.ToString(second.MapArn) ||
		!first.CreateTime.Equal(*second.CreateTime) ||
		!first.UpdateTime.Equal(*second.UpdateTime) {
		t.Fatal("map computed fields drifted across reads")
	}

	// Update bumps UpdateTime, preserves ARN and CreateTime.
	upd, err := c.UpdateMap(ctx, &loc.UpdateMapInput{MapName: aws.String(name), Description: aws.String("second")})
	if err != nil {
		t.Fatalf("UpdateMap: %v", err)
	}

	if aws.ToString(upd.MapArn) != aws.ToString(first.MapArn) {
		t.Fatal("map ARN drifted after update")
	}

	after, err := c.DescribeMap(ctx, &loc.DescribeMapInput{MapName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeMap after update: %v", err)
	}

	if aws.ToString(after.Description) != "second" {
		t.Fatalf("description = %q, want second", aws.ToString(after.Description))
	}

	if !after.CreateTime.Equal(*first.CreateTime) || after.UpdateTime.Before(*first.CreateTime) {
		t.Fatal("CreateTime drifted or UpdateTime not bumped after update")
	}

	if _, err = c.DeleteMap(ctx, &loc.DeleteMapInput{MapName: aws.String(name)}); err != nil {
		t.Fatalf("DeleteMap: %v", err)
	}

	_, err = c.DescribeMap(ctx, &loc.DescribeMapInput{MapName: aws.String(name)})

	var nf *loctypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribeMap after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestPlaceIndexLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "example-index"

	created, err := c.CreatePlaceIndex(ctx, &loc.CreatePlaceIndexInput{
		IndexName:  aws.String(name),
		DataSource: aws.String("Esri"),
	})
	if err != nil {
		t.Fatalf("CreatePlaceIndex: %v", err)
	}

	if !strings.HasSuffix(aws.ToString(created.IndexArn), ":place-index/"+name) {
		t.Fatalf("unexpected index ARN: %q", aws.ToString(created.IndexArn))
	}

	desc, err := c.DescribePlaceIndex(ctx, &loc.DescribePlaceIndexInput{IndexName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribePlaceIndex: %v", err)
	}

	if aws.ToString(desc.DataSource) != "Esri" {
		t.Fatalf("DataSource = %q, want Esri", aws.ToString(desc.DataSource))
	}

	// IntendedUse defaults to SingleUse when the caller omits it.
	if desc.DataSourceConfiguration == nil || string(desc.DataSourceConfiguration.IntendedUse) != "SingleUse" {
		t.Fatalf("IntendedUse = %v, want SingleUse", desc.DataSourceConfiguration)
	}

	if _, err = c.DeletePlaceIndex(ctx, &loc.DeletePlaceIndexInput{IndexName: aws.String(name)}); err != nil {
		t.Fatalf("DeletePlaceIndex: %v", err)
	}

	_, err = c.DescribePlaceIndex(ctx, &loc.DescribePlaceIndexInput{IndexName: aws.String(name)})

	var nf *loctypes.ResourceNotFoundException
	if !errors.As(err, &nf) {
		t.Fatalf("DescribePlaceIndex after delete: got %v, want ResourceNotFoundException", err)
	}
}

func TestRouteCalculatorLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "example-calc"

	created, err := c.CreateRouteCalculator(ctx, &loc.CreateRouteCalculatorInput{
		CalculatorName: aws.String(name),
		DataSource:     aws.String("Esri"),
	})
	if err != nil {
		t.Fatalf("CreateRouteCalculator: %v", err)
	}

	if !strings.HasSuffix(aws.ToString(created.CalculatorArn), ":route-calculator/"+name) {
		t.Fatalf("unexpected calculator ARN: %q", aws.ToString(created.CalculatorArn))
	}

	desc, err := c.DescribeRouteCalculator(ctx, &loc.DescribeRouteCalculatorInput{CalculatorName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeRouteCalculator: %v", err)
	}

	if aws.ToString(desc.DataSource) != "Esri" {
		t.Fatalf("DataSource = %q, want Esri", aws.ToString(desc.DataSource))
	}

	if _, err = c.DeleteRouteCalculator(ctx, &loc.DeleteRouteCalculatorInput{CalculatorName: aws.String(name)}); err != nil {
		t.Fatalf("DeleteRouteCalculator: %v", err)
	}
}

func TestGeofenceCollectionLifecycle(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "example-collection"

	created, err := c.CreateGeofenceCollection(ctx, &loc.CreateGeofenceCollectionInput{
		CollectionName: aws.String(name),
		Description:    aws.String("geo"),
		KmsKeyId:       aws.String("alias/key"),
	})
	if err != nil {
		t.Fatalf("CreateGeofenceCollection: %v", err)
	}

	if !strings.HasSuffix(aws.ToString(created.CollectionArn), ":geofence-collection/"+name) {
		t.Fatalf("unexpected collection ARN: %q", aws.ToString(created.CollectionArn))
	}

	desc, err := c.DescribeGeofenceCollection(ctx, &loc.DescribeGeofenceCollectionInput{CollectionName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeGeofenceCollection: %v", err)
	}

	if aws.ToString(desc.KmsKeyId) != "alias/key" {
		t.Fatalf("KmsKeyId = %q, want alias/key", aws.ToString(desc.KmsKeyId))
	}

	if _, err = c.DeleteGeofenceCollection(ctx,
		&loc.DeleteGeofenceCollectionInput{CollectionName: aws.String(name)}); err != nil {
		t.Fatalf("DeleteGeofenceCollection: %v", err)
	}
}

func TestTrackerLifecycleAndTags(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()
	name := "example-tracker"

	created, err := c.CreateTracker(ctx, &loc.CreateTrackerInput{
		TrackerName: aws.String(name),
		Tags:        map[string]string{"env": "test"},
	})
	if err != nil {
		t.Fatalf("CreateTracker: %v", err)
	}

	if !strings.HasSuffix(aws.ToString(created.TrackerArn), ":tracker/"+name) {
		t.Fatalf("unexpected tracker ARN: %q", aws.ToString(created.TrackerArn))
	}

	desc, err := c.DescribeTracker(ctx, &loc.DescribeTrackerInput{TrackerName: aws.String(name)})
	if err != nil {
		t.Fatalf("DescribeTracker: %v", err)
	}

	// PositionFiltering defaults to TimeBased.
	if string(desc.PositionFiltering) != "TimeBased" {
		t.Fatalf("PositionFiltering = %q, want TimeBased", desc.PositionFiltering)
	}

	// Tag operations reach the resource through the shared /tags/{arn} path.
	if _, err = c.TagResource(ctx, &loc.TagResourceInput{
		ResourceArn: created.TrackerArn,
		Tags:        map[string]string{"team": "geo"},
	}); err != nil {
		t.Fatalf("TagResource: %v", err)
	}

	tags, err := c.ListTagsForResource(ctx, &loc.ListTagsForResourceInput{ResourceArn: created.TrackerArn})
	if err != nil {
		t.Fatalf("ListTagsForResource: %v", err)
	}

	if tags.Tags["env"] != "test" || tags.Tags["team"] != "geo" {
		t.Fatalf("tags = %v, want env=test team=geo", tags.Tags)
	}

	if _, err = c.UntagResource(ctx, &loc.UntagResourceInput{
		ResourceArn: created.TrackerArn,
		TagKeys:     []string{"team"},
	}); err != nil {
		t.Fatalf("UntagResource: %v", err)
	}

	tags2, err := c.ListTagsForResource(ctx, &loc.ListTagsForResourceInput{ResourceArn: created.TrackerArn})
	if err != nil {
		t.Fatalf("ListTagsForResource#2: %v", err)
	}

	if _, ok := tags2.Tags["team"]; ok {
		t.Fatalf("team tag not removed: %v", tags2.Tags)
	}

	if _, err = c.DeleteTracker(ctx, &loc.DeleteTrackerInput{TrackerName: aws.String(name)}); err != nil {
		t.Fatalf("DeleteTracker: %v", err)
	}
}

func TestCreateMapDuplicateConflict(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	in := &loc.CreateMapInput{
		MapName:       aws.String("dup"),
		Configuration: &loctypes.MapConfiguration{Style: aws.String("VectorEsriStreets")},
	}
	if _, err := c.CreateMap(ctx, in); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	_, err := c.CreateMap(ctx, in)

	var conflict *loctypes.ConflictException
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate CreateMap: got %v, want ConflictException", err)
	}
}

func TestListMapsAndTrackers(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	for _, n := range []string{"alpha", "beta"} {
		if _, err := c.CreateMap(ctx, &loc.CreateMapInput{
			MapName:       aws.String(n),
			Configuration: &loctypes.MapConfiguration{Style: aws.String("VectorEsriStreets")},
		}); err != nil {
			t.Fatalf("CreateMap(%s): %v", n, err)
		}
	}

	maps, err := c.ListMaps(ctx, &loc.ListMapsInput{})
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}

	if len(maps.Entries) != 2 {
		t.Fatalf("ListMaps returned %d entries, want 2", len(maps.Entries))
	}

	trackers, err := c.ListTrackers(ctx, &loc.ListTrackersInput{})
	if err != nil {
		t.Fatalf("ListTrackers: %v", err)
	}

	if len(trackers.Entries) != 0 {
		t.Fatalf("ListTrackers returned %d entries, want 0", len(trackers.Entries))
	}
}
