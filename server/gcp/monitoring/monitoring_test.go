package monitoring_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

// HTTP-level test for the GCP Cloud Monitoring handler. Real
// cloud.google.com/go/monitoring uses gRPC by default; the REST surface here
// covers HTTP-level wire-format validation.
func TestMonitoringAlertPolicyCRUD(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	const collURL = "/v3/projects/p1/alertPolicies"

	body := bytes.NewBufferString(`{
		"displayName": "high-cpu",
		"combiner": "OR",
		"enabled": true
	}`)

	resp, err := ts.Client().Post(ts.URL+collURL, "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("create status=%d", resp.StatusCode)
	}

	var got map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&got)

	if got["displayName"] != "high-cpu" {
		t.Errorf("displayName=%v", got["displayName"])
	}

	if name, ok := got["name"].(string); !ok || name == "" {
		t.Errorf("missing canonical name in response")
	}

	// List
	listResp, err := ts.Client().Get(ts.URL + collURL)
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()

	var list struct {
		AlertPolicies []map[string]any `json:"alertPolicies"`
	}

	_ = json.NewDecoder(listResp.Body).Decode(&list)

	if len(list.AlertPolicies) == 0 {
		t.Error("list returned no policies")
	}

	// Get
	getResp, err := ts.Client().Get(ts.URL + collURL + "/high-cpu")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("get status=%d", getResp.StatusCode)
	}

	// Delete
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+collURL+"/high-cpu", http.NoBody)

	delResp, err := ts.Client().Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Errorf("delete status=%d", delResp.StatusCode)
	}
}
