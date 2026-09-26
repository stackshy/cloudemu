package frontdoor_test

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/azure/frontdoor"
	"github.com/stackshy/cloudemu/v2/services/frontdoor/driver"
)

const (
	endpoint    = "ep-1"
	originGroup = "og-1"
	origin      = "origin-1"
	route       = "route-1"
)

// seedChain creates profile -> endpoint + origin group -> origin + route.
func seedChain(t *testing.T) (*frontdoor.Mock, context.Context) {
	t.Helper()

	m := newMock()
	ctx := context.Background()

	_, _, err := m.CreateOrUpdateProfile(ctx, rg, profile, driver.AzureFrontDoorProfile{SKUName: "Standard_AzureFrontDoor"})
	requireNoError(t, err, "create profile")

	_, _, err = m.CreateOrUpdateEndpoint(ctx, rg, profile, endpoint, driver.AzureFrontDoorEndpoint{})
	requireNoError(t, err, "create endpoint")

	_, _, err = m.CreateOrUpdateOriginGroup(ctx, rg, profile, originGroup, driver.AzureFrontDoorOriginGroup{})
	requireNoError(t, err, "create origin group")

	_, created, err := m.CreateOrUpdateOrigin(ctx, rg, profile, originGroup, origin, driver.AzureFrontDoorOrigin{
		Properties: map[string]any{"hostName": "app.example.net"},
	})
	requireNoError(t, err, "create origin")

	if !created {
		t.Fatal("first origin create should report created=true")
	}

	_, created, err = m.CreateOrUpdateRoute(ctx, rg, profile, endpoint, route, driver.AzureFrontDoorRoute{
		OriginGroup: originGroup,
		Properties:  map[string]any{"patternsToMatch": []any{"/*"}},
	})
	requireNoError(t, err, "create route")

	if !created {
		t.Fatal("first route create should report created=true")
	}

	return m, ctx
}

func TestOriginAndRouteCRUD(t *testing.T) {
	m, ctx := seedChain(t)

	o, err := m.GetOrigin(ctx, rg, profile, originGroup, origin)
	requireNoError(t, err, "get origin")

	if o.OriginGroup != originGroup || o.Properties["hostName"] != "app.example.net" {
		t.Errorf("origin = %+v", o)
	}

	// A re-put is a replace, not a create.
	_, created, err := m.CreateOrUpdateOrigin(ctx, rg, profile, originGroup, origin, driver.AzureFrontDoorOrigin{
		Properties: map[string]any{"hostName": "other.example.net"},
	})
	requireNoError(t, err, "replace origin")

	if created {
		t.Error("second origin put should report created=false")
	}

	origins, err := m.ListOrigins(ctx, rg, profile, originGroup)
	requireNoError(t, err, "list origins")

	routes, err := m.ListRoutes(ctx, rg, profile, endpoint)
	requireNoError(t, err, "list routes")

	if len(origins) != 1 || len(routes) != 1 {
		t.Fatalf("listed %d origins and %d routes, want 1 and 1", len(origins), len(routes))
	}

	requireNoError(t, m.DeleteRoute(ctx, rg, profile, endpoint, route), "delete route")
	requireNoError(t, m.DeleteOrigin(ctx, rg, profile, originGroup, origin), "delete origin")

	if err := m.DeleteOrigin(ctx, rg, profile, originGroup, origin); !cerrors.IsNotFound(err) {
		t.Errorf("second delete origin err = %v, want NotFound", err)
	}
}

func TestParentsMustExist(t *testing.T) {
	m, ctx := seedChain(t)

	_, _, err := m.CreateOrUpdateOrigin(ctx, rg, profile, "missing", origin, driver.AzureFrontDoorOrigin{})
	if !cerrors.IsNotFound(err) {
		t.Errorf("origin under missing group err = %v, want NotFound", err)
	}

	_, _, err = m.CreateOrUpdateRoute(ctx, rg, profile, "missing", route, driver.AzureFrontDoorRoute{OriginGroup: originGroup})
	if !cerrors.IsNotFound(err) {
		t.Errorf("route under missing endpoint err = %v, want NotFound", err)
	}

	_, _, err = m.CreateOrUpdateRoute(ctx, rg, profile, endpoint, "r2", driver.AzureFrontDoorRoute{OriginGroup: "missing"})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("route to missing origin group err = %v, want InvalidArgument", err)
	}

	_, _, err = m.CreateOrUpdateRoute(ctx, rg, profile, endpoint, "r3", driver.AzureFrontDoorRoute{})
	if !cerrors.IsInvalidArgument(err) {
		t.Errorf("route without origin group err = %v, want InvalidArgument", err)
	}
}

func TestOriginGroupInUseAndCascades(t *testing.T) {
	m, ctx := seedChain(t)

	if err := m.DeleteOriginGroup(ctx, rg, profile, originGroup); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("delete in-use origin group err = %v, want FailedPrecondition", err)
	}

	// Deleting the endpoint cascades to its route, which frees the group.
	requireNoError(t, m.DeleteEndpoint(ctx, rg, profile, endpoint), "delete endpoint")

	if _, err := m.GetRoute(ctx, rg, profile, endpoint, route); !cerrors.IsNotFound(err) {
		t.Errorf("route after endpoint delete err = %v, want NotFound", err)
	}

	// Deleting the origin group cascades to its origins.
	requireNoError(t, m.DeleteOriginGroup(ctx, rg, profile, originGroup), "delete origin group")

	if _, err := m.GetOrigin(ctx, rg, profile, originGroup, origin); !cerrors.IsNotFound(err) {
		t.Errorf("origin after group delete err = %v, want NotFound", err)
	}
}

func TestProfileDeleteCascadesGrandchildren(t *testing.T) {
	m, ctx := seedChain(t)

	requireNoError(t, m.DeleteProfile(ctx, rg, profile), "delete profile")

	if _, err := m.GetOrigin(ctx, rg, profile, originGroup, origin); !cerrors.IsNotFound(err) {
		t.Errorf("origin after profile delete err = %v, want NotFound", err)
	}

	if _, err := m.GetRoute(ctx, rg, profile, endpoint, route); !cerrors.IsNotFound(err) {
		t.Errorf("route after profile delete err = %v, want NotFound", err)
	}
}

func TestSnapshotRoundTripsOriginsAndRoutes(t *testing.T) {
	m, ctx := seedChain(t)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err, "snapshot")

	restored := newMock()
	requireNoError(t, restored.Restore(ctx, snap), "restore")

	if _, err := restored.GetOrigin(ctx, rg, profile, originGroup, origin); err != nil {
		t.Errorf("restored origin: %v", err)
	}

	r, err := restored.GetRoute(ctx, rg, profile, endpoint, route)
	requireNoError(t, err, "restored route")

	if r.OriginGroup != originGroup {
		t.Errorf("restored route origin group = %q, want %q", r.OriginGroup, originGroup)
	}

	// The restored reference is still enforced.
	if err := restored.DeleteOriginGroup(ctx, rg, profile, originGroup); !cerrors.IsFailedPrecondition(err) {
		t.Errorf("restored in-use delete err = %v, want FailedPrecondition", err)
	}
}
