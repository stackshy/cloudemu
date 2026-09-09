package location_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/location"
	"github.com/stackshy/cloudemu/v2/services/location/driver"
)

func newMock(clock config.Clock) *location.Mock {
	if clock == nil {
		return location.New(config.NewOptions())
	}

	return location.New(config.NewOptions(config.WithClock(clock)))
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createMap(t *testing.T, m *location.Mock, name string) *driver.MapInfo {
	t.Helper()

	out, err := m.CreateMap(context.Background(), &driver.CreateMapInput{
		MapName:       name,
		Configuration: driver.MapConfiguration{Style: "VectorEsriStreets"},
		Description:   "first",
		Tags:          map[string]string{"env": "test"},
	})
	requireNoError(t, err)

	return out
}

func TestCreateMapComputedFieldsAndDataSource(t *testing.T) {
	m := newMock(nil)
	got := createMap(t, m, "cam")

	if !strings.HasSuffix(got.Arn, ":map/cam") || !strings.HasPrefix(got.Arn, "arn:aws:geo:") {
		t.Fatalf("unexpected ARN: %q", got.Arn)
	}

	if got.DataSource != driver.DataSourceEsri {
		t.Fatalf("DataSource = %q, want Esri", got.DataSource)
	}

	if got.CreateTime.IsZero() || !got.CreateTime.Equal(got.UpdateTime) {
		t.Fatal("CreateTime/UpdateTime not set consistently at create")
	}
}

func TestMapUpdateBumpsUpdateTimeOnly(t *testing.T) {
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := config.NewFakeClock(base)
	m := newMock(clock)

	created := createMap(t, m, "cam")

	clock.Advance(time.Hour)

	desc := "second"
	updated, err := m.UpdateMap(context.Background(), &driver.UpdateMapInput{MapName: "cam", Description: &desc})
	requireNoError(t, err)

	if updated.Arn != created.Arn || !updated.CreateTime.Equal(created.CreateTime) {
		t.Fatal("ARN or CreateTime drifted after update")
	}

	if !updated.UpdateTime.Equal(base.Add(time.Hour)) {
		t.Fatalf("UpdateTime = %v, want %v", updated.UpdateTime, base.Add(time.Hour))
	}

	if updated.Description != "second" {
		t.Fatalf("Description = %q, want second", updated.Description)
	}
}

func TestDescribeMapStableAcrossReads(t *testing.T) {
	m := newMock(nil)
	createMap(t, m, "cam")

	first, err := m.DescribeMap(context.Background(), "cam")
	requireNoError(t, err)

	second, err := m.DescribeMap(context.Background(), "cam")
	requireNoError(t, err)

	if first.Arn != second.Arn || !first.CreateTime.Equal(second.CreateTime) ||
		!first.UpdateTime.Equal(second.UpdateTime) {
		t.Fatal("computed fields drifted across reads")
	}
}

func TestDescribeMapCloneIsolation(t *testing.T) {
	m := newMock(nil)
	createMap(t, m, "cam")

	got, err := m.DescribeMap(context.Background(), "cam")
	requireNoError(t, err)

	got.Tags["env"] = "mutated"

	again, err := m.DescribeMap(context.Background(), "cam")
	requireNoError(t, err)

	if again.Tags["env"] != "test" {
		t.Fatalf("stored tags mutated through returned clone: %v", again.Tags)
	}
}

func TestDuplicateCreateConflict(t *testing.T) {
	m := newMock(nil)
	createMap(t, m, "cam")

	_, err := m.CreateMap(context.Background(), &driver.CreateMapInput{
		MapName:       "cam",
		Configuration: driver.MapConfiguration{Style: "VectorEsriStreets"},
	})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExConflict {
		t.Fatalf("duplicate create: got %v, want ConflictException", err)
	}
}

func TestDescribeMissingNotFound(t *testing.T) {
	m := newMock(nil)

	_, err := m.DescribeMap(context.Background(), "ghost")

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExResourceNotFound {
		t.Fatalf("describe missing: got %v, want ResourceNotFoundException", err)
	}
}

func TestPlaceIndexIntendedUseDefault(t *testing.T) {
	m := newMock(nil)

	out, err := m.CreatePlaceIndex(context.Background(), &driver.CreatePlaceIndexInput{
		IndexName:  "idx",
		DataSource: driver.DataSourceEsri,
	})
	requireNoError(t, err)

	if out.DataSourceConfiguration.IntendedUse != driver.IntendedUseSingleUse {
		t.Fatalf("IntendedUse = %q, want SingleUse", out.DataSourceConfiguration.IntendedUse)
	}
}

func TestTrackerPositionFilteringDefault(t *testing.T) {
	m := newMock(nil)

	out, err := m.CreateTracker(context.Background(), &driver.CreateTrackerInput{TrackerName: "trk"})
	requireNoError(t, err)

	if out.PositionFiltering != driver.PositionFilteringTimeBased {
		t.Fatalf("PositionFiltering = %q, want TimeBased", out.PositionFiltering)
	}
}

func TestTagsAcrossStoresByARN(t *testing.T) {
	m := newMock(nil)
	ctx := context.Background()

	coll, err := m.CreateGeofenceCollection(ctx, &driver.CreateGeofenceCollectionInput{CollectionName: "col"})
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, coll.Arn, map[string]string{"team": "geo"}))

	tags, err := m.ListTagsForResource(ctx, coll.Arn)
	requireNoError(t, err)

	if tags["team"] != "geo" {
		t.Fatalf("tags = %v, want team=geo", tags)
	}

	requireNoError(t, m.UntagResource(ctx, coll.Arn, []string{"team"}))

	tags, err = m.ListTagsForResource(ctx, coll.Arn)
	requireNoError(t, err)

	if len(tags) != 0 {
		t.Fatalf("tags after untag = %v, want empty", tags)
	}
}

func TestTagInvalidARN(t *testing.T) {
	m := newMock(nil)

	err := m.TagResource(context.Background(), "not-an-arn", map[string]string{"a": "b"})

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExValidation {
		t.Fatalf("tag invalid ARN: got %v, want ValidationException", err)
	}
}

func TestListPagination(t *testing.T) {
	m := newMock(nil)
	ctx := context.Background()

	for _, n := range []string{"a", "b", "c"} {
		createMap(t, m, n)
	}

	page1, next, err := m.ListMaps(ctx, driver.Page{MaxResults: 2})
	requireNoError(t, err)

	if len(page1) != 2 || next == "" {
		t.Fatalf("page1 len=%d next=%q, want 2 and a token", len(page1), next)
	}

	page2, next2, err := m.ListMaps(ctx, driver.Page{MaxResults: 2, NextToken: next})
	requireNoError(t, err)

	if len(page2) != 1 || next2 != "" {
		t.Fatalf("page2 len=%d next=%q, want 1 and empty", len(page2), next2)
	}
}

func TestDeleteThenNotFound(t *testing.T) {
	m := newMock(nil)
	ctx := context.Background()
	createMap(t, m, "cam")

	requireNoError(t, m.DeleteMap(ctx, "cam"))

	if err := m.DeleteMap(ctx, "cam"); err == nil {
		t.Fatal("second delete should fail with not found")
	}
}
