package cloudrun_test

import (
	"net/http"
	"testing"
)

func servicesURL(base string) string {
	return base + "/v2/projects/" + project + "/locations/" + location + "/services"
}

// numericEnumServiceBody mirrors what the Cloud Run v2 GAPIC REST client
// (cloud.google.com/go/run/apiv2) puts on the wire: every enum field is a raw
// integer (protojson UseEnumNumbers), not its canonical name.
func numericEnumServiceBody() map[string]any {
	return map[string]any{
		"ingress":     1, // INGRESS_TRAFFIC_ALL
		"launchStage": 4, // GA
		"template": map[string]any{
			"executionEnvironment": 2,                           // EXECUTION_ENVIRONMENT_GEN2
			"vpcAccess":            map[string]any{"egress": 1}, // ALL_TRAFFIC
			"containers":           []map[string]any{{"image": "busybox"}},
		},
		"traffic": []map[string]any{{"type": 1, "percent": 100}}, // ..._LATEST
	}
}

// TestCreateServiceAcceptsNumericEnums proves the wire handler decodes the
// integer enum values the GAPIC v2 REST client sends and echoes them back as
// the canonical names every string client (Terraform, gcloud) expects.
func TestCreateServiceAcceptsNumericEnums(t *testing.T) {
	srv := newServer(t, nil)

	createOp := postJSON(t, servicesURL(srv.URL)+"?serviceId=enum-svc", numericEnumServiceBody())
	if done, _ := createOp["done"].(bool); !done {
		t.Fatalf("create op not done (numeric enums rejected?): %+v", createOp)
	}

	got, code := getJSON(t, servicesURL(srv.URL)+"/enum-svc")
	if code != http.StatusOK {
		t.Fatalf("get service code = %d body = %+v", code, got)
	}

	if got["ingress"] != "INGRESS_TRAFFIC_ALL" {
		t.Errorf("ingress = %v, want INGRESS_TRAFFIC_ALL", got["ingress"])
	}

	if got["launchStage"] != "GA" {
		t.Errorf("launchStage = %v, want GA", got["launchStage"])
	}

	tmpl, _ := got["template"].(map[string]any)
	if tmpl["executionEnvironment"] != "EXECUTION_ENVIRONMENT_GEN2" {
		t.Errorf("executionEnvironment = %v, want EXECUTION_ENVIRONMENT_GEN2", tmpl["executionEnvironment"])
	}

	vpc, _ := tmpl["vpcAccess"].(map[string]any)
	if vpc["egress"] != "ALL_TRAFFIC" {
		t.Errorf("vpcAccess.egress = %v, want ALL_TRAFFIC", vpc["egress"])
	}

	traffic, _ := got["traffic"].([]any)
	if len(traffic) != 1 {
		t.Fatalf("traffic len = %d, want 1", len(traffic))
	}

	t0, _ := traffic[0].(map[string]any)
	if t0["type"] != "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST" {
		t.Errorf("traffic[0].type = %v, want ..._LATEST", t0["type"])
	}
}

// TestUpdateServiceAcceptsNumericEnums proves the full-object round-trip the
// GAPIC client performs on UpdateService — which re-sends every field it read,
// including output-only enums (terminalCondition.state, trafficStatuses.type) —
// decodes without a 400.
func TestUpdateServiceAcceptsNumericEnums(t *testing.T) {
	srv := newServer(t, nil)

	postJSON(t, servicesURL(srv.URL)+"?serviceId=enum-svc", numericEnumServiceBody())

	// A body carrying numeric output-only enums, as the client echoes on update.
	patch := map[string]any{
		"ingress":           1,
		"launchStage":       4,
		"terminalCondition": map[string]any{"type": "Ready", "state": 4}, // CONDITION_SUCCEEDED
		"trafficStatuses":   []map[string]any{{"type": 1, "percent": 100}},
		"template": map[string]any{
			"executionEnvironment": 1, // GEN1
			"containers":           []map[string]any{{"image": "busybox:v2"}},
		},
	}

	req, _ := http.NewRequest(http.MethodPatch, servicesURL(srv.URL)+"/enum-svc", encodeBody(t, patch))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH: %v", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update code = %d, want 200 (numeric enums rejected?)", resp.StatusCode)
	}

	got, _ := getJSON(t, servicesURL(srv.URL)+"/enum-svc")
	tmpl, _ := got["template"].(map[string]any)
	if tmpl["executionEnvironment"] != "EXECUTION_ENVIRONMENT_GEN1" {
		t.Errorf("executionEnvironment after update = %v, want GEN1", tmpl["executionEnvironment"])
	}
}

// TestDeleteServiceReturnsDeletedResource proves the delete LRO inlines the
// removed Service in its response, as real Cloud Run does and the GAPIC
// DeleteServiceOperation.Wait requires.
func TestDeleteServiceReturnsDeletedResource(t *testing.T) {
	srv := newServer(t, nil)

	postJSON(t, servicesURL(srv.URL)+"?serviceId=del-svc",
		map[string]any{"template": map[string]any{"containers": []map[string]any{{"image": "busybox"}}}})

	req, _ := http.NewRequest(http.MethodDelete, servicesURL(srv.URL)+"/del-svc", nil)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}

	delOp := decode(t, resp)
	if done, _ := delOp["done"].(bool); !done {
		t.Fatalf("delete op not done: %+v", delOp)
	}

	respObj, _ := delOp["response"].(map[string]any)
	if respObj["@type"] != "type.googleapis.com/google.cloud.run.v2.Service" {
		t.Fatalf("delete response @type = %v, want Service", respObj["@type"])
	}

	wantName := "projects/" + project + "/locations/" + location + "/services/del-svc"
	if respObj["name"] != wantName {
		t.Errorf("deleted service name = %v, want %v", respObj["name"], wantName)
	}
}

// TestSuccessConditionOmitsReason proves a successful terminalCondition carries
// no reason — real Cloud Run leaves reason unset on CONDITION_SUCCEEDED (reason
// is a typed enum, and no value names "Ready").
func TestSuccessConditionOmitsReason(t *testing.T) {
	srv := newServer(t, nil)

	postJSON(t, servicesURL(srv.URL)+"?serviceId=cond-svc",
		map[string]any{"template": map[string]any{"containers": []map[string]any{{"image": "busybox"}}}})

	got, _ := getJSON(t, servicesURL(srv.URL)+"/cond-svc")

	tc, _ := got["terminalCondition"].(map[string]any)
	if tc["state"] != "CONDITION_SUCCEEDED" {
		t.Fatalf("terminalCondition.state = %v, want CONDITION_SUCCEEDED", tc["state"])
	}

	if _, ok := tc["reason"]; ok {
		t.Errorf("terminalCondition unexpectedly carries reason = %v", tc["reason"])
	}
}
