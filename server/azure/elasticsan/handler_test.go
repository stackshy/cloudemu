package elasticsan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/elasticsan"
	elasticsansrv "github.com/stackshy/cloudemu/v2/server/azure/elasticsan"
)

const (
	apiVer   = "?api-version=2024-05-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.ElasticSan/elasticSans/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Sku      *struct {
		Name string `json:"name"`
		Tier string `json:"tier"`
	} `json:"sku"`
	Properties struct {
		BaseSizeTiB         int64    `json:"baseSizeTiB"`
		ExtendedSizeTiB     int64    `json:"extendedCapacitySizeTiB"`
		AvailabilityZones   []string `json:"availabilityZones"`
		PublicNetworkAccess string   `json:"publicNetworkAccess"`
		ProvisioningState   string   `json:"provisioningState"`
		TotalIops           int64    `json:"totalIops"`
		TotalMBps           int64    `json:"totalMBps"`
		TotalSizeTiB        int64    `json:"totalSizeTiB"`
		TotalVolumeSizeGiB  int64    `json:"totalVolumeSizeGiB"`
		VolumeGroupCount    int64    `json:"volumeGroupCount"`
	} `json:"properties"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := elasticsan.New(config.NewOptions())
	h := elasticsansrv.New(mock)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

func decode(t *testing.T, raw []byte) wireResp {
	t.Helper()

	var out wireResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	return out
}

const createBody = `{
	"location": "East US",
	"tags": {"env": "dev"},
	"sku": {"name": "Premium_LRS", "tier": "Premium"},
	"properties": {
		"baseSizeTiB": 1,
		"availabilityZones": ["1"]
	}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "san1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.ElasticSan/elasticSans" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Properties.TotalIops != 5000 || got.Properties.TotalMBps != 200 || got.Properties.TotalSizeTiB != 1 {
		t.Errorf("totals = %d/%d/%d, want 5000/200/1",
			got.Properties.TotalIops, got.Properties.TotalMBps, got.Properties.TotalSizeTiB)
	}

	if got.Properties.TotalVolumeSizeGiB != 0 || got.Properties.VolumeGroupCount != 0 {
		t.Errorf("volume totals = %d/%d, want 0/0",
			got.Properties.TotalVolumeSizeGiB, got.Properties.VolumeGroupCount)
	}

	if got.Sku == nil || got.Sku.Name != "Premium_LRS" || got.Sku.Tier != "Premium" {
		t.Errorf("sku = %+v", got.Sku)
	}

	if len(got.Properties.AvailabilityZones) != 1 || got.Properties.AvailabilityZones[0] != "1" {
		t.Errorf("zones = %v, want [1]", got.Properties.AvailabilityZones)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "san1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	_, a := do(t, srv, http.MethodGet, path, "")
	_, b := do(t, srv, http.MethodGet, path, "")

	if !bytes.Equal(a, b) {
		t.Errorf("GET not byte-stable:\n a=%s\n b=%s", a, b)
	}
}

func TestWirePatchRecomputesTotals(t *testing.T) {
	srv := newServer(t)
	path := basePath + "san1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	patch := `{"tags":{"env":"prod"},"properties":{"baseSizeTiB":2}}`

	code, raw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d, want 200 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Properties.TotalIops != 10000 || got.Properties.TotalMBps != 400 || got.Properties.TotalSizeTiB != 2 {
		t.Errorf("recomputed totals = %d/%d/%d, want 10000/400/2",
			got.Properties.TotalIops, got.Properties.TotalMBps, got.Properties.TotalSizeTiB)
	}

	// PATCH merged: sku and zones preserved, tags replaced.
	if got.Sku == nil || got.Sku.Name != "Premium_LRS" {
		t.Errorf("sku after patch = %+v, want Premium_LRS", got.Sku)
	}

	if len(got.Properties.AvailabilityZones) != 1 {
		t.Errorf("zones after patch = %v, want preserved [1]", got.Properties.AvailabilityZones)
	}

	if got.Tags["env"] != "prod" {
		t.Errorf("tags after patch = %v, want env=prod", got.Tags)
	}
}

func TestWirePatchMissingIs404(t *testing.T) {
	srv := newServer(t)

	code, _ := do(t, srv, http.MethodPatch, basePath+"ghost"+apiVer, `{"properties":{"baseSizeTiB":2}}`)
	if code != http.StatusNotFound {
		t.Errorf("patch missing status = %d, want 404", code)
	}
}

func TestWireDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	path := basePath + "san1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

func TestWireList(t *testing.T) {
	srv := newServer(t)

	if code, raw := do(t, srv, http.MethodPut, basePath+"san1"+apiVer, createBody); code != http.StatusCreated {
		t.Fatalf("create san1: %d (%s)", code, raw)
	}

	if code, raw := do(t, srv, http.MethodPut, basePath+"san2"+apiVer, createBody); code != http.StatusCreated {
		t.Fatalf("create san2: %d (%s)", code, raw)
	}

	code, raw := do(t, srv, http.MethodGet, basePath+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (%s)", code, raw)
	}

	var out struct {
		Value []wireResp `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v (%s)", err, raw)
	}

	if len(out.Value) != 2 {
		t.Errorf("list len = %d, want 2", len(out.Value))
	}
}
