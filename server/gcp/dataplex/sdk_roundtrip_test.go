package dataplex_test

import (
	"context"
	"net/http/httptest"
	"testing"

	dataplex "google.golang.org/api/dataplex/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newSDKClient(t *testing.T) (*dataplex.Service, string) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := dataplex.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dataplex.NewService: %v", err)
	}

	return svc, "mock-project"
}

// TestSDKHierarchyLifecycle drives the full lake → zone → asset hierarchy through
// the real google.golang.org/api/dataplex client: create (LRO must complete
// inline), poll the operation, GET each resource, verify the computed fields are
// present and stable across a second read (the Terraform drift point), patch, and
// cascade-delete.
func TestSDKHierarchyLifecycle(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()

	loc := "projects/" + project + "/locations/us-central1"
	lakeName := loc + "/lakes/lake1"
	zoneName := lakeName + "/zones/zone1"
	assetName := zoneName + "/assets/asset1"

	// --- Lake ---
	lop, err := svc.Projects.Locations.Lakes.Create(loc, &dataplex.GoogleCloudDataplexV1Lake{
		Description: "prod lake",
		DisplayName: "Prod Lake",
		Labels:      map[string]string{"team": "data"},
	}).LakeId("lake1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Lakes.Create: %v", err)
	}

	if !lop.Done {
		t.Fatalf("lake create operation not done (would hang a Terraform apply)")
	}

	if _, err := svc.Projects.Locations.Operations.Get(lop.Name).Context(ctx).Do(); err != nil {
		t.Fatalf("Operations.Get(lake): %v", err)
	}

	lake, err := svc.Projects.Locations.Lakes.Get(lakeName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Lakes.Get: %v", err)
	}

	if lake.Name != lakeName || lake.Uid == "" || lake.State != "ACTIVE" || lake.ServiceAccount == "" {
		t.Fatalf("lake computed fields wrong: %+v", lake)
	}

	if lake.CreateTime == "" || lake.UpdateTime == "" || lake.AssetStatus == nil {
		t.Fatalf("lake computed timestamps/status missing: %+v", lake)
	}

	if lake.Description != "prod lake" || lake.Labels["team"] != "data" {
		t.Fatalf("lake inputs not round-tripped: %+v", lake)
	}

	// Computed fields stable across reads (no Terraform drift).
	lake2, err := svc.Projects.Locations.Lakes.Get(lakeName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Lakes.Get(2): %v", err)
	}

	if lake2.Uid != lake.Uid || lake2.CreateTime != lake.CreateTime {
		t.Fatalf("lake computed drift: uid %q->%q createTime %q->%q", lake.Uid, lake2.Uid, lake.CreateTime, lake2.CreateTime)
	}

	// --- Zone ---
	zop, err := svc.Projects.Locations.Lakes.Zones.Create(lakeName, &dataplex.GoogleCloudDataplexV1Zone{
		Type:          "RAW",
		Description:   "raw zone",
		ResourceSpec:  &dataplex.GoogleCloudDataplexV1ZoneResourceSpec{LocationType: "SINGLE_REGION"},
		DiscoverySpec: &dataplex.GoogleCloudDataplexV1ZoneDiscoverySpec{Enabled: true},
	}).ZoneId("zone1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Zones.Create: %v", err)
	}

	if !zop.Done {
		t.Fatalf("zone create operation not done")
	}

	zone, err := svc.Projects.Locations.Lakes.Zones.Get(zoneName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Zones.Get: %v", err)
	}

	if zone.Name != zoneName || zone.Uid == "" || zone.State != "ACTIVE" || zone.Type != "RAW" {
		t.Fatalf("zone computed/input fields wrong: %+v", zone)
	}

	if zone.ResourceSpec == nil || zone.ResourceSpec.LocationType != "SINGLE_REGION" {
		t.Fatalf("zone resourceSpec not round-tripped: %+v", zone.ResourceSpec)
	}

	// --- Asset ---
	aop, err := svc.Projects.Locations.Lakes.Zones.Assets.Create(zoneName, &dataplex.GoogleCloudDataplexV1Asset{
		Description:   "bucket asset",
		ResourceSpec:  &dataplex.GoogleCloudDataplexV1AssetResourceSpec{Name: "projects/x/buckets/b", Type: "STORAGE_BUCKET"},
		DiscoverySpec: &dataplex.GoogleCloudDataplexV1AssetDiscoverySpec{Enabled: true},
	}).AssetId("asset1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Assets.Create: %v", err)
	}

	if !aop.Done {
		t.Fatalf("asset create operation not done")
	}

	asset, err := svc.Projects.Locations.Lakes.Zones.Assets.Get(assetName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Assets.Get: %v", err)
	}

	if asset.Name != assetName || asset.Uid == "" || asset.State != "ACTIVE" {
		t.Fatalf("asset computed fields wrong: %+v", asset)
	}

	if asset.ResourceStatus == nil || asset.SecurityStatus == nil || asset.DiscoveryStatus == nil {
		t.Fatalf("asset status blocks missing: %+v", asset)
	}

	if asset.ResourceSpec == nil || asset.ResourceSpec.Type != "STORAGE_BUCKET" {
		t.Fatalf("asset resourceSpec not round-tripped: %+v", asset.ResourceSpec)
	}

	// --- Patch lake ---
	pop, err := svc.Projects.Locations.Lakes.Patch(lakeName, &dataplex.GoogleCloudDataplexV1Lake{
		Description: "prod lake v2",
	}).UpdateMask("description").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Lakes.Patch: %v", err)
	}

	if !pop.Done {
		t.Fatalf("lake patch operation not done")
	}

	patched, err := svc.Projects.Locations.Lakes.Get(lakeName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Lakes.Get after patch: %v", err)
	}

	if patched.Description != "prod lake v2" || patched.Labels["team"] != "data" {
		t.Fatalf("patch not applied / unmasked field lost: %+v", patched)
	}

	if patched.Uid != lake.Uid {
		t.Fatalf("uid changed across patch: %q -> %q", lake.Uid, patched.Uid)
	}

	// --- Cascade delete ---
	if _, err := svc.Projects.Locations.Lakes.Delete(lakeName).Context(ctx).Do(); err != nil {
		t.Fatalf("Lakes.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.Lakes.Zones.Get(zoneName).Context(ctx).Do(); err == nil {
		t.Fatalf("zone survived lake cascade delete")
	}

	if _, err := svc.Projects.Locations.Lakes.Zones.Assets.Get(assetName).Context(ctx).Do(); err == nil {
		t.Fatalf("asset survived lake cascade delete")
	}
}

// TestSDKZoneRequiresParentLake verifies a zone create under a missing lake is a
// 404, matching the real API.
func TestSDKZoneRequiresParentLake(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	missing := "projects/" + project + "/locations/us-central1/lakes/nope"

	_, err := svc.Projects.Locations.Lakes.Zones.Create(missing, &dataplex.GoogleCloudDataplexV1Zone{
		Type:         "RAW",
		ResourceSpec: &dataplex.GoogleCloudDataplexV1ZoneResourceSpec{LocationType: "SINGLE_REGION"},
	}).ZoneId("z").Context(ctx).Do()
	if err == nil {
		t.Fatalf("zone create under missing lake should 404")
	}
}

// TestSDKZoneBadEnum verifies an out-of-range zone type is a 400.
func TestSDKZoneBadEnum(t *testing.T) {
	svc, project := newSDKClient(t)
	ctx := context.Background()
	loc := "projects/" + project + "/locations/us-central1"

	if _, err := svc.Projects.Locations.Lakes.Create(loc, &dataplex.GoogleCloudDataplexV1Lake{}).LakeId("l").Context(ctx).Do(); err != nil {
		t.Fatalf("Lakes.Create: %v", err)
	}

	_, err := svc.Projects.Locations.Lakes.Zones.Create(loc+"/lakes/l", &dataplex.GoogleCloudDataplexV1Zone{
		Type:         "BOGUS",
		ResourceSpec: &dataplex.GoogleCloudDataplexV1ZoneResourceSpec{LocationType: "SINGLE_REGION"},
	}).ZoneId("z").Context(ctx).Do()
	if err == nil {
		t.Fatalf("zone create with bad type should 400")
	}
}
