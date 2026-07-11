package loadbalancer_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"

	"github.com/stackshy/cloudemu"
	azureserver "github.com/stackshy/cloudemu/server/azure"
)

const (
	testRG  = "rg-1"
	testSub = "sub-1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func newLBClient(t *testing.T) *armnetwork.LoadBalancersClient {
	t.Helper()

	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{
		LB: cloudP.LB,
		// Wire the network handler too so we prove the loadBalancers resource
		// type isn't shadowed by the virtualNetworks / NSG handler on the same
		// Microsoft.Network provider.
		Network: cloudP.VNet,
	})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	client, err := armnetwork.NewLoadBalancersClient(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("armnetwork.NewLoadBalancersClient: %v", err)
	}

	return client
}

func TestSDKAzureLBLifecycle(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-1", armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Tags:     map[string]*string{"env": to.Ptr("test")},
		SKU:      &armnetwork.LoadBalancerSKU{Name: to.Ptr(armnetwork.LoadBalancerSKUNameStandard)},
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{Name: to.Ptr("pool-a")},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	if created.Name == nil || *created.Name != "lb-1" {
		t.Fatalf("created name = %v, want lb-1", created.Name)
	}

	if created.Properties == nil || len(created.Properties.BackendAddressPools) != 1 {
		t.Fatalf("backend pools = %+v, want 1", created.Properties)
	}

	if *created.Properties.BackendAddressPools[0].Name != "pool-a" {
		t.Fatalf("pool name = %v, want pool-a", created.Properties.BackendAddressPools[0].Name)
	}

	got, err := client.Get(ctx, testRG, "lb-1", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tags = %v, want env=test", got.Tags)
	}

	var names []string

	pager := client.NewListPager(testRG, nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			t.Fatalf("List: %v", perr)
		}

		for _, lb := range page.Value {
			names = append(names, *lb.Name)
		}
	}

	if len(names) != 1 || names[0] != "lb-1" {
		t.Fatalf("list = %v, want [lb-1]", names)
	}

	delPoller, err := client.BeginDelete(ctx, testRG, "lb-1", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	_, err = client.Get(ctx, testRG, "lb-1", nil)

	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) || respErr.StatusCode != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKAzureLBWithRule(t *testing.T) {
	client := newLBClient(t)
	ctx := context.Background()

	poolID := "/subscriptions/" + testSub + "/resourceGroups/" + testRG +
		"/providers/Microsoft.Network/loadBalancers/lb-web/backendAddressPools/web-pool"

	poller, err := client.BeginCreateOrUpdate(ctx, testRG, "lb-web", armnetwork.LoadBalancer{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			BackendAddressPools: []*armnetwork.BackendAddressPool{
				{Name: to.Ptr("web-pool")},
			},
			LoadBalancingRules: []*armnetwork.LoadBalancingRule{
				{
					Name: to.Ptr("http-rule"),
					Properties: &armnetwork.LoadBalancingRulePropertiesFormat{
						Protocol:           to.Ptr(armnetwork.TransportProtocolTCP),
						FrontendPort:       to.Ptr(int32(80)),
						BackendPort:        to.Ptr(int32(8080)),
						BackendAddressPool: &armnetwork.SubResource{ID: to.Ptr(poolID)},
					},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate PollUntilDone: %v", err)
	}

	if created.Properties == nil || len(created.Properties.LoadBalancingRules) != 1 {
		t.Fatalf("rules = %+v, want 1", created.Properties)
	}

	rule := created.Properties.LoadBalancingRules[0]
	if rule.Properties == nil || *rule.Properties.FrontendPort != 80 {
		t.Fatalf("rule frontend port = %+v, want 80", rule.Properties)
	}

	if rule.Properties.BackendAddressPool == nil {
		t.Fatal("rule missing backend address pool reference")
	}
}
