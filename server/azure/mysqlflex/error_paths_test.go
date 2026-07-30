package mysqlflex_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers"
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
func TestSDKMySQLFlexWireErrorMapping(t *testing.T) {
	cf := newFactory(t)
	ctx := context.Background()

	// 404 — server that doesn't exist.
	if _, err := cf.NewServersClient().Get(ctx, "rg-1", "ghost", nil); err == nil {
		t.Error("Get missing server: expected error")
	} else if got := statusOf(t, err); got != http.StatusNotFound {
		t.Errorf("Get missing server: status %d, want 404", got)
	}

	mustCreateServer(t, cf)

	// 400 — firewall rule with start > end.
	fw := cf.NewFirewallRulesClient()

	poller, err := fw.BeginCreateOrUpdate(ctx, "rg-1", "srv1", "bad", armmysqlflexibleservers.FirewallRule{
		Properties: &armmysqlflexibleservers.FirewallRuleProperties{
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

	// 404 — unknown server parameter.
	if _, err := cf.NewConfigurationsClient().Get(ctx, "rg-1", "srv1", "not_a_real_param", nil); err == nil {
		t.Error("Get unknown parameter: expected error")
	} else if got := statusOf(t, err); got != http.StatusNotFound {
		t.Errorf("Get unknown parameter: status %d, want 404", got)
	}
}
