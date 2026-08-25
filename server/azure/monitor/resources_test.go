package monitor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureprovider "github.com/stackshy/cloudemu/v2/providers/azure"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

const apiVer = "?api-version=2018-03-01"

func newMonitorServer(t *testing.T) (*httptest.Server, *azureprovider.Provider) {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		Monitor:         cloudP.Monitor,
		VirtualMachines: cloudP.VirtualMachines,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts, cloudP
}

func doJSON(t *testing.T, ts *httptest.Server, method, url string, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader = http.NoBody
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, _ := http.NewRequest(method, ts.URL+url, rdr)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	out := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}

	return resp.StatusCode, out
}

// TestMetricAlertPersistence covers finding 1: the full alert definition is
// persisted and echoed on Get, not reduced to provisioningState.
func TestMetricAlertPersistence(t *testing.T) {
	ts, _ := newMonitorServer(t)

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/cpu-alert" + apiVer

	body := `{
		"location": "global",
		"tags": {"team": "sre"},
		"properties": {
			"description": "High CPU",
			"severity": 2,
			"enabled": true,
			"scopes": ["/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"],
			"evaluationFrequency": "PT1M",
			"windowSize": "PT5M",
			"criteria": {
				"odata.type": "Microsoft.Azure.Monitor.SingleResourceMultipleMetricCriteria",
				"allOf": [{
					"name": "cpu",
					"metricName": "Percentage CPU",
					"metricNamespace": "Microsoft.Compute/virtualMachines",
					"operator": "GreaterThan",
					"threshold": 80,
					"timeAggregation": "Average"
				}]
			}
		}
	}`

	// Metric Alerts - Create Or Update documents a single response, 200 OK,
	// for both a first create and a subsequent update.
	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", code)
	}

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code)
	}

	props, _ := got["properties"].(map[string]any)
	if props["description"] != "High CPU" {
		t.Errorf("description = %v, want High CPU", props["description"])
	}

	if props["severity"].(float64) != 2 {
		t.Errorf("severity = %v, want 2", props["severity"])
	}

	if props["windowSize"] != "PT5M" {
		t.Errorf("windowSize = %v, want PT5M", props["windowSize"])
	}

	if props["scopes"] == nil || props["criteria"] == nil {
		t.Errorf("scopes/criteria dropped: %+v", props)
	}

	if tags, _ := got["tags"].(map[string]any); tags["team"] != "sre" {
		t.Errorf("tags dropped: %+v", got["tags"])
	}
}

// TestMetricAlertEvaluatesMetric covers finding 1: the named metric is actually
// evaluated. A VM pushes Percentage CPU=25; an alert with threshold 20 (>) must
// go to ALARM.
func TestMetricAlertEvaluatesMetric(t *testing.T) {
	ts, cloudP := newMonitorServer(t)
	ctx := context.Background()

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/cpu-hot" + apiVer

	body := `{
		"location": "global",
		"properties": {
			"windowSize": "PT5M",
			"criteria": {"allOf": [{
				"metricName": "Percentage CPU",
				"metricNamespace": "Microsoft.Compute/virtualMachines",
				"operator": "GreaterThan",
				"threshold": 20,
				"timeAggregation": "Average"
			}]}
		}
	}`

	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", code)
	}

	// The VM pushes Percentage CPU=25 datapoints, which drives the just-created
	// alarm's evaluation over its metric.
	if _, err := cloudP.VirtualMachines.RunInstances(ctx, computedriver.InstanceConfig{
		ImageID: "img-1", InstanceType: "Standard_B1s", Region: "eastus",
	}, 1); err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	alarms, err := cloudP.Monitor.DescribeAlarms(ctx, []string{"cpu-hot"})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(alarms) != 1 {
		t.Fatalf("alarms = %d, want 1 (metric not registered with driver)", len(alarms))
	}

	if alarms[0].State != "ALARM" {
		t.Fatalf("alarm state = %q, want ALARM (metric not evaluated)", alarms[0].State)
	}
}

// TestMetricAlertActionsWireActionGroup covers the actions finding:
// properties.actions[].actionGroupId must reach the driver's AlarmActions so
// a breaching alert can actually notify the linked action group, mirroring
// the AWS CloudWatch alarm -> SNS AlarmActions wiring.
func TestMetricAlertActionsWireActionGroup(t *testing.T) {
	ts, cloudP := newMonitorServer(t)
	ctx := context.Background()

	const actionGroupID = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/actionGroups/ag1"

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/cpu-notify" + apiVer

	body := `{
		"location": "global",
		"properties": {
			"windowSize": "PT5M",
			"actions": [{"actionGroupId": "` + actionGroupID + `", "webHookProperties": {"k": "v"}}],
			"criteria": {"allOf": [{
				"metricName": "Percentage CPU",
				"metricNamespace": "Microsoft.Compute/virtualMachines",
				"operator": "GreaterThan",
				"threshold": 20,
				"timeAggregation": "Average"
			}]}
		}
	}`

	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200", code)
	}

	alarms, err := cloudP.Monitor.DescribeAlarms(ctx, []string{"cpu-notify"})
	if err != nil {
		t.Fatalf("DescribeAlarms: %v", err)
	}

	if len(alarms) != 1 {
		t.Fatalf("alarms = %d, want 1", len(alarms))
	}

	if len(alarms[0].AlarmActions) != 1 || alarms[0].AlarmActions[0] != actionGroupID {
		t.Fatalf("AlarmActions = %v, want [%s] (actionGroupId dropped)", alarms[0].AlarmActions, actionGroupID)
	}
}

// TestMetricAlertListScopedToResourceGroup covers the store-isolation finding:
// a metricAlert created in rg-2 must not appear in a list scoped to rg-1, and
// a metricAlert of the same name in both resource groups must not collide.
func TestMetricAlertListScopedToResourceGroup(t *testing.T) {
	ts, _ := newMonitorServer(t)

	body := `{"location":"global","properties":{"windowSize":"PT5M",
		"criteria":{"allOf":[{"metricName":"Percentage CPU","operator":"GreaterThan","threshold":20,"timeAggregation":"Average"}]}}}`

	rg1URL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts/shared-name" + apiVer
	rg2URL := "/subscriptions/sub-1/resourceGroups/rg-2/providers/Microsoft.Insights/metricAlerts/shared-name" + apiVer

	if code, _ := doJSON(t, ts, http.MethodPut, rg1URL, body); code != http.StatusOK {
		t.Fatalf("PUT rg-1 status = %d, want 200", code)
	}

	if code, _ := doJSON(t, ts, http.MethodPut, rg2URL, body); code != http.StatusOK {
		t.Fatalf("PUT rg-2 status = %d, want 200", code)
	}

	listRG1URL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts" + apiVer

	_, listed := doJSON(t, ts, http.MethodGet, listRG1URL, "")

	value, _ := listed["value"].([]any)
	if len(value) != 1 {
		t.Fatalf("rg-1 list len = %d, want 1 (rg-2's alert leaked in, or rg-1's was lost to the name collision)", len(value))
	}

	// Deleting the rg-2 alert must not remove rg-1's same-named alert.
	if code, _ := doJSON(t, ts, http.MethodDelete, rg2URL, ""); code != http.StatusOK {
		t.Fatalf("DELETE rg-2 status = %d, want 200", code)
	}

	if code, _ := doJSON(t, ts, http.MethodGet, rg1URL, ""); code != http.StatusOK {
		t.Fatalf("GET rg-1 after deleting rg-2's same-named alert status = %d, want 200 (cross-resource-group delete)", code)
	}
}

// TestMetricAlertListEmptyIsArray covers finding 10: an empty list is
// {"value":[]}, never {"value":null}.
func TestMetricAlertListEmptyIsArray(t *testing.T) {
	ts, _ := newMonitorServer(t)

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/metricAlerts" + apiVer

	req, _ := http.NewRequest(http.MethodGet, ts.URL+url, http.NoBody)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"value":[]`) {
		t.Fatalf("empty list body = %s, want value:[]", raw)
	}
}

func TestActionGroupCRUD(t *testing.T) {
	ts, _ := newMonitorServer(t)

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/actionGroups/ag1" + apiVer

	body := `{"location":"Global","properties":{"groupShortName":"sample","enabled":true,
		"emailReceivers":[{"name":"oncall","emailAddress":"a@b.com"}]}}`

	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", code)
	}

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code)
	}

	props, _ := got["properties"].(map[string]any)
	if props["groupShortName"] != "sample" {
		t.Errorf("groupShortName = %v, want sample", props["groupShortName"])
	}

	if props["emailReceivers"] == nil {
		t.Errorf("emailReceivers dropped")
	}

	if code, _ := doJSON(t, ts, http.MethodDelete, url, ""); code != http.StatusOK {
		t.Errorf("DELETE status = %d, want 200", code)
	}

	if code, _ := doJSON(t, ts, http.MethodGet, url, ""); code != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", code)
	}
}

func TestActivityLogAlertCRUD(t *testing.T) {
	ts, _ := newMonitorServer(t)

	const url = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/activityLogAlerts/ala1" + apiVer

	body := `{"location":"Global","properties":{"enabled":true,
		"scopes":["/subscriptions/sub-1"],
		"condition":{"allOf":[{"field":"category","equals":"Administrative"}]}}}`

	if code, _ := doJSON(t, ts, http.MethodPut, url, body); code != http.StatusCreated {
		t.Fatalf("PUT status = %d, want 201", code)
	}

	code, got := doJSON(t, ts, http.MethodGet, url, "")
	if code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", code)
	}

	if props, _ := got["properties"].(map[string]any); props["condition"] == nil {
		t.Errorf("condition dropped: %+v", got["properties"])
	}
}

// TestInsightsDeleteMissingIsNoContent asserts DELETE on a metricAlert,
// actionGroup or activityLogAlert that does not exist returns 204 No Content
// (idempotent), matching the Azure Monitor REST contract, not a 404.
func TestInsightsDeleteMissingIsNoContent(t *testing.T) {
	ts, _ := newMonitorServer(t)

	for _, typ := range []string{"metricAlerts", "actionGroups", "activityLogAlerts"} {
		url := "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Insights/" + typ + "/nope" + apiVer

		if code, body := doJSON(t, ts, http.MethodDelete, url, ""); code != http.StatusNoContent {
			t.Errorf("DELETE missing %s = %d, want 204; body = %v", typ, code, body)
		}
	}
}
