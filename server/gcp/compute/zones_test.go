package compute_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestZonesGet covers zones.get: Terraform's google_compute_instance resolves
// the zone via GET .../zones/{zone} before creating an instance, so the endpoint
// must return a live (status UP) compute#zone rather than 501.
func TestZonesGet(t *testing.T) {
	ts := newGCPTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/compute/v1/projects/" + testProject + "/zones/" + testZone)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("zones.get status=%d body=%s", resp.StatusCode, dump)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got["kind"] != "compute#zone" {
		t.Errorf("kind=%v want compute#zone", got["kind"])
	}

	if got["name"] != testZone {
		t.Errorf("name=%v want %s", got["name"], testZone)
	}

	if got["status"] != "UP" {
		t.Errorf("status=%v want UP", got["status"])
	}

	region, _ := got["region"].(string)
	if !strings.HasSuffix(region, "/regions/us-central1") {
		t.Errorf("region=%s want .../regions/us-central1", region)
	}

	self, _ := got["selfLink"].(string)
	if !strings.HasSuffix(self, "/zones/"+testZone) {
		t.Errorf("selfLink=%s", self)
	}
}

// TestRegionsGet covers regions.get returning a live compute#region with a
// zones[] list.
func TestRegionsGet(t *testing.T) {
	ts := newGCPTestServer(t)

	resp, err := ts.Client().Get(ts.URL + "/compute/v1/projects/" + testProject + "/regions/us-central1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("regions.get status=%d body=%s", resp.StatusCode, dump)
	}

	var got struct {
		Kind   string   `json:"kind"`
		Name   string   `json:"name"`
		Status string   `json:"status"`
		Zones  []string `json:"zones"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got.Kind != "compute#region" || got.Name != "us-central1" || got.Status != "UP" {
		t.Errorf("region=%+v", got)
	}

	if len(got.Zones) == 0 {
		t.Errorf("region zones empty, want the region's zones listed")
	}
}

// TestDiskInsertUnquotedSizeGb reproduces the Terraform google provider's disk
// create: it marshals its request body from a map[string]interface{}, so sizeGb
// arrives as a BARE JSON number (20), not the quoted "20" the typed SDK sends. A
// strict `,string` decode rejected it with 400; the endpoint must accept either.
func TestDiskInsertUnquotedSizeGb(t *testing.T) {
	ts := newGCPTestServer(t)

	body := strings.NewReader(`{
		"name": "tf-disk",
		"sizeGb": 20,
		"type": "projects/p-1/zones/us-central1-a/diskTypes/pd-balanced"
	}`)

	resp, err := ts.Client().Post(ts.URL+zonesPath("/disks"), "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("disk insert (unquoted sizeGb) status=%d body=%s", resp.StatusCode, dump)
	}

	// Confirm the size round-tripped (not silently dropped to 0).
	get, err := ts.Client().Get(ts.URL + zonesPath("/disks/tf-disk"))
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()

	var disk map[string]any
	if err := json.NewDecoder(get.Body).Decode(&disk); err != nil {
		t.Fatal(err)
	}

	if disk["sizeGb"] != "20" {
		t.Errorf("sizeGb=%v want \"20\"", disk["sizeGb"])
	}
}

// TestDefaultServiceAccountRealistic verifies that an instance created without a
// serviceAccounts block reads back the default compute SA with a realistic
// project-scoped email (never the literal "default") and the default-access
// scope set — matching real GCP.
func TestDefaultServiceAccountRealistic(t *testing.T) {
	ts := newGCPTestServer(t)
	_ = insertInstance(t, ts, "vm-defsa")

	resp, err := ts.Client().Get(ts.URL + zonesPath("/instances/vm-defsa"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var got struct {
		ServiceAccounts []struct {
			Email  string   `json:"email"`
			Scopes []string `json:"scopes"`
		} `json:"serviceAccounts"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.ServiceAccounts) != 1 {
		t.Fatalf("serviceAccounts=%+v want 1", got.ServiceAccounts)
	}

	wantEmail := testProject + "-compute@developer.gserviceaccount.com"
	if got.ServiceAccounts[0].Email != wantEmail {
		t.Errorf("default SA email=%q want %q", got.ServiceAccounts[0].Email, wantEmail)
	}

	if len(got.ServiceAccounts[0].Scopes) != 6 {
		t.Errorf("default SA scopes=%v want the 6 default-access scopes", got.ServiceAccounts[0].Scopes)
	}
}
