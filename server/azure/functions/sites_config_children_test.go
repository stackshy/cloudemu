package functions_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const webAPIVer = "?api-version=2023-12-01"

// newConfigServer starts a TLS server with one Linux site whose site PUT set
// http20Enabled, the way azurerm_linux_web_app creates it.
func newConfigServer(t *testing.T, site string) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{Functions: cloudP.Functions}))
	t.Cleanup(ts.Close)

	ensureRG(t, ts.Client(), ts.URL, subID, rgName)

	status, body := call(t, ts, http.MethodPut, sitesURL(site), `{"kind":"app,linux","location":"eastus",
		"properties":{"siteConfig":{"linuxFxVersion":"NODE|20-lts","http20Enabled":true,
		"appSettings":[{"name":"SECRET","value":"s"}]}}}`)
	if status != http.StatusOK {
		t.Fatalf("create site: %d %v", status, body)
	}

	return ts
}

// call sends one ARM request and decodes the JSON object response, if any.
func call(t *testing.T, ts *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path+webAPIVer, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)

	return resp.StatusCode, out
}

// lookup walks dotted keys through nested JSON objects.
func lookup(m map[string]any, path string) any {
	var cur any = m

	for _, k := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}

		cur = obj[k]
	}

	return cur
}

type configStep struct {
	name       string
	method     string
	path       string
	body       string
	wantStatus int
	// wantKey/wantVal assert one dotted key of the response when wantKey is set.
	wantKey string
	wantVal any
}

func runConfigSteps(t *testing.T, ts *httptest.Server, steps []configStep) {
	t.Helper()

	for _, s := range steps {
		status, body := call(t, ts, s.method, s.path, s.body)
		if status != s.wantStatus {
			t.Fatalf("%s: status %d, want %d (%v)", s.name, status, s.wantStatus, body)
		}

		if s.wantKey == "" {
			continue
		}

		if got := lookup(body, s.wantKey); got != s.wantVal {
			t.Fatalf("%s: %s = %v (%T), want %v", s.name, s.wantKey, got, got, s.wantVal)
		}
	}
}

// TestSiteConfigChildren drives the config/{name} documents azurerm's
// linux_web_app and linux_function_app create and read, using the casing
// azurerm sends (config/appSettings, config/connectionStrings).
func TestSiteConfigChildren(t *testing.T) {
	ts := newConfigServer(t, "cfg-app")
	cfg := sitesURL("cfg-app") + "/config/"

	runConfigSteps(t, ts, []configStep{
		{"appSettings mixed case PUT", http.MethodPut, cfg + "appSettings",
			`{"properties":{"FOO":"v1"}}`, http.StatusOK, "properties.FOO", "v1"},
		{"appsettings lowercase PUT", http.MethodPut, cfg + "appsettings",
			`{"properties":{"FOO":"v2"}}`, http.StatusOK, "properties.FOO", "v2"},
		{"appSettings LIST upper action", http.MethodPost, cfg + "appSettings/LIST", "",
			http.StatusOK, "properties.FOO", "v2"},

		{"web keeps site PUT siteConfig", http.MethodGet, cfg + "web", "", http.StatusOK,
			"properties.http20Enabled", true},
		{"web has defaults", http.MethodGet, cfg + "web", "", http.StatusOK,
			"properties.numberOfWorkers", float64(1)},
		{"web PATCH merges", http.MethodPatch, cfg + "web", `{"properties":{"webSocketsEnabled":true}}`,
			http.StatusOK, "properties.http20Enabled", true},
		{"web PATCH applied", http.MethodGet, cfg + "web", "", http.StatusOK,
			"properties.webSocketsEnabled", true},
		{"web PUT replaces", http.MethodPut, cfg + "web", `{"properties":{"minTlsVersion":"1.3"}}`,
			http.StatusOK, "properties.http20Enabled", false},
		{"web PUT sets modeled knob", http.MethodGet, cfg + "web", "", http.StatusOK,
			"properties.minTlsVersion", "1.3"},

		{"connectionStrings default", http.MethodPost, cfg + "connectionStrings/list", "",
			http.StatusOK, "properties.db", nil},
		{"connectionStrings PUT", http.MethodPut, cfg + "connectionStrings",
			`{"properties":{"db":{"value":"Server=x","type":"SQLAzure"}}}`, http.StatusOK, "properties.db.type", "SQLAzure"},
		{"connectionstrings list", http.MethodPost, cfg + "connectionstrings/list", "",
			http.StatusOK, "properties.db.value", "Server=x"},
		{"connectionStrings bad type", http.MethodPut, cfg + "connectionStrings",
			`{"properties":{"db":{"value":"x","type":"Bogus"}}}`, http.StatusBadRequest, "", nil},

		{"authsettings default v1", http.MethodPost, cfg + "authsettings/list", "",
			http.StatusOK, "properties.configVersion", "v1"},
		{"authsettingsV2 default", http.MethodGet, cfg + "authsettingsV2", "",
			http.StatusOK, "properties.platform.enabled", false},
		{"authsettingsV2 PUT", http.MethodPut, cfg + "authsettingsV2",
			`{"properties":{"platform":{"enabled":true}}}`, http.StatusOK, "properties.platform.enabled", true},
		{"authsettingsV2 list is a GET", http.MethodGet, cfg + "authsettingsV2/list", "",
			http.StatusOK, "properties.platform.enabled", true},
		{"authsettings now v2", http.MethodPost, cfg + "authsettings/list", "",
			http.StatusOK, "properties.configVersion", "v2"},

		{"logs default failedRequestsTracing", http.MethodGet, cfg + "logs", "",
			http.StatusOK, "properties.failedRequestsTracing.enabled", false},
		{"logs default detailedErrorMessages", http.MethodGet, cfg + "logs", "",
			http.StatusOK, "properties.detailedErrorMessages.enabled", false},
		{"logs PUT", http.MethodPut, cfg + "logs",
			`{"properties":{"httpLogs":{"fileSystem":{"enabled":true,"retentionInMb":35}}}}`,
			http.StatusOK, "properties.httpLogs.fileSystem.enabled", true},

		{"slotConfigNames default", http.MethodGet, cfg + "slotConfigNames", "", http.StatusOK,
			"properties.appSettingNames", nil},
		{"slotConfigNames PUT", http.MethodPut, cfg + "slotConfigNames",
			`{"properties":{"appSettingNames":["A"]}}`, http.StatusOK, "name", "slotconfignames"},

		{"backup unset 404", http.MethodPost, cfg + "backup/list", "", http.StatusNotFound, "", nil},
		{"backup PUT", http.MethodPut, cfg + "backup", `{"properties":{"enabled":true}}`,
			http.StatusOK, "properties.enabled", true},
		{"backup list", http.MethodPost, cfg + "backup/list", "", http.StatusOK, "properties.enabled", true},
		{"backup DELETE", http.MethodDelete, cfg + "backup", "", http.StatusOK, "", nil},
		{"backup gone", http.MethodPost, cfg + "backup/list", "", http.StatusNotFound, "", nil},

		{"unknown config", http.MethodGet, cfg + "zz", "", http.StatusNotFound, "error.code", "InvalidResourceType"},
		{"missing site", http.MethodPost, sitesURL("nope") + "/config/connectionStrings/list", "",
			http.StatusNotFound, "", nil},
	})
}

func TestSitePublishingCredentialsAndPolicies(t *testing.T) {
	ts := newConfigServer(t, "pub-app")
	site := sitesURL("pub-app")

	_, first := call(t, ts, http.MethodPost, site+"/config/publishingcredentials/list", "")
	_, second := call(t, ts, http.MethodPost, site+"/config/publishingcredentials/list", "")

	pwd := lookup(first, "properties.publishingPassword")
	if pwd == "" || pwd == nil || pwd != lookup(second, "properties.publishingPassword") {
		t.Fatalf("publishing password %v then %v, want one stable value", pwd, lookup(second, "properties.publishingPassword"))
	}

	if got := lookup(first, "properties.publishingUserName"); got != "$pub-app" {
		t.Fatalf("publishingUserName = %v", got)
	}

	pol := site + "/basicPublishingCredentialsPolicies/"

	runConfigSteps(t, ts, []configStep{
		{"ftp default allowed", http.MethodGet, pol + "ftp", "", http.StatusOK, "properties.allow", true},
		{"ftp disable", http.MethodPut, pol + "ftp", `{"properties":{"allow":false}}`,
			http.StatusOK, "properties.allow", false},
		{"ftp read back", http.MethodGet, pol + "ftp", "", http.StatusOK, "properties.allow", false},
		{"scm untouched", http.MethodGet, pol + "scm", "", http.StatusOK, "properties.allow", true},
		{"lowercase segment", http.MethodGet, site + "/basicpublishingcredentialspolicies/FTP", "",
			http.StatusOK, "properties.allow", false},
		{"unknown policy", http.MethodGet, pol + "x", "", http.StatusNotFound, "", nil},
		{"missing allow", http.MethodPut, pol + "scm", `{"properties":{}}`, http.StatusBadRequest, "", nil},
	})
}

// TestSiteDeleteDropsConfig confirms config documents live and die with the
// site: a delete then a same-name create starts from the defaults again.
func TestSiteDeleteDropsConfig(t *testing.T) {
	ts := newConfigServer(t, "del-app")
	site := sitesURL("del-app")

	runConfigSteps(t, ts, []configStep{
		{"set conn", http.MethodPut, site + "/config/connectionStrings",
			`{"properties":{"db":{"value":"x","type":"Custom"}}}`, http.StatusOK, "", nil},
		{"set ftp", http.MethodPut, site + "/basicPublishingCredentialsPolicies/ftp",
			`{"properties":{"allow":false}}`, http.StatusOK, "", nil},
		{"delete site", http.MethodDelete, site, "", http.StatusOK, "", nil},
		{"config 404", http.MethodPost, site + "/config/connectionStrings/list", "", http.StatusNotFound, "", nil},
		{"recreate", http.MethodPut, site, `{"kind":"app,linux","location":"eastus"}`, http.StatusOK, "", nil},
		{"conn reset", http.MethodPost, site + "/config/connectionStrings/list", "", http.StatusOK, "properties.db", nil},
		{"ftp reset", http.MethodGet, site + "/basicPublishingCredentialsPolicies/ftp", "",
			http.StatusOK, "properties.allow", true},
	})
}

func TestWebCheckNameAvailability(t *testing.T) {
	ts := newConfigServer(t, "taken-app")
	path := "/subscriptions/" + subID + "/providers/Microsoft.Web/checknameavailability"

	runConfigSteps(t, ts, []configStep{
		{"free name", http.MethodPost, path, `{"name":"free-app","type":"Microsoft.Web/sites"}`,
			http.StatusOK, "nameAvailable", true},
		{"taken name", http.MethodPost, path, `{"name":"TAKEN-app","type":"Site"}`,
			http.StatusOK, "nameAvailable", false},
		{"taken reason", http.MethodPost, path, `{"name":"taken-app","type":"Microsoft.Web/sites"}`,
			http.StatusOK, "reason", "AlreadyExists"},
		{"other type", http.MethodPost, path, `{"name":"taken-app","type":"Microsoft.Web/hostingEnvironments"}`,
			http.StatusOK, "nameAvailable", true},
		{"no name", http.MethodPost, path, `{"type":"Site"}`, http.StatusBadRequest, "", nil},
		{"GET rejected", http.MethodGet, path, "", http.StatusMethodNotAllowed, "", nil},
	})
}
