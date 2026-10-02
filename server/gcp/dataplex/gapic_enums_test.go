package dataplex_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gapic "cloud.google.com/go/dataplex/apiv1"
	"cloud.google.com/go/dataplex/apiv1/dataplexpb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	dpdriver "github.com/stackshy/cloudemu/v2/services/dataplex/driver"
)

const (
	gapicProject  = "mock-project"
	gapicLocation = "us-central1"
	gapicParent   = "projects/" + gapicProject + "/locations/" + gapicLocation
)

func newGAPIC(t *testing.T) (*gapic.Client, *gcp.Provider, *httptest.Server) {
	t.Helper()

	cloud := cloudemu.NewGCP()

	ts := httptest.NewServer(gcpserver.NewFromProvider(cloud))
	t.Cleanup(ts.Close)

	c, err := gapic.NewRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c, cloud, ts
}

func gapicCreateLake(t *testing.T, c *gapic.Client, id string) string {
	t.Helper()

	ctx := context.Background()

	op, err := c.CreateLake(ctx, &dataplexpb.CreateLakeRequest{Parent: gapicParent, LakeId: id, Lake: &dataplexpb.Lake{}})
	if err != nil {
		t.Fatalf("CreateLake: %v", err)
	}

	lake, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateLake Wait: %v", err)
	}

	return lake.GetName()
}

func assertZoneEnums(t *testing.T, step string, z *dataplexpb.Zone) {
	t.Helper()

	if z.GetType() != dataplexpb.Zone_RAW {
		t.Errorf("%s: type=%v want RAW", step, z.GetType())
	}

	if z.GetResourceSpec().GetLocationType() != dataplexpb.Zone_ResourceSpec_SINGLE_REGION {
		t.Errorf("%s: resourceSpec.locationType=%v want SINGLE_REGION", step, z.GetResourceSpec().GetLocationType())
	}

	if z.GetState() != dataplexpb.State_ACTIVE {
		t.Errorf("%s: state=%v want ACTIVE", step, z.GetState())
	}
}

// TestGAPICZoneAssetNumericEnumsLifecycle is GDPX-01: the dataplex/apiv1 REST
// client sends zone type, resourceSpec.locationType and the asset
// resourceSpec.type as numbers. Zone create used to fail with "zone type is
// required and must be one of RAW, CURATED". The zone update resends the
// fetched zone, so the output-only state goes back as a number too.
func TestGAPICZoneAssetNumericEnumsLifecycle(t *testing.T) {
	c, _, _ := newGAPIC(t)
	ctx := context.Background()
	lakeName := gapicCreateLake(t, c, "enum-lake")

	zop, err := c.CreateZone(ctx, &dataplexpb.CreateZoneRequest{
		Parent: lakeName,
		ZoneId: "raw-zone",
		Zone: &dataplexpb.Zone{
			Description:  "v1",
			Type:         dataplexpb.Zone_RAW,
			ResourceSpec: &dataplexpb.Zone_ResourceSpec{LocationType: dataplexpb.Zone_ResourceSpec_SINGLE_REGION},
		},
	})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	zone, err := zop.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateZone Wait: %v", err)
	}

	assertZoneEnums(t, "create", zone)

	zoneName := zone.GetName()

	aop, err := c.CreateAsset(ctx, &dataplexpb.CreateAssetRequest{
		Parent:  zoneName,
		AssetId: "bucket-asset",
		Asset: &dataplexpb.Asset{ResourceSpec: &dataplexpb.Asset_ResourceSpec{
			Type: dataplexpb.Asset_ResourceSpec_STORAGE_BUCKET,
			Name: "projects/" + gapicProject + "/buckets/b",
		}},
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	asset, err := aop.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateAsset Wait: %v", err)
	}

	if asset.GetResourceSpec().GetType() != dataplexpb.Asset_ResourceSpec_STORAGE_BUCKET {
		t.Errorf("asset: resourceSpec.type=%v want STORAGE_BUCKET", asset.GetResourceSpec().GetType())
	}

	got, err := c.GetZone(ctx, &dataplexpb.GetZoneRequest{Name: zoneName})
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}

	assertZoneEnums(t, "get", got)

	assertZoneListed(t, c, lakeName, zoneName)

	got.Description = "v2"

	uop, err := c.UpdateZone(ctx, &dataplexpb.UpdateZoneRequest{
		Zone:       got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
	})
	if err != nil {
		t.Fatalf("UpdateZone: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateZone Wait: %v", err)
	}

	if updated.GetDescription() != "v2" {
		t.Errorf("update: description=%q want v2", updated.GetDescription())
	}

	assertZoneEnums(t, "update", updated)

	gapicDelete(t, "DeleteAsset", func() error {
		op, err := c.DeleteAsset(ctx, &dataplexpb.DeleteAssetRequest{Name: asset.GetName()})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	gapicDelete(t, "DeleteZone", func() error {
		op, err := c.DeleteZone(ctx, &dataplexpb.DeleteZoneRequest{Name: zoneName})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	gapicDelete(t, "DeleteLake", func() error {
		op, err := c.DeleteLake(ctx, &dataplexpb.DeleteLakeRequest{Name: lakeName})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	_, err = c.GetZone(ctx, &dataplexpb.GetZoneRequest{Name: zoneName})

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("GetZone after delete: err=%v want 404", err)
	}
}

func gapicDelete(t *testing.T, step string, del func() error) {
	t.Helper()

	if err := del(); err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

func assertZoneListed(t *testing.T, c *gapic.Client, lakeName, zoneName string) {
	t.Helper()

	it := c.ListZones(context.Background(), &dataplexpb.ListZonesRequest{Parent: lakeName})

	for {
		z, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListZones: %s not listed", zoneName)
		}

		if err != nil {
			t.Fatalf("ListZones: %v", err)
		}

		if z.GetName() == zoneName {
			assertZoneEnums(t, "list", z)
			return
		}
	}
}

// TestZoneStoredNumericEnumsRenderNames covers a zone whose enum fields were
// stored as numbers before request bodies were normalized: reads must render
// the names and leave the stored bytes as they are.
func TestZoneStoredNumericEnumsRenderNames(t *testing.T) {
	c, cloud, ts := newGAPIC(t)
	ctx := context.Background()
	lakeName := gapicCreateLake(t, c, "legacy-lake")

	stored := map[string]json.RawMessage{
		"type":         json.RawMessage(`1`),
		"resourceSpec": json.RawMessage(`{"locationType":1}`),
	}

	if _, _, err := cloud.Dataplex.CreateZone(ctx, &dpdriver.Config{
		Project: gapicProject, Location: gapicLocation, Lake: "legacy-lake", ID: "legacy", Fields: stored,
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}

	zoneName := lakeName + "/zones/legacy"

	// The gapic client decodes numbers and names alike, so the wire form is
	// checked on a raw GET.
	if typ, loc := rawZoneEnums(t, ts, zoneName); typ != `"RAW"` || loc != `"SINGLE_REGION"` {
		t.Errorf("raw GET type=%s resourceSpec.locationType=%s want \"RAW\", \"SINGLE_REGION\"", typ, loc)
	}

	got, err := c.GetZone(ctx, &dataplexpb.GetZoneRequest{Name: zoneName})
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}

	assertZoneEnums(t, "get", got)

	res, err := cloud.Dataplex.GetZone(ctx, gapicProject, gapicLocation, "legacy-lake", "legacy")
	if err != nil {
		t.Fatalf("driver GetZone: %v", err)
	}

	if string(res.Fields["type"]) != "1" || string(res.Fields["resourceSpec"]) != `{"locationType":1}` {
		t.Fatalf("stored fields rewritten: type=%s resourceSpec=%s", res.Fields["type"], res.Fields["resourceSpec"])
	}
}

type rawZoneJSON struct {
	Type         json.RawMessage `json:"type"`
	ResourceSpec struct {
		LocationType json.RawMessage `json:"locationType"`
	} `json:"resourceSpec"`
}

// rawZoneEnums GETs a zone over plain HTTP and returns its type and
// resourceSpec.locationType exactly as they are on the wire.
func rawZoneEnums(t *testing.T, ts *httptest.Server, name string) (typ, locationType string) {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + "/v1/" + name)
	if err != nil {
		t.Fatalf("raw GET: %v", err)
	}

	defer resp.Body.Close()

	var z rawZoneJSON
	if err := json.NewDecoder(resp.Body).Decode(&z); err != nil {
		t.Fatalf("decode raw GET: %v", err)
	}

	return string(z.Type), string(z.ResourceSpec.LocationType)
}
