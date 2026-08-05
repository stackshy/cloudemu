package sql_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// statusOf extracts the HTTP status from a typed ARM SDK error.
func statusOf(t *testing.T, err error) int {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected an azcore.ResponseError, got %T: %v", err, err)
	}

	return re.StatusCode
}

// The provider-level behavior is tested elsewhere; this verifies the ARM wire
// layer maps canonical errors to the right HTTP status (GCP has the equivalent
// TestSDKCloudSQLErrorPaths).
func TestSDKAzureSQLWireErrorMapping(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()

	// 404 — server that doesn't exist.
	if _, err := cf.NewServersClient().Get(ctx, "rg-1", "ghost", nil); err == nil {
		t.Error("Get missing server: expected error")
	} else if got := statusOf(t, err); got != http.StatusNotFound {
		t.Errorf("Get missing server: status %d, want 404", got)
	}

	// 404 — database on a server that doesn't exist.
	if _, err := cf.NewDatabasesClient().Get(ctx, "rg-1", "ghost", "db", nil); err == nil {
		t.Error("Get database on missing server: expected error")
	} else if got := statusOf(t, err); got != http.StatusNotFound {
		t.Errorf("Get database on missing server: status %d, want 404", got)
	}

	mustCreateSQLServer(t, cf)

	// 400 — firewall rule with start > end.
	fw := cf.NewFirewallRulesClient()

	_, err := fw.CreateOrUpdate(ctx, "rg-1", "srv1", "bad", armsql.FirewallRule{
		Properties: &armsql.ServerFirewallRuleProperties{
			StartIPAddress: to.Ptr("10.0.0.9"),
			EndIPAddress:   to.Ptr("10.0.0.1"),
		},
	}, nil)
	if err == nil {
		t.Error("firewall rule with start > end: expected error")
	} else if got := statusOf(t, err); got != http.StatusBadRequest {
		t.Errorf("firewall start > end: status %d, want 400", got)
	}
}
