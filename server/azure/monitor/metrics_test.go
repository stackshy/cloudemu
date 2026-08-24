package monitor_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const vmURI = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"

// TestMetricsDataPlane covers finding 3: Microsoft.Insights/metrics serves the
// timeseries the compute mock pushed, instead of 501.
func TestMetricsDataPlane(t *testing.T) {
	ts, cloudP := newMonitorServer(t)
	ctx := context.Background()

	if _, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "img-1", InstanceType: "Standard_B1s", Region: "eastus",
	}, 1); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	url := vmURI + "/providers/microsoft.insights/metrics?metricnames=Percentage%20CPU&aggregation=average&api-version=2023-10-01"

	code, got := doJSON(t, ts, http.MethodGet, url, "")
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

// TestMetricDefinitions covers finding 3: metricDefinitions lists the metrics
// the driver knows for the resource's namespace.
func TestMetricDefinitions(t *testing.T) {
	ts, cloudP := newMonitorServer(t)
	ctx := context.Background()

	if _, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "img-1", InstanceType: "Standard_B1s", Region: "eastus",
	}, 1); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

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
	if wid, _ := props["workspaceId"].(string); !strings.Contains(wid, "ws1") {
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
