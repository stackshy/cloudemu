package monitor_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

// HTTP-level tests for the Azure Monitor handler. The real armmonitor SDK
// has tight schema requirements for metric criteria; we cover the wire-level
// happy path here and rely on the existing pattern from other Azure
// handlers (real-SDK round-trip is a follow-up).
func TestMonitorAlertCRUD(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Monitor: cloudP.Monitor})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	const alertURL = "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/metricAlerts/alert-1?api-version=2018-03-01"

	body := bytes.NewBufferString(`{
		"location": "global",
		"properties": {
			"description": "test alert",
			"severity": 3,
			"enabled": true,
			"scopes": ["/subscriptions/sub-1/resourceGroups/rg-1"],
			"evaluationFrequency": "PT1M",
			"windowSize": "PT5M"
		}
	}`)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+alertURL, body)
	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("PUT status=%d want 201", resp.StatusCode)
	}

	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)

	if got["name"] != "alert-1" {
		t.Errorf("name=%v want alert-1", got["name"])
	}

	// Get the alert.
	getReq, _ := http.NewRequest(http.MethodGet, ts.URL+alertURL, http.NoBody)

	getResp, err := ts.Client().Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("GET status=%d want 200", getResp.StatusCode)
	}

	// List alerts.
	listURL := "/subscriptions/sub-1/resourceGroups/rg-1/providers/microsoft.insights/metricAlerts?api-version=2018-03-01"

	listResp, err := ts.Client().Get(ts.URL + listURL)
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()

	if listResp.StatusCode != http.StatusOK {
		t.Errorf("LIST status=%d", listResp.StatusCode)
	}

	var list struct {
		Value []map[string]any `json:"value"`
	}

	_ = json.NewDecoder(listResp.Body).Decode(&list)

	if len(list.Value) == 0 {
		t.Error("expected at least 1 alert in list")
	}

	// Delete the alert.
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+alertURL, http.NoBody)

	delResp, err := ts.Client().Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Errorf("DELETE status=%d", delResp.StatusCode)
	}
}
