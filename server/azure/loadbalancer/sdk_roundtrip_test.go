package loadbalancer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	testRG  = "rg-1"
	testSub = "sub-1"
)

type fakeCred struct{}

func (fakeCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// ensureRG creates a resource group so tests can PUT resources into it. Real
// Azure requires the group to exist first (the emulator enforces this via a
// pre-dispatch gate), so tests must provision it before their resource ops.
func ensureRG(t *testing.T, ts *httptest.Server, sub, rg string) {
	t.Helper()

	ensureRGWith(t, ts.Client(), ts.URL, sub, rg)
}

// ensureRGWith is ensureRG for a caller that only has a raw *http.Client and
// base URL (e.g. a bundle of sub-resource clients sharing one server), rather
// than the *httptest.Server itself.
func ensureRGWith(t *testing.T, client *http.Client, baseURL, sub, rg string) {
	t.Helper()

	url := baseURL + "/subscriptions/" + sub + "/resourcegroups/" + rg + "?api-version=2021-04-01"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, url,
		strings.NewReader(`{"location":"eastus"}`))
	if err != nil {
		t.Fatalf("ensureRG new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("ensureRG PUT %s: %v", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("ensureRG %s: unexpected status %d", url, resp.StatusCode)
	}
}

// lbServer bundles the emulator's TLS wire server endpoint with the
// arm.ClientOptions every armnetwork client in a test shares, so a whole-LB
// client and any sub-resource client (backend pools, NAT rules, probes, ...)
// address the same in-memory driver instance. Endpoint/HTTPClient let a test
// issue a raw HTTP request for a method the real armnetwork SDK has no client
// call for (e.g. a standalone PUT on a Get/List-only sub-resource).
type lbServer struct {
	Endpoint   string
	HTTPClient *http.Client
	Opts       *arm.ClientOptions
}

func newLBServer(t *testing.T) lbServer {
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

	ensureRG(t, ts, testSub, testRG)

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: ts.URL,
				Audience: "https://management.azure.com",
			},
		},
	}

	return lbServer{
		Endpoint:   ts.URL,
		HTTPClient: ts.Client(),
		Opts: &arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Cloud:     myCloud,
				Transport: ts.Client(),
				Retry:     policy.RetryOptions{MaxRetries: -1},
			},
		},
	}
}

// newLBServerOpts is the arm.ClientOptions-only convenience wrapper for tests
// that don't need raw HTTP access.
func newLBServerOpts(t *testing.T) *arm.ClientOptions {
	t.Helper()

	return newLBServer(t).Opts
}

func newLBClient(t *testing.T) *armnetwork.LoadBalancersClient {
	t.Helper()

	client, err := armnetwork.NewLoadBalancersClient(testSub, fakeCred{}, newLBServerOpts(t))
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
