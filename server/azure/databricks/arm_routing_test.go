package databricks_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
	dbxdriver "github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// TestARMRoutingCaseInsensitive proves that the Microsoft.Databricks ARM handler
// routes on the provider namespace and resource-type segments case-insensitively,
// matching ARM's own semantics. The armdatabricks SDK always emits canonical
// casing, so these assertions issue RAW HTTP requests to bypass it.
func TestARMRoutingCaseInsensitive(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{Databricks: cloudP.Databricks})

	ts := httptest.NewTLSServer(srv)
	defer ts.Close()

	const (
		routeSub = "sub-1"
		routeRG  = "rg-1"
		routeAC  = "ac1"
	)

	if _, err := cloudP.Databricks.CreateOrUpdateAccessConnector(context.Background(), dbxdriver.AccessConnectorConfig{
		Name:          routeAC,
		ResourceGroup: routeRG,
		Location:      "eastus",
	}); err != nil {
		t.Fatalf("seed access connector: %v", err)
	}

	base := ts.URL + "/subscriptions/" + routeSub + "/resourceGroups/" + routeRG + "/providers/"

	tests := []struct {
		name string
		path string
		want int
	}{
		{
			// Lowercased provider AND resource-type segments. Before the fix this
			// 404'd because matching was case-sensitive; ARM treats both segments
			// case-insensitively, so this must resolve to the seeded connector.
			name: "lowercased provider and resource type",
			path: base + "microsoft.databricks/accessconnectors/" + routeAC + "?api-version=2023-02-01",
			want: http.StatusOK,
		},
		{
			// Canonical casing (what the SDK emits) must keep behaving exactly as
			// before the fix.
			name: "canonical casing",
			path: base + "Microsoft.Databricks/accessConnectors/" + routeAC + "?api-version=2023-02-01",
			want: http.StatusOK,
		},
		{
			// A genuinely unknown provider matches no handler, so the server's
			// dispatcher rejects it (501 Not Implemented) rather than routing it
			// into the Databricks handler.
			name: "unknown provider is not routed to databricks",
			path: base + "Microsoft.NotDatabricks/accessConnectors/" + routeAC + "?api-version=2023-02-01",
			want: http.StatusNotImplemented,
		},
		{
			// A known provider + workspaces resource but an unrecognized
			// sub-resource must still hit the handler's default 404 branch, which
			// the case-insensitive refactor must leave intact.
			name: "unknown workspace sub-resource 404s",
			path: base + "Microsoft.Databricks/workspaces/ws-1/bogusSubResource?api-version=2023-02-01",
			want: http.StatusNotFound,
		},
	}

	client := ts.Client()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.want {
				t.Fatalf("GET %s: status = %d, want %d", tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}
