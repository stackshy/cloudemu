package images_test

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

func newImagesServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		VirtualMachines: cloud.VirtualMachines,
		Images:          cloud.VirtualMachines,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func putRaw(t *testing.T, ts *httptest.Server, path, body string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPut, ts.URL+path+wireAPIVersion, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s: status %d body=%s", path, resp.StatusCode, dump)
	}
}

// TestImageGetHasStorageProfileAndSource verifies a cold GET on a captured image
// returns the osDisk storageProfile and the sourceVirtualMachine reference.
func TestImageGetHasStorageProfileAndSource(t *testing.T) {
	ts := newImagesServer(t)

	vmID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-src"

	putRaw(t, ts, vmID, `{
		"location":"eastus",
		"properties":{
			"hardwareProfile":{"vmSize":"Standard_D2s_v3"},
			"storageProfile":{"osDisk":{"osType":"Linux"}}
		}
	}`)

	imgID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/images/img-1"

	putRaw(t, ts, imgID, `{
		"location":"eastus",
		"properties":{"sourceVirtualMachine":{"id":"`+vmID+`"}}
	}`)

	resp, err := ts.Client().Get(ts.URL + imgID + wireAPIVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET image status=%d body=%s", resp.StatusCode, dump)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	props, _ := got["properties"].(map[string]any)

	src, _ := props["sourceVirtualMachine"].(map[string]any)
	if src == nil || src["id"] != vmID {
		t.Errorf("sourceVirtualMachine.id=%v want %s", src, vmID)
	}

	sp, _ := props["storageProfile"].(map[string]any)
	if sp == nil {
		t.Fatal("storageProfile missing on image GET")
	}

	osDisk, _ := sp["osDisk"].(map[string]any)
	if osDisk == nil {
		t.Fatal("storageProfile.osDisk missing")
	}

	if osDisk["osType"] != "Linux" {
		t.Errorf("osDisk.osType=%v want Linux", osDisk["osType"])
	}

	if osDisk["osState"] == nil || osDisk["osState"] == "" {
		t.Error("osDisk.osState missing")
	}

	if osDisk["diskSizeGB"] == nil {
		t.Error("osDisk.diskSizeGB missing")
	}

	if osDisk["storageAccountType"] == nil || osDisk["storageAccountType"] == "" {
		t.Error("osDisk.storageAccountType missing")
	}
}
