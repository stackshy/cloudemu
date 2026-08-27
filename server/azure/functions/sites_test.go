package functions_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

func newFuncServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	return srv
}

func siteInRG(rg, name string) string {
	return "/subscriptions/" + subID + "/resourceGroups/" + rg + "/providers/Microsoft.Web/sites/" + name
}

// TestSiteLocationAndProvisioningState covers findings 1 and 2: the creation
// region survives a GET and provisioningState is present.
func TestSiteLocationAndProvisioningState(t *testing.T) {
	srv := newFuncServer(t)

	body := `{"kind":"functionapp","location":"westus2","properties":{"siteConfig":{"linuxFxVersion":"Python|3.11"}}}`
	doRequest(t, srv, http.MethodPut, sitesURL("loc")+apiVer, body)

	var site struct {
		Location   string `json:"location"`
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
		} `json:"properties"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodGet, sitesURL("loc")+apiVer, ""), &site)

	if site.Location != "westus2" {
		t.Fatalf("GET location = %q, want westus2", site.Location)
	}

	if site.Properties.ProvisioningState != "Succeeded" {
		t.Fatalf("provisioningState = %q, want Succeeded", site.Properties.ProvisioningState)
	}
}

// TestListApplicationSettingsAndConfigWeb covers finding 4.
func TestListApplicationSettingsAndConfigWeb(t *testing.T) {
	srv := newFuncServer(t)

	body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{"linuxFxVersion":"Node|20","appSettings":[{"name":"FOO","value":"bar"}]}}}`
	doRequest(t, srv, http.MethodPut, sitesURL("cfg")+apiVer, body)

	var dict struct {
		Type       string            `json:"type"`
		Properties map[string]string `json:"properties"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodPost, sitesURL("cfg")+"/config/appsettings/list"+apiVer, ""), &dict)

	if dict.Type != "Microsoft.Web/sites/config" {
		t.Fatalf("appsettings type = %q", dict.Type)
	}

	if dict.Properties["FOO"] != "bar" {
		t.Fatalf("appsettings FOO = %q, want bar", dict.Properties["FOO"])
	}

	var cfg struct {
		Type       string `json:"type"`
		Properties struct {
			LinuxFxVersion string `json:"linuxFxVersion"`
		} `json:"properties"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodGet, sitesURL("cfg")+"/config/web"+apiVer, ""), &cfg)

	if cfg.Properties.LinuxFxVersion != "Node|20" {
		t.Fatalf("config/web linuxFxVersion = %q, want Node|20", cfg.Properties.LinuxFxVersion)
	}
}

// TestListHostKeys covers finding 5 (host keys).
func TestListHostKeys(t *testing.T) {
	srv := newFuncServer(t)

	doRequest(t, srv, http.MethodPut, sitesURL("keys")+apiVer,
		`{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`)

	var hk struct {
		MasterKey    string            `json:"masterKey"`
		FunctionKeys map[string]string `json:"functionKeys"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodPost, sitesURL("keys")+"/host/default/listkeys"+apiVer, ""), &hk)

	if hk.MasterKey == "" {
		t.Fatalf("masterKey is empty")
	}

	if hk.FunctionKeys["default"] == "" {
		t.Fatalf("host default function key is empty: %+v", hk.FunctionKeys)
	}
}

// TestFunctionsCRUD covers finding 3 (list/get/404) and finding 5 (function keys).
func TestFunctionsCRUD(t *testing.T) {
	srv := newFuncServer(t)

	doRequest(t, srv, http.MethodPut, sitesURL("fns")+apiVer,
		`{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`)

	// No functions deployed yet → empty collection, not the site body.
	var empty struct {
		Value []json.RawMessage `json:"value"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodGet, sitesURL("fns")+"/functions"+apiVer, ""), &empty)

	if len(empty.Value) != 0 {
		t.Fatalf("expected 0 functions, got %d", len(empty.Value))
	}

	// A non-existent function → 404 (not a bogus 200).
	if r := doRequest(t, srv, http.MethodGet, sitesURL("fns")+"/functions/ghost"+apiVer, ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing function = %d, want 404", r.StatusCode)
	}

	// Create a function → 201.
	created := doRequest(t, srv, http.MethodPut, sitesURL("fns")+"/functions/httpTrigger"+apiVer,
		`{"properties":{"language":"python","config":{"bindings":[]}}}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create function = %d, want 201", created.StatusCode)
	}

	var env struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Properties struct {
			Name string `json:"name"`
		} `json:"properties"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodGet, sitesURL("fns")+"/functions/httpTrigger"+apiVer, ""), &env)

	if env.Name != "httpTrigger" || env.Type != "Microsoft.Web/sites/functions" {
		t.Fatalf("function envelope = %+v", env)
	}

	// List now returns the one function.
	decodeInto(t, doRequest(t, srv, http.MethodGet, sitesURL("fns")+"/functions"+apiVer, ""), &empty)
	if len(empty.Value) != 1 {
		t.Fatalf("expected 1 function after create, got %d", len(empty.Value))
	}

	// Function keys → 200 with a default key.
	var fk struct {
		Properties map[string]string `json:"properties"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodPost,
		sitesURL("fns")+"/functions/httpTrigger/listkeys"+apiVer, ""), &fk)

	if fk.Properties["default"] == "" {
		t.Fatalf("function default key is empty: %+v", fk.Properties)
	}
}

// TestRestart covers finding 11.
func TestRestart(t *testing.T) {
	srv := newFuncServer(t)

	doRequest(t, srv, http.MethodPut, sitesURL("rst")+apiVer,
		`{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`)

	if r := doRequest(t, srv, http.MethodPost, sitesURL("rst")+"/restart"+apiVer, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("restart = %d, want 200", r.StatusCode)
	}

	if r := doRequest(t, srv, http.MethodPost, sitesURL("missing")+"/restart"+apiVer, ""); r.StatusCode != http.StatusNotFound {
		t.Fatalf("restart missing = %d, want 404", r.StatusCode)
	}
}

// TestListFiltersByResourceGroup covers findings 9 and 10.
func TestListFiltersByResourceGroup(t *testing.T) {
	srv := newFuncServer(t)

	putSite := func(rg, name string) {
		doRequest(t, srv, http.MethodPut, siteInRG(rg, name)+apiVer,
			`{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`)
	}
	putSite("rgA", "a1")
	putSite("rgA", "a2")
	putSite("rgB", "b1")

	// RG-scoped list returns only rgA's sites.
	var rgList struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	rgURL := "/subscriptions/" + subID + "/resourceGroups/rgA/providers/Microsoft.Web/sites"
	decodeInto(t, doRequest(t, srv, http.MethodGet, rgURL+apiVer, ""), &rgList)

	if len(rgList.Value) != 2 {
		t.Fatalf("rgA list = %d entries, want 2", len(rgList.Value))
	}

	for _, e := range rgList.Value {
		if !strings.Contains(e.ID, "/resourceGroups/rgA/") {
			t.Fatalf("rgA list leaked a foreign id: %s", e.ID)
		}
	}

	// Subscription-wide list preserves each site's true RG (no empty segment).
	subURL := "/subscriptions/" + subID + "/providers/Microsoft.Web/sites"
	decodeInto(t, doRequest(t, srv, http.MethodGet, subURL+apiVer, ""), &rgList)

	if len(rgList.Value) != 3 {
		t.Fatalf("sub-wide list = %d entries, want 3", len(rgList.Value))
	}

	for _, e := range rgList.Value {
		if strings.Contains(e.ID, "resourceGroups//") {
			t.Fatalf("sub-wide id has empty resourceGroups segment: %s", e.ID)
		}
	}
}

// TestPlainGetDoesNotEchoSecrets covers finding 13.
func TestPlainGetDoesNotEchoSecrets(t *testing.T) {
	srv := newFuncServer(t)

	body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{"appSettings":[{"name":"SECRET","value":"topsecret"}]}}}`
	doRequest(t, srv, http.MethodPut, sitesURL("sec")+apiVer, body)

	raw := readBody(t, doRequest(t, srv, http.MethodGet, sitesURL("sec")+apiVer, ""))
	if strings.Contains(raw, "topsecret") {
		t.Fatalf("secret app-setting value leaked into plain GET: %s", raw)
	}
}

// TestPlanLocationAndCollection covers findings 6 and 14.
func TestPlanLocationAndCollection(t *testing.T) {
	srv := newFuncServer(t)

	planURL := "/subscriptions/" + subID + "/resourceGroups/" + rgName + "/providers/Microsoft.Web/serverfarms/plan1"
	doRequest(t, srv, http.MethodPut, planURL+apiVer,
		`{"location":"westeurope","sku":{"name":"EP1","tier":"ElasticPremium"}}`)

	var plan struct {
		Location string `json:"location"`
	}
	decodeInto(t, doRequest(t, srv, http.MethodGet, planURL+apiVer, ""), &plan)

	if plan.Location != "westeurope" {
		t.Fatalf("plan location = %q, want westeurope", plan.Location)
	}

	collURL := "/subscriptions/" + subID + "/resourceGroups/" + rgName + "/providers/Microsoft.Web/serverfarms"

	var coll struct {
		Value []json.RawMessage `json:"value"`
	}
	resp := doRequest(t, srv, http.MethodGet, collURL+apiVer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("serverfarms collection = %d, want 200", resp.StatusCode)
	}

	decodeInto(t, resp, &coll)
	if len(coll.Value) != 1 {
		t.Fatalf("serverfarms collection = %d entries, want 1", len(coll.Value))
	}
}

func decodeInto(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
