package monitor_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

// unitsByName GETs the metrics of names on the VM and returns each unit.
func unitsByName(t *testing.T, get func(string) (int, map[string]any), names ...string) map[string]any {
	t.Helper()

	q := url.Values{"metricnames": {strings.Join(names, ",")}, "aggregation": {"average"}, "api-version": {"2023-10-01"}}

	code, got := get(vmURI + "/providers/microsoft.insights/metrics?" + q.Encode())
	if code != http.StatusOK {
		t.Fatalf("GET metrics status = %d, want 200", code)
	}

	out := map[string]any{}

	for _, v := range got["value"].([]any) {
		m := v.(map[string]any)
		out[m["name"].(map[string]any)["value"].(string)] = m["unit"]
	}

	return out
}

// TestVMMetricsAzureUnits checks the built-in VM metrics carry their Azure
// Monitor units. They were stored as the CloudWatch-only None.
func TestVMMetricsAzureUnits(t *testing.T) {
	ts, _ := newMonitorServer(t)

	putVM(t, ts, "vm1")

	get := func(path string) (int, map[string]any) { return doJSON(t, ts, http.MethodGet, path, "") }

	got := unitsByName(t, get, "Percentage CPU", "Network In Total", "Disk Read Operations/Sec", "Available Memory Bytes")
	want := map[string]any{
		"Percentage CPU": "Percent", "Network In Total": "Bytes",
		"Disk Read Operations/Sec": "CountPerSecond", "Available Memory Bytes": "Bytes",
	}

	for name, unit := range want {
		if got[name] != unit {
			t.Errorf("%s unit = %v, want %v", name, got[name], unit)
		}
	}
}

// TestMetricsUnitlessIsUnspecified checks the wire safety net. Data stored
// with no unit, or with None, is reported as the Azure unit Unspecified.
func TestMetricsUnitlessIsUnspecified(t *testing.T) {
	ts, cloudP := newMonitorServer(t)

	putVM(t, ts, "vm1")

	now := time.Now().UTC()
	dims := map[string]string{"resourceId": vmURI}

	err := cloudP.Monitor.PutMetricData(context.Background(), []driver.MetricDatum{
		{Namespace: "Microsoft.Compute/virtualMachines", MetricName: "Blank", Value: 1, Dimensions: dims, Timestamp: now},
		{Namespace: "Microsoft.Compute/virtualMachines", MetricName: "AwsNone", Value: 1, Unit: "None", Dimensions: dims, Timestamp: now},
	})
	if err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	get := func(path string) (int, map[string]any) { return doJSON(t, ts, http.MethodGet, path, "") }

	got := unitsByName(t, get, "Blank", "AwsNone")
	if got["Blank"] != "Unspecified" || got["AwsNone"] != "Unspecified" {
		t.Fatalf("units = %v, want Unspecified for both", got)
	}
}

// TestMetricsReportStoredUnit checks that metrics and metricDefinitions report
// the unit the data was stored with. Both used to hardcode Count.
func TestMetricsReportStoredUnit(t *testing.T) {
	ts, cloudP := newMonitorServer(t)

	putVM(t, ts, "vm1")

	err := cloudP.Monitor.PutMetricData(context.Background(), []driver.MetricDatum{{
		Namespace: "Microsoft.Compute/virtualMachines", MetricName: "Custom Pct", Value: 40, Unit: "Percent",
		Dimensions: map[string]string{"resourceId": vmURI}, Timestamp: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("PutMetricData: %v", err)
	}

	url := vmURI + "/providers/microsoft.insights/metrics?metricnames=Custom%20Pct,Unknown&aggregation=average&api-version=2023-10-01"

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET metrics status = %d, want 200", code)
	}

	value, _ := got["value"].([]any)
	if len(value) != 2 {
		t.Fatalf("value len = %d, want 2", len(value))
	}

	if unit := value[0].(map[string]any)["unit"]; unit != "Percent" {
		t.Fatalf("metrics unit = %v, want Percent", unit)
	}

	// A metric with no data keeps the Azure default.
	if unit := value[1].(map[string]any)["unit"]; unit != "Count" {
		t.Fatalf("unit of a metric with no data = %v, want Count", unit)
	}

	defs := vmURI + "/providers/microsoft.insights/metricDefinitions?api-version=2023-10-01"

	code, got = doJSON(t, ts, http.MethodGet, defs, "")
	if code != http.StatusOK {
		t.Fatalf("GET metricDefinitions status = %d, want 200", code)
	}

	defList, _ := got["value"].([]any)
	for _, v := range defList {
		def, _ := v.(map[string]any)
		if def["name"].(map[string]any)["value"] == "Custom Pct" {
			if def["unit"] != "Percent" {
				t.Fatalf("definition unit = %v, want Percent", def["unit"])
			}

			return
		}
	}

	t.Fatalf("Custom Pct not in metricDefinitions: %+v", defList)
}
