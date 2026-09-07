package communication_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/communication"
	communicationsrv "github.com/stackshy/cloudemu/v2/server/azure/communication"
)

const (
	apiVer   = "?api-version=2023-04-01"
	basePath = "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Communication/communicationServices/"
)

type wireResp struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags"`
	Identity *struct {
		Type        string `json:"type"`
		PrincipalID string `json:"principalId"`
		TenantID    string `json:"tenantId"`
	} `json:"identity"`
	Properties struct {
		ProvisioningState   string   `json:"provisioningState"`
		HostName            string   `json:"hostName"`
		DataLocation        string   `json:"dataLocation"`
		ImmutableResourceID string   `json:"immutableResourceId"`
		NotificationHubID   string   `json:"notificationHubId"`
		LinkedDomains       []string `json:"linkedDomains"`
		Version             string   `json:"version"`
	} `json:"properties"`
}

type keysResp struct {
	PrimaryKey                string `json:"primaryKey"`
	SecondaryKey              string `json:"secondaryKey"`
	PrimaryConnectionString   string `json:"primaryConnectionString"`
	SecondaryConnectionString string `json:"secondaryConnectionString"`
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	mock := communication.New(config.NewOptions())
	h := communicationsrv.New(mock)
	srv := httptest.NewServer(h)
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

func decode(t *testing.T, raw []byte) wireResp {
	t.Helper()

	var out wireResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}

	return out
}

const createBody = `{
	"location": "global",
	"tags": {"env": "dev"},
	"identity": {"type": "SystemAssigned"},
	"properties": {
		"dataLocation": "United States",
		"linkedDomains": ["/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.Communication/emailServices/es/domains/d"]
	}
}`

func TestWireCreateAndComputedFields(t *testing.T) {
	srv := newServer(t)
	path := basePath + "acs1" + apiVer

	code, raw := do(t, srv, http.MethodPut, path, createBody)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201 (%s)", code, raw)
	}

	got := decode(t, raw)
	if got.Type != "Microsoft.Communication/communicationServices" {
		t.Errorf("type = %q", got.Type)
	}

	if got.Location != "global" {
		t.Errorf("location = %q, want global", got.Location)
	}

	if got.Properties.HostName != "acs1.communication.azure.com" {
		t.Errorf("hostName = %q", got.Properties.HostName)
	}

	if got.Properties.DataLocation != "United States" {
		t.Errorf("dataLocation = %q", got.Properties.DataLocation)
	}

	if got.Properties.ImmutableResourceID == "" {
		t.Errorf("immutableResourceId empty")
	}

	if got.Properties.ProvisioningState != "Succeeded" {
		t.Errorf("provisioningState = %q, want Succeeded", got.Properties.ProvisioningState)
	}

	if got.Identity == nil || got.Identity.PrincipalID == "" || got.Identity.TenantID == "" {
		t.Errorf("identity ids = %+v", got.Identity)
	}

	if len(got.Properties.LinkedDomains) != 1 {
		t.Errorf("linkedDomains = %+v", got.Properties.LinkedDomains)
	}
}

func TestWireGetIsByteStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "acs1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	_, a := do(t, srv, http.MethodGet, path, "")
	_, b := do(t, srv, http.MethodGet, path, "")

	if !bytes.Equal(a, b) {
		t.Errorf("GET not byte-stable:\n a=%s\n b=%s", a, b)
	}
}

func TestWirePatchMergesAndPreservesComputedAndDataLocation(t *testing.T) {
	srv := newServer(t)
	path := basePath + "acs1" + apiVer

	_, raw := do(t, srv, http.MethodPut, path, createBody)
	before := decode(t, raw)

	// PATCH mutates tags and (illegally) tries to change dataLocation. The mock
	// pins dataLocation to its create-time value.
	patch := `{"tags": {"env": "prod"}, "properties": {"dataLocation": "Europe", "notificationHubId": "/subscriptions/sub1/resourceGroups/rg1/providers/Microsoft.NotificationHubs/namespaces/ns/notificationHubs/hub"}}`

	code, praw := do(t, srv, http.MethodPatch, path, patch)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, praw)
	}

	after := decode(t, praw)
	if after.Tags["env"] != "prod" {
		t.Errorf("tags = %+v", after.Tags)
	}

	if after.Properties.DataLocation != "United States" {
		t.Errorf("dataLocation = %q, want United States (immutable)", after.Properties.DataLocation)
	}

	if after.Properties.NotificationHubID == "" {
		t.Errorf("notificationHubId not applied on patch")
	}

	if after.Properties.HostName != before.Properties.HostName ||
		after.Properties.ImmutableResourceID != before.Properties.ImmutableResourceID {
		t.Errorf("computed fields drifted on patch")
	}

	// linkedDomains was not mentioned in the PATCH, so it must survive.
	if len(after.Properties.LinkedDomains) != 1 {
		t.Errorf("linkedDomains not preserved on patch: %+v", after.Properties.LinkedDomains)
	}
}

func TestWirePatchOnMissingIs404(t *testing.T) {
	srv := newServer(t)
	path := basePath + "nope" + apiVer

	if code, _ := do(t, srv, http.MethodPatch, path, `{"tags":{"a":"b"}}`); code != http.StatusNotFound {
		t.Errorf("patch missing = %d, want 404", code)
	}
}

func TestWireListKeysIsStable(t *testing.T) {
	srv := newServer(t)
	path := basePath + "acs1" + apiVer

	if code, raw := do(t, srv, http.MethodPut, path, createBody); code != http.StatusCreated {
		t.Fatalf("create: %d (%s)", code, raw)
	}

	keysPath := basePath + "acs1/listKeys" + apiVer

	code, raw := do(t, srv, http.MethodPost, keysPath, "")
	if code != http.StatusOK {
		t.Fatalf("listKeys status = %d (%s)", code, raw)
	}

	var first keysResp
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("unmarshal keys: %v", err)
	}

	if first.PrimaryKey == "" || first.PrimaryConnectionString == "" {
		t.Fatalf("empty keys: %+v", first)
	}

	_, raw2 := do(t, srv, http.MethodPost, keysPath, "")
	if !bytes.Equal(raw, raw2) {
		t.Errorf("listKeys not stable:\n a=%s\n b=%s", raw, raw2)
	}

	// regenerateKey returns the same deterministic keys.
	regenPath := basePath + "acs1/regenerateKey" + apiVer

	_, raw3 := do(t, srv, http.MethodPost, regenPath, `{"keyType":"Primary"}`)
	if !bytes.Equal(raw, raw3) {
		t.Errorf("regenerateKey not stable:\n a=%s\n c=%s", raw, raw3)
	}
}

func TestWireDeleteIdempotent(t *testing.T) {
	srv := newServer(t)
	path := basePath + "acs1" + apiVer

	do(t, srv, http.MethodPut, path, createBody)

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusOK {
		t.Errorf("first delete = %d, want 200", code)
	}

	if code, _ := do(t, srv, http.MethodDelete, path, ""); code != http.StatusNoContent {
		t.Errorf("second delete = %d, want 204", code)
	}

	if code, _ := do(t, srv, http.MethodGet, path, ""); code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", code)
	}
}

func TestWireList(t *testing.T) {
	srv := newServer(t)

	for _, n := range []string{"a", "b"} {
		do(t, srv, http.MethodPut, basePath+n+apiVer, createBody)
	}

	code, raw := do(t, srv, http.MethodGet, basePath+apiVer, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}

	var out struct {
		Value []wireResp `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}

	if len(out.Value) != 2 {
		t.Errorf("list len = %d, want 2", len(out.Value))
	}
}
