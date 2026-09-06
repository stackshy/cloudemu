package applicationgateway_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const (
	testRG   = "rg-1"
	testSub  = "sub-1"
	gwName   = "appgw-1"
	provider = "Microsoft.Network"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// newClient stands up the full Azure wire server backed by a fresh in-memory
// provider and returns an armnetwork ApplicationGateways client pointed at it.
// The VNet handler is wired too so we prove the applicationGateways resource
// type is not shadowed by another Microsoft.Network handler.
func newClient(t *testing.T) *armnetwork.ApplicationGatewaysClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		AppGateway: cloudP.AppGateway,
		Network:    cloudP.VNet,
		LB:         cloudP.LB,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armnetwork.NewApplicationGatewaysClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewApplicationGatewaysClient: %v", err)
	}

	return client
}

func gwID() string {
	return "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/" + provider + "/applicationGateways/" + gwName
}

func childID(collection, name string) string {
	return gwID() + "/" + collection + "/" + name
}

// fullGateway builds a Standard_v2 gateway exercising all seven required nested
// collections with the standard cross-collection references.
func fullGateway(capacity int32) armnetwork.ApplicationGateway {
	return armnetwork.ApplicationGateway{
		Location: to.Ptr("eastus"),
		Zones:    []*string{to.Ptr("1"), to.Ptr("2"), to.Ptr("3")},
		Properties: &armnetwork.ApplicationGatewayPropertiesFormat{
			SKU: &armnetwork.ApplicationGatewaySKU{
				Name:     to.Ptr(armnetwork.ApplicationGatewaySKUNameStandardV2),
				Tier:     to.Ptr(armnetwork.ApplicationGatewayTierStandardV2),
				Capacity: to.Ptr(capacity),
			},
			EnableHTTP2: to.Ptr(false),
			GatewayIPConfigurations: []*armnetwork.ApplicationGatewayIPConfiguration{{
				Name: to.Ptr("gwip"),
				Properties: &armnetwork.ApplicationGatewayIPConfigurationPropertiesFormat{
					Subnet: &armnetwork.SubResource{ID: to.Ptr("/subscriptions/" + testSub +
						"/resourceGroups/" + testRG + "/providers/" + provider +
						"/virtualNetworks/vnet/subnets/appgw")},
				},
			}},
			FrontendIPConfigurations: []*armnetwork.ApplicationGatewayFrontendIPConfiguration{{
				Name: to.Ptr("feip"),
				Properties: &armnetwork.ApplicationGatewayFrontendIPConfigurationPropertiesFormat{
					PublicIPAddress: &armnetwork.SubResource{ID: to.Ptr("/subscriptions/" + testSub +
						"/resourceGroups/" + testRG + "/providers/" + provider + "/publicIPAddresses/pip")},
				},
			}},
			FrontendPorts: []*armnetwork.ApplicationGatewayFrontendPort{{
				Name:       to.Ptr("port80"),
				Properties: &armnetwork.ApplicationGatewayFrontendPortPropertiesFormat{Port: to.Ptr(int32(80))},
			}},
			BackendAddressPools: []*armnetwork.ApplicationGatewayBackendAddressPool{{
				Name: to.Ptr("pool1"),
				Properties: &armnetwork.ApplicationGatewayBackendAddressPoolPropertiesFormat{
					BackendAddresses: []*armnetwork.ApplicationGatewayBackendAddress{{IPAddress: to.Ptr("10.0.1.4")}},
				},
			}},
			BackendHTTPSettingsCollection: []*armnetwork.ApplicationGatewayBackendHTTPSettings{{
				Name: to.Ptr("bhs"),
				Properties: &armnetwork.ApplicationGatewayBackendHTTPSettingsPropertiesFormat{
					Port:                to.Ptr(int32(80)),
					Protocol:            to.Ptr(armnetwork.ApplicationGatewayProtocolHTTP),
					CookieBasedAffinity: to.Ptr(armnetwork.ApplicationGatewayCookieBasedAffinityDisabled),
				},
			}},
			HTTPListeners: []*armnetwork.ApplicationGatewayHTTPListener{{
				Name: to.Ptr("listener"),
				Properties: &armnetwork.ApplicationGatewayHTTPListenerPropertiesFormat{
					FrontendIPConfiguration: &armnetwork.SubResource{ID: to.Ptr(childID("frontendIPConfigurations", "feip"))},
					FrontendPort:            &armnetwork.SubResource{ID: to.Ptr(childID("frontendPorts", "port80"))},
					Protocol:                to.Ptr(armnetwork.ApplicationGatewayProtocolHTTP),
				},
			}},
			RequestRoutingRules: []*armnetwork.ApplicationGatewayRequestRoutingRule{{
				Name: to.Ptr("rule1"),
				Properties: &armnetwork.ApplicationGatewayRequestRoutingRulePropertiesFormat{
					RuleType:            to.Ptr(armnetwork.ApplicationGatewayRequestRoutingRuleTypeBasic),
					Priority:            to.Ptr(int32(100)),
					HTTPListener:        &armnetwork.SubResource{ID: to.Ptr(childID("httpListeners", "listener"))},
					BackendAddressPool:  &armnetwork.SubResource{ID: to.Ptr(childID("backendAddressPools", "pool1"))},
					BackendHTTPSettings: &armnetwork.SubResource{ID: to.Ptr(childID("backendHttpSettingsCollection", "bhs"))},
				},
			}},
		},
	}
}

func createGateway(t *testing.T, client *armnetwork.ApplicationGatewaysClient, body armnetwork.ApplicationGateway) armnetwork.ApplicationGateway {
	t.Helper()

	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, gwName, body, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return resp.ApplicationGateway
}
