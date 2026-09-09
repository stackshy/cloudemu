package applicationgateway

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/applicationgateway/driver"
)

func newTestMock() *Mock {
	clk := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(config.WithClock(clk), config.WithAccountID("sub"), config.WithRegion("eastus"))

	return New(opts)
}

func sampleGateway() driver.AzureAppGateway {
	return driver.AzureAppGateway{
		Location:    "eastus",
		Zones:       []string{"1", "2"},
		SKUName:     "Standard_v2",
		SKUTier:     "Standard_v2",
		SKUCapacity: 2,
		Identity:    map[string]any{"type": "SystemAssigned"},
		Tags:        map[string]string{"env": "test"},
		Collections: map[string][]driver.AzureAppGatewayChild{
			"backendAddressPools": {{Name: "pool1", Properties: map[string]any{"backendAddresses": []any{}}}},
		},
		OtherProps: map[string]any{"enableHttp2": false},
	}
}

func TestCreateReportsCreatedThenUpdate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	stored, created, err := m.CreateOrUpdateAzureApplicationGateway(ctx, "rg", "gw", sampleGateway())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !created {
		t.Fatalf("first create reported created=false, want true")
	}

	if stored.Name != "gw" || stored.ResourceGroup != "rg" {
		t.Fatalf("stored identity = %q/%q, want rg/gw", stored.ResourceGroup, stored.Name)
	}

	_, created2, err := m.CreateOrUpdateAzureApplicationGateway(ctx, "rg", "gw", sampleGateway())
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if created2 {
		t.Fatalf("second create reported created=true, want false (update)")
	}
}

func TestCreateRequiresName(t *testing.T) {
	_, _, err := newTestMock().CreateOrUpdateAzureApplicationGateway(context.Background(), "rg", "", sampleGateway())
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("empty name err = %v, want InvalidArgument", err)
	}
}

func TestGetAndDelete(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.GetAzureApplicationGateway(ctx, "rg", "missing"); !cerrors.IsNotFound(err) {
		t.Fatalf("get missing err = %v, want NotFound", err)
	}

	_, _, _ = m.CreateOrUpdateAzureApplicationGateway(ctx, "rg", "gw", sampleGateway())

	// Case-insensitive addressing.
	got, err := m.GetAzureApplicationGateway(ctx, "RG", "GW")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.SKUTier != "Standard_v2" || len(got.Zones) != 2 {
		t.Fatalf("got = %+v, want tier Standard_v2 and 2 zones", got)
	}

	if err := m.DeleteAzureApplicationGateway(ctx, "rg", "gw"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := m.DeleteAzureApplicationGateway(ctx, "rg", "gw"); !cerrors.IsNotFound(err) {
		t.Fatalf("delete again err = %v, want NotFound", err)
	}
}

func TestListScopes(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _, _ = m.CreateOrUpdateAzureApplicationGateway(ctx, "rg-a", "gw1", sampleGateway())
	_, _, _ = m.CreateOrUpdateAzureApplicationGateway(ctx, "rg-b", "gw2", sampleGateway())

	rgScoped, err := m.ListAzureApplicationGateways(ctx, "rg-a")
	if err != nil {
		t.Fatalf("list rg: %v", err)
	}

	if len(rgScoped) != 1 || rgScoped[0].Name != "gw1" {
		t.Fatalf("rg-a list = %+v, want [gw1]", rgScoped)
	}

	all, err := m.ListAzureApplicationGateways(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}

	if len(all) != 2 {
		t.Fatalf("subscription list = %d, want 2", len(all))
	}
}

// TestCloneIsolation proves the store never aliases a caller's nested maps.
func TestCloneIsolation(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	in := sampleGateway()
	_, _, _ = m.CreateOrUpdateAzureApplicationGateway(ctx, "rg", "gw", in)

	// Mutate the caller's nested structures after storing.
	in.Collections["backendAddressPools"][0].Properties["mutated"] = true
	in.OtherProps["enableHttp2"] = true

	got, err := m.GetAzureApplicationGateway(ctx, "rg", "gw")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if _, leaked := got.Collections["backendAddressPools"][0].Properties["mutated"]; leaked {
		t.Fatalf("caller mutation leaked into stored child properties")
	}

	if got.OtherProps["enableHttp2"] != false {
		t.Fatalf("caller mutation leaked into OtherProps: %v", got.OtherProps["enableHttp2"])
	}
}

func TestSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, _, _ = m.CreateOrUpdateAzureApplicationGateway(ctx, "rg", "gw", sampleGateway())

	snap, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	restored := newTestMock()
	if err := restored.Restore(ctx, snap); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := restored.GetAzureApplicationGateway(ctx, "rg", "gw")
	if err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	if got.SKUCapacity != 2 || got.Collections["backendAddressPools"][0].Name != "pool1" {
		t.Fatalf("restored gateway = %+v, want capacity 2 and pool1", got)
	}
}
