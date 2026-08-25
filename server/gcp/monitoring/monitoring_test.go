package monitoring_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// policyID extracts the opaque id from an alert policy's canonical name
// (projects/{p}/alertPolicies/{id}).
func policyID(t *testing.T, created map[string]any) string {
	t.Helper()

	name, ok := created["name"].(string)
	if !ok || name == "" {
		t.Fatal("create response missing canonical name")
	}

	return name[strings.LastIndex(name, "/")+1:]
}

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

	canonical, ok := got["name"].(string)
	if !ok || canonical == "" {
		t.Fatal("missing canonical name in response")
	}

	// Real Cloud Monitoring addresses a policy by its opaque numeric id, not by
	// displayName — extract the id assigned on create.
	id := canonical[strings.LastIndex(canonical, "/")+1:]

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
	getResp, err := ts.Client().Get(ts.URL + collURL + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("get status=%d", getResp.StatusCode)
	}

	// Delete
	delReq, _ := http.NewRequest(http.MethodDelete, ts.URL+collURL+"/"+id, http.NoBody)

	delResp, err := ts.Client().Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Errorf("delete status=%d", delResp.StatusCode)
	}
}

// TestMonitoringAlertPolicySemantics guards the #321 fix: a policy's
// conditions/combiner/enabled/userLabels must round-trip on Get (not be
// dropped for a hardcoded skeleton), and PATCH must apply.
func TestMonitoringAlertPolicySemantics(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	const collURL = "/v3/projects/p1/alertPolicies"

	create := bytes.NewBufferString(`{
		"displayName": "cpu-alert",
		"combiner": "AND",
		"enabled": true,
		"userLabels": {"team": "sre"},
		"conditions": [{"displayName": "cpu>80"}]
	}`)

	resp, err := ts.Client().Post(ts.URL+collURL, "application/json", create)
	if err != nil {
		t.Fatal(err)
	}

	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	id := policyID(t, created)

	// Get must reflect what was created.
	getResp, err := ts.Client().Get(ts.URL + collURL + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var got map[string]any
	_ = json.NewDecoder(getResp.Body).Decode(&got)

	if got["combiner"] != "AND" {
		t.Errorf("combiner=%v want AND (dropped on read)", got["combiner"])
	}

	if got["enabled"] != true {
		t.Errorf("enabled=%v want true", got["enabled"])
	}

	if ul, _ := got["userLabels"].(map[string]any); ul["team"] != "sre" {
		t.Errorf("userLabels=%v want team=sre", got["userLabels"])
	}

	if conds, _ := got["conditions"].([]any); len(conds) != 1 {
		t.Errorf("conditions=%v want 1", got["conditions"])
	}

	// PATCH updates the combiner but OMITS enabled — a partial patch must NOT
	// silently disable the policy (regression guard for the omitted-field bug).
	patch := bytes.NewBufferString(`{"combiner": "OR"}`)
	patchReq, _ := http.NewRequest(http.MethodPatch, ts.URL+collURL+"/"+id, patch)
	patchReq.Header.Set("Content-Type", "application/json")

	patchResp, err := ts.Client().Do(patchReq)
	if err != nil {
		t.Fatal(err)
	}
	defer patchResp.Body.Close()

	var patched map[string]any
	_ = json.NewDecoder(patchResp.Body).Decode(&patched)

	if patched["combiner"] != "OR" {
		t.Errorf("after PATCH combiner=%v want OR", patched["combiner"])
	}

	if patched["enabled"] != true {
		t.Errorf("after PATCH omitting enabled, enabled=%v want true (must not silently disable)", patched["enabled"])
	}
}

// TestMonitoringNonThresholdCondition guards that a non-threshold condition
// (conditionAbsent) round-trips instead of being dropped.
func TestMonitoringNonThresholdCondition(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Monitoring: cloudP.CloudMonitoring})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	const collURL = "/v3/projects/p1/alertPolicies"

	create := bytes.NewBufferString(`{
		"displayName": "absent-alert",
		"combiner": "OR",
		"conditions": [{"displayName": "no data", "conditionAbsent": {"duration": "300s"}}]
	}`)

	resp, err := ts.Client().Post(ts.URL+collURL, "application/json", create)
	if err != nil {
		t.Fatal(err)
	}

	var created map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	getResp, err := ts.Client().Get(ts.URL + collURL + "/" + policyID(t, created))
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var got map[string]any
	_ = json.NewDecoder(getResp.Body).Decode(&got)

	conds, _ := got["conditions"].([]any)
	if len(conds) != 1 {
		t.Fatalf("conditions=%v want 1", got["conditions"])
	}

	c0, _ := conds[0].(map[string]any)
	if _, ok := c0["conditionAbsent"]; !ok {
		t.Errorf("conditionAbsent dropped on round-trip: %+v", c0)
	}
}
