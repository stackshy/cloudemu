package postgresflex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// The 2025-08-01 Microsoft.DBforPostgreSQL API, which azurerm's go-azure-sdk
// pins, lists 202 as the only success code for every mutating Flexible Server
// call, and go-azure-sdk rejects any other code ("unexpected status 201"). Each
// call must answer 202 with an Azure-AsyncOperation URL that reports Succeeded.
func TestPostgresFlexMutationsReturnAcceptedWithAsyncOperation(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	ts := httptest.NewTLSServer(azureserver.New(azureserver.Drivers{PostgresFlex: cloudP.PostgresFlex}))
	t.Cleanup(ts.Close)
	ensureRG(t, ts, subID, "rg-1")

	const base = "/subscriptions/" + subID + "/resourceGroups/rg-1/providers/Microsoft.DBforPostgreSQL/flexibleServers/srv1"

	server := `{"location":"eastus","sku":{"name":"Standard_B1ms","tier":"Burstable"},"properties":{"version":"16"}}`

	steps := []struct {
		name, method, path, body string
	}{
		{"create server", http.MethodPut, base, server},
		{"put server again", http.MethodPut, base, server},
		{"patch server", http.MethodPatch, base, `{"tags":{"env":"dev"}}`},
		{"put database", http.MethodPut, base + "/databases/app", `{"properties":{"charset":"UTF8"}}`},
		{"put firewall rule", http.MethodPut, base + "/firewallRules/all",
			`{"properties":{"startIpAddress":"0.0.0.0","endIpAddress":"0.0.0.0"}}`},
		{"put configuration", http.MethodPut, base + "/configurations/max_connections", `{"properties":{"value":"100"}}`},
		{"restart", http.MethodPost, base + "/restart", ""},
		{"delete firewall rule", http.MethodDelete, base + "/firewallRules/all", ""},
		{"delete database", http.MethodDelete, base + "/databases/app", ""},
		{"delete server", http.MethodDelete, base, ""},
	}

	for _, st := range steps {
		resp := doReq(t, ts, st.method, ts.URL+st.path+"?api-version=2025-08-01", st.body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("%s: status %d, want 202", st.name, resp.StatusCode)
		}

		opURL := resp.Header.Get("Azure-AsyncOperation")
		if opURL == "" {
			t.Fatalf("%s: missing Azure-AsyncOperation header", st.name)
		}

		poll := doReq(t, ts, http.MethodGet, opURL, "")

		var got struct {
			Status string `json:"status"`
		}

		err := json.NewDecoder(poll.Body).Decode(&got)
		poll.Body.Close()

		if err != nil || poll.StatusCode != http.StatusOK || got.Status != "Succeeded" {
			t.Fatalf("%s: poll %s = %d %q (%v), want 200 Succeeded", st.name, opURL, poll.StatusCode, got.Status, err)
		}
	}
}

func doReq(t *testing.T, ts *httptest.Server, method, url, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}
