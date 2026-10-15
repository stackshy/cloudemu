package bastion_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

// TestBastionRejectsWrongSubnetName proves a Bastion host whose ipConfiguration
// subnet is not named AzureBastionSubnet is refused with a 400.
func TestBastionRejectsWrongSubnetName(t *testing.T) {
	ts := newServer(t)
	c := bastionClient(t, ts)

	body := sampleHost()
	body.Properties.IPConfigurations[0].Properties.Subnet.ID = to.Ptr(
		"/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.Network/virtualNetworks/vnet/subnets/default")

	_, err := c.BeginCreateOrUpdate(context.Background(), testRG, hostName, body, nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("want ResponseError, got %v", err)
	}

	if respErr.StatusCode != 400 || respErr.ErrorCode != "InvalidParameter" {
		t.Errorf("got %d %s, want 400 InvalidParameter", respErr.StatusCode, respErr.ErrorCode)
	}

	if !strings.Contains(respErr.Error(), "AzureBastionSubnet") {
		t.Errorf("message should mention AzureBastionSubnet: %v", respErr)
	}
}
