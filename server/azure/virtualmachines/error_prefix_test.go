package virtualmachines_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	azurecompute "github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	azurevnet "github.com/stackshy/cloudemu/v2/providers/azure/vnet"
	"github.com/stackshy/cloudemu/v2/server/azure/virtualmachines"
)

// TestCreateVMMissingNICMessageHasNoInternalPrefix guards the NetworkInterfaceNotFound
// error path in instances.go: creating a VM whose networkProfile references a NIC that
// does not exist must surface a clean message, not the internal "InvalidArgument: "
// taxonomy prefix that err.Error() on a *cerrors.Error carries.
func TestCreateVMMissingNICMessageHasNoInternalPrefix(t *testing.T) {
	opts := config.NewOptions(config.WithRegion("eastus"))
	compute := azurecompute.New(opts)
	net := azurevnet.New(opts)

	h := virtualmachines.New(compute, net)

	ts := httptest.NewServer(h)
	defer ts.Close()

	url := ts.URL + "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Compute/virtualMachines/vm1"

	body := `{
		"location": "eastus",
		"properties": {
			"hardwareProfile": {"vmSize": "Standard_D2s_v3"},
			"storageProfile": {
				"imageReference": {"publisher": "Canonical", "offer": "UbuntuServer", "sku": "22.04-LTS", "version": "latest"}
			},
			"osProfile": {"computerName": "vm1", "adminUsername": "azureuser"},
			"networkProfile": {
				"networkInterfaces": [
					{"id": "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/networkInterfaces/missing-nic"}
				]
			}
		}
	}`

	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var respBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if respBody.Error.Code != "NetworkInterfaceNotFound" {
		t.Errorf("code=%q want NetworkInterfaceNotFound", respBody.Error.Code)
	}

	if strings.Contains(respBody.Error.Message, "InvalidArgument:") {
		t.Errorf("message=%q leaks internal error-code prefix", respBody.Error.Message)
	}
}
