package postgresflex_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers"
)

func statusOf(t *testing.T, err error) int {
	t.Helper()

	var re *azcore.ResponseError
	if !errors.As(err, &re) {
		t.Fatalf("expected an azcore.ResponseError, got %T: %v", err, err)
	}

	return re.StatusCode
}

// Verifies the ARM wire layer maps canonical errors to HTTP status (parity with
// GCP's TestSDKCloudSQLErrorPaths).
func TestSDKPostgresFlexWireErrorMapping(t *testing.T) {
	opts := newClientOpts(t)
	ctx := context.Background()

	servers, err := armpostgresqlflexibleservers.NewServersClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewServersClient: %v", err)
	}

	// 404 — server that doesn't exist.
	if _, err := servers.Get(ctx, "rg-1", "ghost", nil); err == nil {
		t.Error("Get missing server: expected error")
	} else if got := statusOf(t, err); got != http.StatusNotFound {
		t.Errorf("Get missing server: status %d, want 404", got)
	}

	mustCreateServer(t, opts)

	// 400 — firewall rule with start > end.
	fw, err := armpostgresqlflexibleservers.NewFirewallRulesClient(subID, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewFirewallRulesClient: %v", err)
	}

	poller, err := fw.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "bad", armpostgresqlflexibleservers.FirewallRule{
		Properties: &armpostgresqlflexibleservers.FirewallRuleProperties{
			StartIPAddress: to.Ptr("10.0.0.9"),
			EndIPAddress:   to.Ptr("10.0.0.1"),
		},
	}, nil)
	if err == nil {
		_, err = poller.PollUntilDone(ctx, nil)
	}

	if err == nil {
		t.Error("firewall rule with start > end: expected error")
	} else if got := statusOf(t, err); got != http.StatusBadRequest {
		t.Errorf("firewall start > end: status %d, want 400", got)
	}
}
