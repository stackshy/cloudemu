package virtualmachines_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// armBasePath returns the per-test ARM resource URL for a given VM name.
func armBasePath(name string) string {
	return "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/" + name
}

const apiVersion = "?api-version=2023-09-01"

func newAzureTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{VirtualMachines: cloud.VirtualMachines})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func putVM(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()

	body := strings.NewReader(`{
		"location": "eastus",
		"tags": {"env": "test"},
		"properties": {
			"hardwareProfile": {"vmSize": "Standard_D2s_v3"},
			"storageProfile": {
				"imageReference": {"publisher": "Canonical", "offer": "UbuntuServer", "sku": "22.04-LTS", "version": "latest"}
			},
			"osProfile": {"computerName": "` + name + `", "adminUsername": "azureuser"}
		}
	}`)

	req, err := http.NewRequest(http.MethodPut, ts.URL+armBasePath(name)+apiVersion, body)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s: status %d, body=%s", name, resp.StatusCode, dump)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return out
}

func TestVMCreateOrUpdate(t *testing.T) {
	ts := newAzureTestServer(t)
	got := putVM(t, ts, "vm-1")

	if got["name"] != "vm-1" {
		t.Errorf("name=%v want vm-1", got["name"])
	}

	wantID := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-1"
	if got["id"] != wantID {
		t.Errorf("id=%v want %s", got["id"], wantID)
	}

	if got["type"] != "Microsoft.Compute/virtualMachines" {
		t.Errorf("type=%v", got["type"])
	}

	props, _ := got["properties"].(map[string]any)
	if props["provisioningState"] != "Succeeded" {
		t.Errorf("provisioningState=%v", props["provisioningState"])
	}

	if props["vmId"] == "" || props["vmId"] == nil {
		t.Error("expected non-empty vmId (driver instance ID)")
	}
}

func TestVMGet(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-get")

	resp, err := ts.Client().Get(ts.URL + armBasePath("vm-get") + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if got["name"] != "vm-get" {
		t.Errorf("name=%v", got["name"])
	}
}

func TestVMGetNotFound(t *testing.T) {
	ts := newAzureTestServer(t)

	resp, err := ts.Client().Get(ts.URL + armBasePath("missing") + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d want 404", resp.StatusCode)
	}

	var env map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&env)

	body, _ := env["error"].(map[string]any)
	if body["code"] != "ResourceNotFound" {
		t.Errorf("code=%v want ResourceNotFound", body["code"])
	}
}

func TestVMList(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-a")
	_ = putVM(t, ts, "vm-b")

	listURL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines"

	resp, err := ts.Client().Get(ts.URL + listURL + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	var got struct {
		Value []map[string]any `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Value) != 2 {
		t.Errorf("got %d VMs want 2", len(got.Value))
	}

	names := map[string]bool{}
	for _, v := range got.Value {
		if n, ok := v["name"].(string); ok {
			names[n] = true
		}
	}

	if !names["vm-a"] || !names["vm-b"] {
		t.Errorf("missing vms in list: %v", names)
	}
}

func TestVMListSubscriptionScope(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-sub")

	listURL := "/subscriptions/sub-1/providers/Microsoft.Compute/virtualMachines"

	resp, err := ts.Client().Get(ts.URL + listURL + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestVMDelete(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-del")

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+armBasePath("vm-del")+apiVersion, http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete status=%d want 204", resp.StatusCode)
	}
}

func TestVMLifecycleActions(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-life")

	for _, action := range []string{"powerOff", "start", "restart"} {
		t.Run(action, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost,
				ts.URL+armBasePath("vm-life")+"/"+action+apiVersion, http.NoBody)

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNoContent {
				dump, _ := io.ReadAll(resp.Body)
				t.Errorf("%s: status=%d body=%s", action, resp.StatusCode, dump)
			}
		})
	}
}

func TestVMUnsupportedMethod(t *testing.T) {
	ts := newAzureTestServer(t)

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+armBasePath("x")+apiVersion, http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status=%d want 501", resp.StatusCode)
	}
}

func TestVMHandlerIgnoresOtherProviders(t *testing.T) {
	// Sanity check: our handler shouldn't claim non-Compute ARM URLs. The
	// server.Server returns 501 when no handler matches.
	ts := newAzureTestServer(t)

	storageURL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Storage/storageAccounts/myacct"

	resp, err := ts.Client().Get(ts.URL + storageURL + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status=%d want 501 (no handler)", resp.StatusCode)
	}
}

func TestVMInvalidJSON(t *testing.T) {
	ts := newAzureTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+armBasePath("bad")+apiVersion,
		bytes.NewBufferString(`{not json`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d want 400", resp.StatusCode)
	}
}
