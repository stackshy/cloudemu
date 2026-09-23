package monitor_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/services/monitoring/driver"
)

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
