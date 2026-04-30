package functions_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

const (
	subID  = "00000000-0000-0000-0000-000000000000"
	rgName = "test-rg"

	apiVer = "?api-version=2022-03-01"
)

func sitesURL(name string) string {
	return "/subscriptions/" + subID +
		"/resourceGroups/" + rgName +
		"/providers/Microsoft.Web/sites/" + name
}

func collectionURL() string {
	return "/subscriptions/" + subID +
		"/resourceGroups/" + rgName +
		"/providers/Microsoft.Web/sites"
}

func TestMatches_AcceptsArmAndApiPaths(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	cases := []struct {
		method, url string
		wantStatus  int
	}{
		{http.MethodGet, sitesURL("missing") + apiVer, http.StatusNotFound},
		{http.MethodGet, collectionURL() + apiVer, http.StatusOK},
	}

	for _, tc := range cases {
		req, _ := http.NewRequestWithContext(context.Background(), tc.method, srv.URL+tc.url, nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.url, err)
		}

		_ = resp.Body.Close()

		if resp.StatusCode != tc.wantStatus {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.url, resp.StatusCode, tc.wantStatus)
		}
	}
}

func TestPutGetDeleteSiteRoundTrip(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	body := `{
        "kind":"functionapp",
        "location":"westus",
        "tags":{"env":"test"},
        "properties":{
            "siteConfig":{
                "linuxFxVersion":"Python|3.10",
                "appSettings":[{"name":"K","value":"V"}]
            },
            "httpsOnly":true
        }
    }`

	put := doRequest(t, srv, http.MethodPut, sitesURL("hello")+apiVer, body)
	if put.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200; body=%s", put.StatusCode, readBody(t, put))
	}

	got := doRequest(t, srv, http.MethodGet, sitesURL("hello")+apiVer, "")
	if got.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", got.StatusCode)
	}

	var site siteShape

	if err := json.NewDecoder(got.Body).Decode(&site); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = got.Body.Close()

	if site.Name != "hello" {
		t.Fatalf("Name = %q, want hello", site.Name)
	}

	if site.Kind != "functionapp" {
		t.Fatalf("Kind = %q, want functionapp", site.Kind)
	}

	if site.Properties.SiteConfig.LinuxFxVersion != "Python|3.10" {
		t.Fatalf("LinuxFxVersion = %q", site.Properties.SiteConfig.LinuxFxVersion)
	}

	if !strings.Contains(site.ID, "/Microsoft.Web/sites/hello") {
		t.Fatalf("ID = %q, want path containing /Microsoft.Web/sites/hello", site.ID)
	}

	del := doRequest(t, srv, http.MethodDelete, sitesURL("hello")+apiVer, "")
	if del.StatusCode != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", del.StatusCode)
	}

	missing := doRequest(t, srv, http.MethodGet, sitesURL("hello")+apiVer, "")
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete GET = %d, want 404", missing.StatusCode)
	}
}

func TestList(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	for _, n := range []string{"a", "b", "c"} {
		body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`

		resp := doRequest(t, srv, http.MethodPut, sitesURL(n)+apiVer, body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT %s = %d", n, resp.StatusCode)
		}
	}

	resp := doRequest(t, srv, http.MethodGet, collectionURL()+apiVer, "")

	var got struct {
		Value []siteShape `json:"value"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = resp.Body.Close()

	if len(got.Value) != 3 {
		t.Fatalf("len(value) = %d, want 3", len(got.Value))
	}
}

func TestPutIsIdempotent(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{"linuxFxVersion":"Node|18"}}}`

	first := doRequest(t, srv, http.MethodPut, sitesURL("idem")+apiVer, body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first PUT = %d", first.StatusCode)
	}

	body2 := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{"linuxFxVersion":"Node|20"}}}`

	second := doRequest(t, srv, http.MethodPut, sitesURL("idem")+apiVer, body2)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("second PUT = %d, want 200 (idempotent)", second.StatusCode)
	}

	got := doRequest(t, srv, http.MethodGet, sitesURL("idem")+apiVer, "")

	var site siteShape
	if err := json.NewDecoder(got.Body).Decode(&site); err != nil {
		t.Fatalf("decode: %v", err)
	}

	_ = got.Body.Close()

	if site.Properties.SiteConfig.LinuxFxVersion != "Node|20" {
		t.Fatalf("LinuxFxVersion = %q, want Node|20 (after update)",
			site.Properties.SiteConfig.LinuxFxVersion)
	}
}

func TestInvokeRunsRegisteredHandler(t *testing.T) {
	cloud := cloudemu.NewAzure()
	srv := httptest.NewServer(azureserver.New(azureserver.Drivers{Functions: cloud.Functions}))
	t.Cleanup(srv.Close)

	body := `{"kind":"functionapp","location":"eastus","properties":{"siteConfig":{}}}`
	if r := doRequest(t, srv, http.MethodPut, sitesURL("echo")+apiVer, body); r.StatusCode != http.StatusOK {
		t.Fatalf("PUT echo = %d", r.StatusCode)
	}

	cloud.Functions.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return append([]byte(`{"got":`), append(payload, '}')...), nil
	})

	resp := doRequest(t, srv, http.MethodPost, "/api/echo", `"hello"`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("invoke status = %d, want 200", resp.StatusCode)
	}

	out := readBody(t, resp)
	if out != `{"got":"hello"}` {
		t.Fatalf("body = %q, want {\"got\":\"hello\"}", out)
	}
}

// helpers --------------------------------------------------------------------

type siteShape struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Kind       string             `json:"kind"`
	Properties sitePropertiesView `json:"properties"`
}

type sitePropertiesView struct {
	State           string         `json:"state"`
	HostNames       []string       `json:"hostNames"`
	DefaultHostName string         `json:"defaultHostName"`
	SiteConfig      siteConfigView `json:"siteConfig"`
}

type siteConfigView struct {
	LinuxFxVersion string `json:"linuxFxVersion"`
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}

	req, _ := http.NewRequestWithContext(context.Background(), method, srv.URL+path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return strings.TrimSpace(string(b))
}

