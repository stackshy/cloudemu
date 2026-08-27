package monitor_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const vmURI = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"

const vmAPIVer = "?api-version=2023-09-01"

// vmURIFor is vmURI generalized to an arbitrary VM name, for tests that need
// more than one resource in the same resource group.
func vmURIFor(name string) string {
	return "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/" + name
}

// putVM creates name via the real ARM CreateOrUpdate PUT, so the ARM name the
// URL addresses it by is the same name the compute mock records (and derives
// its Microsoft.Insights "resourceId" dimension from) — unlike calling
// RunInstances directly, which never associates the driver's internal
// instance id with an ARM name at all.
func putVM(t *testing.T, ts *httptest.Server, name string) {
	t.Helper()

	body := `{
		"location": "eastus",
		"properties": {
			"hardwareProfile": {"vmSize": "Standard_B1s"},
			"storageProfile": {
				"imageReference": {"publisher": "Canonical", "offer": "UbuntuServer", "sku": "22.04-LTS", "version": "latest"}
			},
			"osProfile": {"computerName": "` + name + `", "adminUsername": "azureuser"}
		}
	}`

	if code, got := doJSON(t, ts, http.MethodPut, vmURIFor(name)+vmAPIVer, body); code != http.StatusCreated {
		t.Fatalf("PUT VM %s status = %d, want 201; body = %+v", name, code, got)
	}
}

func metricsURLFor(name string) string {
	return vmURIFor(name) + "/providers/microsoft.insights/metrics?metricnames=Percentage%20CPU&aggregation=average&api-version=2023-10-01"
}

// TestMetricsDataPlane covers finding 3: Microsoft.Insights/metrics serves the
// timeseries the compute mock pushed, instead of 501.
func TestMetricsDataPlane(t *testing.T) {
	ts, _ := newMonitorServer(t)

	putVM(t, ts, "vm1")

	code, got := doJSON(t, ts, http.MethodGet, metricsURLFor("vm1"), "")
	if code != http.StatusOK {
		t.Fatalf("GET metrics status = %d, want 200", code)
	}

	value, _ := got["value"].([]any)
	if len(value) != 1 {
		t.Fatalf("value len = %d, want 1", len(value))
	}

	metric, _ := value[0].(map[string]any)
	name, _ := metric["name"].(map[string]any)
	if name["value"] != "Percentage CPU" {
		t.Fatalf("metric name = %v, want Percentage CPU", name["value"])
	}

	series, _ := metric["timeseries"].([]any)
	if len(series) == 0 {
		t.Fatalf("timeseries empty")
	}

	data, _ := series[0].(map[string]any)["data"].([]any)
	if len(data) == 0 {
		t.Fatalf("no datapoints returned")
	}

	first, _ := data[0].(map[string]any)
	if first["average"].(float64) != 25 {
		t.Fatalf("average = %v, want 25", first["average"])
	}
}

// TestMetricsDataPlaneIsolatedPerResource covers the isolation finding: two
// VMs in the same resource group must not see each other's datapoints. vm2 is
// powered off (driving its Percentage CPU to 0); vm1's query must still read
// its own Running value (25), and vm2's query must reflect its own stop (0),
// not whatever the other resource last reported.
func TestMetricsDataPlaneIsolatedPerResource(t *testing.T) {
	ts, _ := newMonitorServer(t)

	putVM(t, ts, "vm1")
	putVM(t, ts, "vm2")

	powerOffURL := vmURIFor("vm2") + "/powerOff" + vmAPIVer
	if code, _ := doJSON(t, ts, http.MethodPost, powerOffURL, ""); code != http.StatusAccepted {
		t.Fatalf("POST powerOff vm2 status = %d, want 202", code)
	}

	// vm1 was never touched: every datapoint must still read 25, never the 0
	// that vm2's power-off just recorded.
	code, got := doJSON(t, ts, http.MethodGet, metricsURLFor("vm1"), "")
	if code != http.StatusOK {
		t.Fatalf("GET vm1 metrics status = %d, want 200", code)
	}

	for _, row := range metricDatapoints(t, got) {
		if avg := row["average"].(float64); avg != 25 {
			t.Fatalf("vm1 metrics leaked another resource's datapoint: average = %v, want 25", avg)
		}
	}

	// vm2's own query does see its power-off (its most recent datapoint reads 0).
	code, got = doJSON(t, ts, http.MethodGet, metricsURLFor("vm2"), "")
	if code != http.StatusOK {
		t.Fatalf("GET vm2 metrics status = %d, want 200", code)
	}

	vm2Data := metricDatapoints(t, got)
	if len(vm2Data) == 0 {
		t.Fatalf("vm2: no datapoints returned")
	}

	// The last bucket blends this VM's own tail-end Running datapoint (25) with
	// its own power-off datapoint (0) landing in the same period, so it reads
	// below 25 rather than exactly 0 — but it must have moved off 25, proving
	// vm2 saw its own power-off event.
	last := vm2Data[len(vm2Data)-1]
	if avg := last["average"].(float64); avg == 25 {
		t.Fatalf("vm2 power-off metric not scoped to vm2: last average = %v, want < 25", avg)
	}
}

func metricDatapoints(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()

	value, _ := body["value"].([]any)
	if len(value) != 1 {
		t.Fatalf("value len = %d, want 1", len(value))
	}

	metric, _ := value[0].(map[string]any)
	series, _ := metric["timeseries"].([]any)

	if len(series) == 0 {
		t.Fatalf("timeseries empty")
	}

	raw, _ := series[0].(map[string]any)["data"].([]any)

	out := make([]map[string]any, 0, len(raw))
	for _, d := range raw {
		row, _ := d.(map[string]any)
		out = append(out, row)
	}

	return out
}

// TestMetricDefinitions covers finding 3: metricDefinitions lists the metrics
// the driver knows for the resource's namespace.
func TestMetricDefinitions(t *testing.T) {
	ts, _ := newMonitorServer(t)

	putVM(t, ts, "vm1")

	url := vmURI + "/providers/microsoft.insights/metricDefinitions?api-version=2023-10-01"

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET metricDefinitions status = %d, want 200", code)
	}

	value, _ := got["value"].([]any)

	found := false
	for _, v := range value {
		def, _ := v.(map[string]any)
		name, _ := def["name"].(map[string]any)
		if name["value"] == "Percentage CPU" {
			found = true
		}
	}

	if !found {
		t.Fatalf("Percentage CPU not in metricDefinitions: %+v", value)
	}
}

// TestDiagnosticSettings covers finding 2: diagnosticSettings CRUD on an
// arbitrary resource URI, routing logs to a workspace.
func TestDiagnosticSettings(t *testing.T) {
	ts, _ := newMonitorServer(t)

	url := vmURI + "/providers/microsoft.insights/diagnosticSettings/mysetting?api-version=2021-05-01-preview"

	body := `{"properties":{
		"workspaceId":"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.OperationalInsights/workspaces/ws1",
		"logs":[{"categoryGroup":"allLogs","enabled":true}]}}`

	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusOK {
		t.Fatalf("PUT diagnosticSettings status = %d, want 200", code)
	}

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code)
	}

	if got["type"] != "Microsoft.Insights/diagnosticSettings" {
		t.Errorf("type = %v", got["type"])
	}

	props, _ := got["properties"].(map[string]any)
	if wid, _ := props["workspaceId"].(string); wid == "" {
		t.Errorf("workspaceId dropped: %+v", props)
	}

	listURL := vmURI + "/providers/microsoft.insights/diagnosticSettings?api-version=2021-05-01-preview"

	_, listed := doJSON(t, ts, http.MethodGet, listURL, "")
	if v, _ := listed["value"].([]any); len(v) != 1 {
		t.Errorf("list len = %d, want 1", len(v))
	}

	if code, _ := doJSON(t, ts, http.MethodDelete, url, ""); code != http.StatusOK {
		t.Errorf("DELETE status = %d, want 200", code)
	}

	if code, _ := doJSON(t, ts, http.MethodGet, url, ""); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
}

// TestDiagnosticSettingsDeleteMissingIsNoContent asserts DELETE on a
// diagnosticSetting that does not exist returns 204 No Content (idempotent),
// matching the Azure Monitor REST contract, not a 404 error body.
func TestDiagnosticSettingsDeleteMissingIsNoContent(t *testing.T) {
	ts, _ := newMonitorServer(t)

	url := vmURI + "/providers/microsoft.insights/diagnosticSettings/never?api-version=2021-05-01-preview"

	if code, body := doJSON(t, ts, http.MethodDelete, url, ""); code != http.StatusNoContent {
		t.Errorf("DELETE missing status = %d, want 204; body = %v", code, body)
	}
}
