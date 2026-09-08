package iothub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
	iothubsrv "github.com/stackshy/cloudemu/v2/server/azure/iothub"
)

const (
	apiVer  = "?api-version=2023-06-30"
	hubBase = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Devices/IotHubs/"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := iothub.New(config.NewOptions())
	srv := httptest.NewServer(iothubsrv.New(mock))
	t.Cleanup(srv.Close)

	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, raw
}

func createHub(t *testing.T, srv *httptest.Server) {
	t.Helper()

	body := `{"location":"westus","sku":{"name":"S1","capacity":1},"tags":{"env":"dev"},` +
		`"properties":{"eventHubEndpoints":{"events":{"partitionCount":2,"retentionTimeInDays":1}}}}`

	status, _ := do(t, srv, http.MethodPut, hubBase+"hub1"+apiVer, body)
	if status != http.StatusCreated {
		t.Fatalf("create hub status = %d, want 201", status)
	}
}

func TestHubCreateGetByteStableKeysNotEchoed(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	s1, b1 := do(t, srv, http.MethodGet, hubBase+"hub1"+apiVer, "")
	s2, b2 := do(t, srv, http.MethodGet, hubBase+"hub1"+apiVer, "")

	if s1 != 200 || s2 != 200 {
		t.Fatalf("get status = %d/%d", s1, s2)
	}

	if string(b1) != string(b2) {
		t.Errorf("GET not byte-identical:\n%s\n%s", b1, b2)
	}

	var resp map[string]any
	if err := json.Unmarshal(b1, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	props := resp["properties"].(map[string]any)
	if props["hostName"] != "hub1.azure-devices.net" {
		t.Errorf("hostName = %v", props["hostName"])
	}

	if _, ok := props["authorizationPolicies"]; ok {
		t.Error("keys leaked on plain GET (authorizationPolicies present)")
	}

	if !bytes.Contains(b1, []byte(`"provisioningState":"Succeeded"`)) {
		t.Error("provisioningState missing")
	}
}

func TestListKeysByteStableAndGetKeysForKeyName(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	s1, b1 := do(t, srv, http.MethodPost, hubBase+"hub1/listkeys"+apiVer, "")
	s2, b2 := do(t, srv, http.MethodPost, hubBase+"hub1/listkeys"+apiVer, "")

	if s1 != 200 || s2 != 200 {
		t.Fatalf("listkeys status = %d/%d", s1, s2)
	}

	if string(b1) != string(b2) {
		t.Errorf("listkeys not byte-stable:\n%s\n%s", b1, b2)
	}

	if !bytes.Contains(b1, []byte(`"iothubowner"`)) || !bytes.Contains(b1, []byte(`"primaryKey"`)) {
		t.Errorf("listkeys missing default policy/keys: %s", b1)
	}

	// getKeysForKeyName for a single policy.
	s3, b3 := do(t, srv, http.MethodPost, hubBase+"hub1/IotHubKeys/iothubowner/listkeys"+apiVer, "")
	if s3 != 200 {
		t.Fatalf("getKeysForKeyName status = %d", s3)
	}

	var pol map[string]any
	if err := json.Unmarshal(b3, &pol); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}

	if pol["keyName"] != "iothubowner" || pol["primaryKey"] == "" {
		t.Errorf("getKeysForKeyName = %v", pol)
	}
}

func TestConsumerGroupLifecycle(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	cgBase := hubBase + "hub1/eventHubEndpoints/events/ConsumerGroups/"

	// $Default seeded.
	sList, bList := do(t, srv, http.MethodGet, cgBase[:len(cgBase)-1]+apiVer, "")
	if sList != 200 || !bytes.Contains(bList, []byte(`"$Default"`)) {
		t.Fatalf("default consumer group missing: status=%d body=%s", sList, bList)
	}

	// Create a new consumer group.
	sCreate, bCreate := do(t, srv, http.MethodPut, cgBase+"telemetry"+apiVer, "")
	if sCreate != http.StatusCreated {
		t.Fatalf("create cg status = %d, want 201", sCreate)
	}

	if !bytes.Contains(bCreate, []byte(`"Microsoft.Devices/IotHubs/EventHubEndpoints/ConsumerGroups"`)) {
		t.Errorf("cg type wrong: %s", bCreate)
	}

	// Get it.
	sGet, _ := do(t, srv, http.MethodGet, cgBase+"telemetry"+apiVer, "")
	if sGet != 200 {
		t.Errorf("get cg status = %d", sGet)
	}

	// List now has both.
	_, bList2 := do(t, srv, http.MethodGet, cgBase[:len(cgBase)-1]+apiVer, "")
	if !bytes.Contains(bList2, []byte(`"$Default"`)) || !bytes.Contains(bList2, []byte(`"telemetry"`)) {
		t.Errorf("list missing groups: %s", bList2)
	}

	// Delete it.
	sDel, _ := do(t, srv, http.MethodDelete, cgBase+"telemetry"+apiVer, "")
	if sDel != 200 {
		t.Errorf("delete cg status = %d, want 200", sDel)
	}
}

func TestConsumerGroupNameCollidesWithHub(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	// A consumer group named identically to its hub must resolve to the CG, not
	// silently fall back to the hub (forward-anchored hubTail).
	cgPath := hubBase + "hub1/eventHubEndpoints/events/ConsumerGroups/hub1" + apiVer

	if s, _ := do(t, srv, http.MethodPut, cgPath, ""); s != http.StatusCreated {
		t.Fatalf("create cg named after hub: status = %d, want 201", s)
	}

	sGet, bGet := do(t, srv, http.MethodGet, cgPath, "")
	if sGet != 200 {
		t.Fatalf("get cg named after hub: status = %d", sGet)
	}

	if !bytes.Contains(bGet, []byte(`ConsumerGroups`)) || bytes.Contains(bGet, []byte(`"hostName"`)) {
		t.Errorf("get returned the hub body, not the consumer group: %s", bGet)
	}
}

func TestHubNamedEventsConsumerGroups(t *testing.T) {
	srv := newServer(t)

	// A hub literally named "events" collides with the eventHubEndpoints/events
	// segment; listing its consumer groups must still return $Default, not 404.
	body := `{"location":"westus","sku":{"name":"S1","capacity":1}}`
	if s, _ := do(t, srv, http.MethodPut, hubBase+"events"+apiVer, body); s != http.StatusCreated {
		t.Fatalf("create hub named events: status = %d, want 201", s)
	}

	listPath := hubBase + "events/eventHubEndpoints/events/ConsumerGroups" + apiVer

	s, b := do(t, srv, http.MethodGet, listPath, "")
	if s != 200 || !bytes.Contains(b, []byte(`"$Default"`)) {
		t.Fatalf("list cg for hub named events: status=%d body=%s", s, b)
	}
}

func TestPatchTagsReplace(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	status, body := do(t, srv, http.MethodPatch, hubBase+"hub1"+apiVer, `{"tags":{"team":"iot"}}`)
	if status != 200 {
		t.Fatalf("patch status = %d", status)
	}

	var resp map[string]any
	_ = json.Unmarshal(body, &resp)

	tags, _ := resp["tags"].(map[string]any)
	if _, ok := tags["env"]; ok {
		t.Errorf("tags not replaced: %v", tags)
	}

	if tags["team"] != "iot" {
		t.Errorf("new tag missing: %v", tags)
	}
}

func TestDeleteHubThen404(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	sDel, _ := do(t, srv, http.MethodDelete, hubBase+"hub1"+apiVer, "")
	if sDel != 200 {
		t.Fatalf("delete hub status = %d", sDel)
	}

	sGet, _ := do(t, srv, http.MethodGet, hubBase+"hub1"+apiVer, "")
	if sGet != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", sGet)
	}

	// Idempotent delete → 204.
	sDel2, _ := do(t, srv, http.MethodDelete, hubBase+"hub1"+apiVer, "")
	if sDel2 != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", sDel2)
	}
}

func TestListHubsByGroupAndSubscription(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	sRG, bRG := do(t, srv, http.MethodGet,
		"/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Devices/IotHubs"+apiVer, "")
	if sRG != 200 || !bytes.Contains(bRG, []byte(`"hub1"`)) {
		t.Errorf("list by RG: status=%d body=%s", sRG, bRG)
	}

	sSub, bSub := do(t, srv, http.MethodGet,
		"/subscriptions/sub1/providers/Microsoft.Devices/IotHubs"+apiVer, "")
	if sSub != 200 || !bytes.Contains(bSub, []byte(`"hub1"`)) {
		t.Errorf("list by subscription: status=%d body=%s", sSub, bSub)
	}
}

func TestConsumerGroupParentNotFound(t *testing.T) {
	srv := newServer(t)

	status, body := do(t, srv, http.MethodPut,
		hubBase+"ghost/eventHubEndpoints/events/ConsumerGroups/cg1"+apiVer, "")
	if status != http.StatusNotFound {
		t.Fatalf("cg under missing hub = %d, want 404", status)
	}

	if !bytes.Contains(body, []byte("ParentResourceNotFound")) {
		t.Errorf("want ParentResourceNotFound, got %s", body)
	}
}

func TestRoutingRoundTrip(t *testing.T) {
	srv := newServer(t)

	body := `{"location":"westus","sku":{"name":"S1","capacity":1},` +
		`"properties":{"routing":{"routes":[{"name":"r1","source":"DeviceMessages","isEnabled":true}]}}}`

	status, _ := do(t, srv, http.MethodPut, hubBase+"hubr"+apiVer, body)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d", status)
	}

	_, get := do(t, srv, http.MethodGet, hubBase+"hubr"+apiVer, "")
	if !bytes.Contains(get, []byte(`"routes":[{"name":"r1","source":"DeviceMessages","isEnabled":true}]`)) {
		t.Errorf("routing not round-tripped verbatim: %s", get)
	}
}

func TestMatchesAndUnknownSubResource(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	status, _ := do(t, srv, http.MethodGet, hubBase+"hub1/bogusSub"+apiVer, "")
	if status != http.StatusNotFound {
		t.Errorf("unknown sub-resource = %d, want 404", status)
	}

	// Method not allowed on a hub.
	statusM, _ := do(t, srv, http.MethodPost, hubBase+"hub1"+apiVer, "")
	if statusM != http.StatusMethodNotAllowed {
		t.Errorf("POST on hub = %d, want 405", statusM)
	}
}

func TestPatchMissingHub404(t *testing.T) {
	srv := newServer(t)

	status, _ := do(t, srv, http.MethodPatch, hubBase+"ghost"+apiVer, `{"tags":{"a":"b"}}`)
	if status != http.StatusNotFound {
		t.Errorf("patch missing hub = %d, want 404", status)
	}
}
