package iothub_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/azure/iothub"
	iothubsrv "github.com/stackshy/cloudemu/v2/server/azure/iothub"
)

func TestMatches(t *testing.T) {
	h := iothubsrv.New(iothub.New(config.NewOptions()))

	match := httptest.NewRequest(http.MethodGet, hubBase+"hub1"+apiVer, nil)
	if !h.Matches(match) {
		t.Error("expected match for IotHubs path")
	}

	other := httptest.NewRequest(http.MethodGet,
		"/subscriptions/s/resourceGroups/r/providers/Microsoft.Storage/storageAccounts/x"+apiVer, nil)
	if h.Matches(other) {
		t.Error("unexpected match for non-IotHubs path")
	}

	bad := httptest.NewRequest(http.MethodGet, "/not/an/arm/path", nil)
	if h.Matches(bad) {
		t.Error("unexpected match for malformed path")
	}
}

func TestPurgeResourceGroupCascade(t *testing.T) {
	mock := iothub.New(config.NewOptions())
	h := iothubsrv.New(mock)

	srv := httptest.NewServer(h)
	defer srv.Close()

	createHub(t, srv)

	if err := h.PurgeResourceGroup(context.Background(), "sub1", "rg1"); err != nil {
		t.Fatalf("purge: %v", err)
	}

	status, _ := do(t, srv, http.MethodGet, hubBase+"hub1"+apiVer, "")
	if status != http.StatusNotFound {
		t.Errorf("hub survived purge: status = %d", status)
	}
}

func TestMethodNotAllowedBranches(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, hubBase + "hub1/listkeys" + apiVer},                                      // listkeys GET
		{http.MethodGet, hubBase + "hub1/IotHubKeys/iothubowner/listkeys" + apiVer},               // getKeysForKeyName GET
		{http.MethodPost, "/subscriptions/sub1/providers/Microsoft.Devices/IotHubs" + apiVer},     // list POST
		{http.MethodPatch, hubBase + "hub1/eventHubEndpoints/events/ConsumerGroups/cgx" + apiVer}, // cg PATCH
		{http.MethodPost, hubBase + "hub1/eventHubEndpoints/events/ConsumerGroups" + apiVer},      // cg list POST
	}

	for _, c := range cases {
		status, _ := do(t, srv, c.method, c.path, "")
		if status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", c.method, c.path, status)
		}
	}
}

func TestListKeysMissingHub404(t *testing.T) {
	srv := newServer(t)

	status, _ := do(t, srv, http.MethodPost, hubBase+"ghost/listkeys"+apiVer, "")
	if status != http.StatusNotFound {
		t.Errorf("listkeys missing hub = %d, want 404", status)
	}

	status2, _ := do(t, srv, http.MethodPost, hubBase+"ghost/IotHubKeys/iothubowner/listkeys"+apiVer, "")
	if status2 != http.StatusNotFound {
		t.Errorf("getKeysForKeyName missing hub = %d, want 404", status2)
	}
}

func TestBadCreateBody(t *testing.T) {
	srv := newServer(t)

	status, _ := do(t, srv, http.MethodPut, hubBase+"hubx"+apiVer, `{not json`)
	if status != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", status)
	}
}

func TestPatchBadBody(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	status, _ := do(t, srv, http.MethodPatch, hubBase+"hub1"+apiVer, `{bad`)
	if status != http.StatusBadRequest {
		t.Errorf("patch bad body = %d, want 400", status)
	}
}

func TestGetConsumerGroupMissing404(t *testing.T) {
	srv := newServer(t)
	createHub(t, srv)

	cg := hubBase + "hub1/eventHubEndpoints/events/ConsumerGroups/ghost" + apiVer

	sGet, _ := do(t, srv, http.MethodGet, cg, "")
	if sGet != http.StatusNotFound {
		t.Errorf("get missing cg = %d, want 404", sGet)
	}

	sDel, _ := do(t, srv, http.MethodDelete, cg, "")
	if sDel != http.StatusNoContent {
		t.Errorf("delete missing cg = %d, want 204", sDel)
	}
}
