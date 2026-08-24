package virtualmachines_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// getVM issues a GET on a VM and returns the decoded body.
func getVM(t *testing.T, ts *httptest.Server, name string) map[string]any {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + armBasePath(name) + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d body=%s", name, resp.StatusCode, dump)
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	return out
}

// postAction issues a POST lifecycle action and returns the status code.
func postAction(t *testing.T, ts *httptest.Server, name, action string) int {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+armBasePath(name)+"/"+action+apiVersion, http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

// powerStateCode returns the PowerState code from a VM body's instanceView.
func powerStateCode(t *testing.T, body map[string]any) string {
	t.Helper()

	props, _ := body["properties"].(map[string]any)
	iv, _ := props["instanceView"].(map[string]any)

	statuses, _ := iv["statuses"].([]any)
	for _, s := range statuses {
		st, _ := s.(map[string]any)
		code, _ := st["code"].(string)

		if len(code) > len("PowerState/") && code[:len("PowerState/")] == "PowerState/" {
			return code[len("PowerState/"):]
		}
	}

	return ""
}

// TestVMCreateOrUpdateIdempotent verifies a second PUT to the same {rg,name}
// updates the VM in place rather than creating a duplicate: List returns one,
// and the vmId is preserved across the update.
func TestVMCreateOrUpdateIdempotent(t *testing.T) {
	ts := newAzureTestServer(t)

	first := putVM(t, ts, "vm-idem")
	second := putVM(t, ts, "vm-idem")

	firstProps, _ := first["properties"].(map[string]any)
	secondProps, _ := second["properties"].(map[string]any)

	if firstProps["vmId"] != secondProps["vmId"] {
		t.Errorf("vmId changed across idempotent PUT: %v -> %v", firstProps["vmId"], secondProps["vmId"])
	}

	listURL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines"

	resp, err := ts.Client().Get(ts.URL + listURL + apiVersion)
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
		t.Fatalf("List returned %d VMs after repeated PUT, want 1", len(got.Value))
	}
}

// TestVMListResourceGroupScope verifies ListByResourceGroup does not leak VMs
// belonging to other resource groups.
func TestVMListResourceGroupScope(t *testing.T) {
	ts := newAzureTestServer(t)

	_ = putVM(t, ts, "vm-a")

	// Create a VM in a different resource group.
	body := `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_D2s_v3"}}}`

	req, _ := http.NewRequest(http.MethodPut,
		ts.URL+"/subscriptions/sub-1/resourceGroups/rg-2/providers/Microsoft.Compute/virtualMachines/vm-b"+apiVersion,
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	listURL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines"

	lresp, err := ts.Client().Get(ts.URL + listURL + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer lresp.Body.Close()

	var got struct {
		Value []map[string]any `json:"value"`
	}

	if err := json.NewDecoder(lresp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Value) != 1 {
		t.Fatalf("rg-1 list returned %d VMs, want 1 (rg-2 leaked)", len(got.Value))
	}

	if got.Value[0]["name"] != "vm-a" {
		t.Errorf("rg-1 list returned %v, want vm-a", got.Value[0]["name"])
	}
}

// TestVMInstanceView verifies GET .../{name}/instanceView returns a
// VirtualMachineInstanceView with power, provisioning, agent, and disk status.
func TestVMInstanceView(t *testing.T) {
	ts := newAzureTestServer(t)
	_ = putVM(t, ts, "vm-iv")

	resp, err := ts.Client().Get(ts.URL + armBasePath("vm-iv") + "/instanceView" + apiVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		dump, _ := io.ReadAll(resp.Body)
		t.Fatalf("instanceView status=%d body=%s", resp.StatusCode, dump)
	}

	var iv map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&iv); err != nil {
		t.Fatal(err)
	}

	if iv["vmAgent"] == nil {
		t.Error("instanceView missing vmAgent")
	}

	if iv["disks"] == nil {
		t.Error("instanceView missing disks")
	}

	statuses, _ := iv["statuses"].([]any)
	if len(statuses) < 2 {
		t.Fatalf("instanceView statuses=%d want >=2", len(statuses))
	}

	var sawPower, sawProvisioning bool

	for _, s := range statuses {
		st, _ := s.(map[string]any)
		code, _ := st["code"].(string)

		if code == "PowerState/running" {
			sawPower = true
		}

		if code == "ProvisioningState/succeeded" {
			sawProvisioning = true
		}
	}

	if !sawPower {
		t.Error("instanceView missing PowerState/running")
	}

	if !sawProvisioning {
		t.Error("instanceView missing ProvisioningState/succeeded")
	}
}

// TestVMPowerOffVsDeallocate verifies PowerOff reports PowerState/stopped (the
// VM stays allocated) while Deallocate reports PowerState/deallocated.
func TestVMPowerOffVsDeallocate(t *testing.T) {
	ts := newAzureTestServer(t)

	_ = putVM(t, ts, "vm-off")
	if code := postAction(t, ts, "vm-off", "powerOff"); code != http.StatusAccepted {
		t.Fatalf("powerOff status=%d want 202", code)
	}

	if got := powerStateCode(t, getVM(t, ts, "vm-off")); got != "stopped" {
		t.Errorf("after powerOff powerState=%q want stopped", got)
	}

	_ = putVM(t, ts, "vm-dealloc")
	if code := postAction(t, ts, "vm-dealloc", "deallocate"); code != http.StatusAccepted {
		t.Fatalf("deallocate status=%d want 202", code)
	}

	if got := powerStateCode(t, getVM(t, ts, "vm-dealloc")); got != "deallocated" {
		t.Errorf("after deallocate powerState=%q want deallocated", got)
	}
}
