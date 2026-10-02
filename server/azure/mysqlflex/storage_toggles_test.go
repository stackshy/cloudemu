package mysqlflex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type togglesView struct {
	Properties struct {
		Storage struct {
			AutoGrow      string `json:"autoGrow"`
			AutoIoScaling string `json:"autoIoScaling"`
			LogOnDisk     string `json:"logOnDisk"`
		} `json:"storage"`
		Backup struct {
			GeoRedundantBackup string `json:"geoRedundantBackup"`
		} `json:"backup"`
	} `json:"properties"`
}

func (v *togglesView) flat() [4]string {
	p := v.Properties

	return [4]string{p.Storage.AutoGrow, p.Storage.AutoIoScaling, p.Storage.LogOnDisk, p.Backup.GeoRedundantBackup}
}

func sendJSON(t *testing.T, ts *httptest.Server, method, path, body string) togglesView {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method,
		ts.URL+path+"?api-version=2023-12-30", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("%s %s: status %d", method, path, resp.StatusCode)
	}

	var v togglesView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("%s %s: decode: %v", method, path, err)
	}

	return v
}

// terraform-provider-azurerm dereferences storage.autoGrow, autoIoScaling,
// logOnDisk and backup.geoRedundantBackup on every read of a MySQL Flexible
// Server, so all four must always be present: the service defaults when the
// request omitted them, the submitted values otherwise, kept across a PATCH
// that does not resend them.
func TestMySQLFlexStorageAndBackupToggles(t *testing.T) {
	const base = "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.DBforMySQL/flexibleServers/"

	tests := []struct {
		name      string
		create    string
		patch     string
		wantAfter [4]string
	}{
		{
			name:      "defaults when omitted",
			create:    `{"location":"eastus","properties":{"version":"8.0.21"}}`,
			wantAfter: [4]string{"Enabled", "Disabled", "Disabled", "Disabled"},
		},
		{
			name: "submitted values round-trip and survive a partial PATCH",
			create: `{"location":"eastus","properties":{` +
				`"storage":{"storageSizeGB":32,"autoGrow":"Disabled","autoIoScaling":"Enabled","logOnDisk":"Enabled"},` +
				`"backup":{"backupRetentionDays":7,"geoRedundantBackup":"Enabled"}}}`,
			patch:     `{"properties":{"storage":{"autoGrow":"Enabled"}}}`,
			wantAfter: [4]string{"Enabled", "Enabled", "Enabled", "Enabled"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ts := newFactory(t)
			path := base + "srv-toggles"

			sendJSON(t, ts, http.MethodPut, path, tc.create)

			if tc.patch != "" {
				sendJSON(t, ts, http.MethodPatch, path, tc.patch)
			}

			got := sendJSON(t, ts, http.MethodGet, path, "")
			if got.flat() != tc.wantAfter {
				t.Fatalf("autoGrow/autoIoScaling/logOnDisk/geoRedundantBackup = %v, want %v", got.flat(), tc.wantAfter)
			}
		})
	}
}
