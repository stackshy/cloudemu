package databricks

import (
	"context"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/databricks/driver"
)

// TestCreateWorkspaceUsesRequestSubscription verifies the workspace id is built
// from the request subscription rather than the emulator's default account.
func TestCreateWorkspaceUsesRequestSubscription(t *testing.T) {
	m := newTestMock() // default account is "sub-1"

	cfg := validConfig()
	cfg.Subscription = "req-sub"

	ws, err := m.CreateWorkspace(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if ws.Subscription != "req-sub" {
		t.Errorf("subscription = %q, want req-sub", ws.Subscription)
	}

	if !strings.HasPrefix(ws.ID, "/subscriptions/req-sub/") {
		t.Errorf("id = %q, want prefix /subscriptions/req-sub/", ws.ID)
	}
}

// TestCreateWorkspaceFallsBackToAccount verifies that when no subscription is
// supplied (typed Go API callers), the id falls back to the default account.
func TestCreateWorkspaceFallsBackToAccount(t *testing.T) {
	m := newTestMock() // default account is "sub-1"

	ws, err := m.CreateWorkspace(context.Background(), validConfig())
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	if !strings.HasPrefix(ws.ID, "/subscriptions/sub-1/") {
		t.Errorf("id = %q, want prefix /subscriptions/sub-1/", ws.ID)
	}
}

// TestAccessConnectorUsesRequestSubscription verifies the access-connector id is
// built from the request subscription.
func TestAccessConnectorUsesRequestSubscription(t *testing.T) {
	m := newTestMock()

	ac, err := m.CreateOrUpdateAccessConnector(context.Background(), driver.AccessConnectorConfig{
		Name:          "conn-1",
		Subscription:  "req-sub",
		ResourceGroup: "rg-1",
		Location:      "eastus",
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessConnector: %v", err)
	}

	if !strings.HasPrefix(ac.ID, "/subscriptions/req-sub/") {
		t.Errorf("id = %q, want prefix /subscriptions/req-sub/", ac.ID)
	}
}

// TestWorkspaceChildInheritsSubscription verifies a workspace sub-resource id
// inherits the parent workspace's subscription.
func TestWorkspaceChildInheritsSubscription(t *testing.T) {
	m := newTestMock()

	cfg := validConfig()
	cfg.Subscription = "req-sub"

	if _, err := m.CreateWorkspace(context.Background(), cfg); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	pec, err := m.PutPrivateEndpointConnection(context.Background(), "rg-1", "ws-1", "pec-1", "Approved", "ok")
	if err != nil {
		t.Fatalf("PutPrivateEndpointConnection: %v", err)
	}

	if !strings.HasPrefix(pec.ID, "/subscriptions/req-sub/") {
		t.Errorf("child id = %q, want prefix /subscriptions/req-sub/", pec.ID)
	}
}
