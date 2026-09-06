package disks_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const wireAPIVersion = "?api-version=2023-09-01"

func newDisksServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Disks: cloud.VirtualMachines})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func diskPath(rg, name string) string {
	return "/subscriptions/sub-1/resourceGroups/" + rg + "/providers/Microsoft.Compute/disks/" + name
}

func putDisk(t *testing.T, ts *httptest.Server, rg, name, body string) map[string]any {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+diskPath(rg, name)+wireAPIVersion, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT disk %s: status %d body=%s", name, resp.StatusCode, dump)
	}

	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return out
}

func getDisk(t *testing.T, ts *httptest.Server, rg, name string) map[string]any {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + diskPath(rg, name) + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET disk %s: status %d body=%s", name, resp.StatusCode, dump)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	return out
}

func diskProps(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	props, ok := body["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties in %v", body)
	}

	return props
}

// TestDiskCreationDataAndMetadata verifies an empty disk echoes its createOption
// and populates diskSizeGB, timeCreated, and uniqueId.
func TestDiskCreationDataAndMetadata(t *testing.T) {
	ts := newDisksServer(t)

	putDisk(t, ts, "rg-1", "disk-empty", `{
		"location":"eastus",
		"sku":{"name":"Premium_LRS"},
		"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":100}
	}`)

	props := diskProps(t, getDisk(t, ts, "rg-1", "disk-empty"))

	if props["diskSizeGB"].(float64) != 100 {
		t.Errorf("diskSizeGB=%v want 100", props["diskSizeGB"])
	}

	cd, _ := props["creationData"].(map[string]any)
	if cd["createOption"] != "Empty" {
		t.Errorf("createOption=%v want Empty", cd["createOption"])
	}

	if props["timeCreated"] == nil || props["timeCreated"] == "" {
		t.Error("timeCreated missing")
	}

	if props["uniqueId"] == nil || props["uniqueId"] == "" {
		t.Error("uniqueId missing")
	}
}

// TestDiskCopyInheritsSourceSize verifies a Copy disk with no explicit
// diskSizeGB inherits the source disk's size and echoes createOption/source.
func TestDiskCopyInheritsSourceSize(t *testing.T) {
	ts := newDisksServer(t)

	putDisk(t, ts, "rg-1", "disk-src", `{
		"location":"eastus","sku":{"name":"Premium_LRS"},
		"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":128}
	}`)

	sourceID := diskPath("rg-1", "disk-src")

	putDisk(t, ts, "rg-1", "disk-copy", `{
		"location":"eastus","sku":{"name":"Premium_LRS"},
		"properties":{"creationData":{"createOption":"Copy","sourceResourceId":"`+sourceID+`"}}
	}`)

	props := diskProps(t, getDisk(t, ts, "rg-1", "disk-copy"))

	if props["diskSizeGB"].(float64) != 128 {
		t.Errorf("copy diskSizeGB=%v want 128 (inherited)", props["diskSizeGB"])
	}

	cd, _ := props["creationData"].(map[string]any)
	if cd["createOption"] != "Copy" {
		t.Errorf("createOption=%v want Copy", cd["createOption"])
	}

	if cd["sourceResourceId"] != sourceID {
		t.Errorf("sourceResourceId=%v want %s", cd["sourceResourceId"], sourceID)
	}
}

// TestDiskCreateOrUpdateIdempotent verifies a repeated PUT does not accumulate a
// duplicate: List returns one.
func TestDiskCreateOrUpdateIdempotent(t *testing.T) {
	ts := newDisksServer(t)

	body := `{"location":"eastus","sku":{"name":"Premium_LRS"},"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":64}}`
	putDisk(t, ts, "rg-1", "disk-idem", body)
	putDisk(t, ts, "rg-1", "disk-idem", body)

	resp, err := ts.Client().Get(ts.URL +
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks" + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got struct {
		Value []map[string]any `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Value) != 1 {
		t.Fatalf("List returned %d disks after repeated PUT, want 1", len(got.Value))
	}
}

// TestDiskCreateOrUpdateCrossRGIsolation verifies that PUTting a disk with a
// name that already exists in ANOTHER resource group does not delete/hijack the
// original — in ARM {subscription,resourceGroup,name} is the resource identity.
func TestDiskCreateOrUpdateCrossRGIsolation(t *testing.T) {
	ts := newDisksServer(t)

	body := `{"location":"eastus","sku":{"name":"Premium_LRS"},"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":64}}`
	putDisk(t, ts, "rg-1", "shared", body)
	putDisk(t, ts, "rg-2", "shared", body)

	// The rg-1 disk must survive the rg-2 PUT of the same name.
	resp, err := ts.Client().Get(ts.URL +
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks/shared" + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("GET rg-1/shared after rg-2 PUT: status %d, want 200 (original was hijacked)", resp.StatusCode)
	}
}

// TestDiskZoneRoundTrip verifies a zonal disk's top-level zones array survives
// create → get → list. zones is a top-level Disk field (not a property), so the
// properties-only echo overlay does not preserve it; without the handler's own
// round-trip azurerm_managed_disk's `zone` would drift every plan.
func TestDiskZoneRoundTrip(t *testing.T) {
	ts := newDisksServer(t)

	putResp := putDisk(t, ts, "rg-1", "disk-zonal", `{
		"location":"eastus",
		"sku":{"name":"Premium_LRS"},
		"zones":["2"],
		"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":64}
	}`)

	assertSingleZone(t, "create", putResp, "2")
	assertSingleZone(t, "get", getDisk(t, ts, "rg-1", "disk-zonal"), "2")

	// A regional (non-zonal) disk must omit zones entirely, not report an empty
	// or phantom zone.
	putDisk(t, ts, "rg-1", "disk-regional", `{
		"location":"eastus","sku":{"name":"Premium_LRS"},
		"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":64}
	}`)

	if z, present := getDisk(t, ts, "rg-1", "disk-regional")["zones"]; present {
		t.Errorf("regional disk reported zones=%v, want field omitted", z)
	}
}

func assertSingleZone(t *testing.T, stage string, body map[string]any, want string) {
	t.Helper()

	zones, ok := body["zones"].([]any)
	if !ok || len(zones) != 1 {
		t.Fatalf("%s: zones=%v want single-element [%q]", stage, body["zones"], want)
	}

	if zones[0] != want {
		t.Errorf("%s: zones[0]=%v want %q", stage, zones[0], want)
	}
}

// TestDiskListResourceGroupScope verifies list does not leak other RGs' disks.
func TestDiskListResourceGroupScope(t *testing.T) {
	ts := newDisksServer(t)

	body := `{"location":"eastus","sku":{"name":"Premium_LRS"},"properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":32}}`
	putDisk(t, ts, "rg-1", "disk-a", body)
	putDisk(t, ts, "rg-2", "disk-b", body)

	resp, err := ts.Client().Get(ts.URL +
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/disks" + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got struct {
		Value []map[string]any `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Value) != 1 {
		t.Fatalf("rg-1 disk list returned %d, want 1 (rg-2 leaked)", len(got.Value))
	}
}
