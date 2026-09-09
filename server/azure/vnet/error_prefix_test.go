package vnet_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	azurevnet "github.com/stackshy/cloudemu/v2/providers/azure/vnet"
	"github.com/stackshy/cloudemu/v2/server/azure/vnet"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// TestDeleteInUseNICMessageHasNoInternalPrefix guards the
// InUseNetworkInterfaceCannotBeDeleted error path in network_interface.go:
// deleting a NIC still attached to a VM must surface a clean message, not the
// internal "FailedPrecondition: " taxonomy prefix that err.Error() on a
// *cerrors.Error carries.
func TestDeleteInUseNICMessageHasNoInternalPrefix(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("eastus"))
	mock := azurevnet.New(opts)

	ctx := context.Background()

	if _, err := mock.CreateOrUpdateNetworkInterface(ctx, "rg-1", "nic-1", netdriver.AzureNICConfig{
		Location:  "eastus",
		IPConfigs: []netdriver.AzureIPConfig{{Name: "ipconfig1", Primary: true}},
	}); err != nil {
		t.Fatalf("create nic: %v", err)
	}

	if err := mock.AttachNetworkInterface(ctx, "rg-1", "nic-1",
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm-1"); err != nil {
		t.Fatalf("attach nic: %v", err)
	}

	h := vnet.New(mock)

	ts := httptest.NewServer(h)
	defer ts.Close()

	url := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/nic-1"

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Error.Code != "InUseNetworkInterfaceCannotBeDeleted" {
		t.Errorf("code=%q want InUseNetworkInterfaceCannotBeDeleted", body.Error.Code)
	}

	if strings.Contains(body.Error.Message, "FailedPrecondition:") {
		t.Errorf("message=%q leaks internal error-code prefix", body.Error.Message)
	}
}
