package azure_test

import (
	"context"
	"encoding/json"
	"io"
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

// alCred is a static-token credential for the Activity Log test.
type alCred struct{}

func (alCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// TestActivityLogRecordsMutatingARMOp verifies a mutating ARM operation is
// auto-recorded and surfaced by the Activity Log read API — the Azure analogue
// of AWS CloudTrail LookupEvents reflecting a mutating call.
func TestActivityLogRecordsMutatingARMOp(t *testing.T) {
	p := cloudemu.NewAzure()
	srv := azureserver.NewFromProvider(p)

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()
	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
				},
			},
			Transport: ts.Client(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	const sub = "sub-al"

	vnetClient, err := armnetwork.NewVirtualNetworksClient(sub, alCred{}, opts)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	poller, err := vnetClient.BeginCreateOrUpdate(ctx, "rg-al", "vnet-al", armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("begin create: %v", err)
	}

	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create vnet: %v", err)
	}

	// Read the Activity Log via its management-events API (no auth needed — the
	// default server accepts any credentials).
	url := ts.URL + "/subscriptions/" + sub +
		"/providers/Microsoft.Insights/eventtypes/management/values?api-version=2015-04-01"

	resp, err := ts.Client().Get(url)
	if err != nil {
		t.Fatalf("get activity log: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("activity log status %d: %s", resp.StatusCode, body)
	}

	var got struct {
		Value []struct {
			OperationName struct {
				Value string `json:"value"`
			} `json:"operationName"`
			ResourceGroupName string `json:"resourceGroupName"`
		} `json:"value"`
	}
	if err = json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	found := false
	for _, e := range got.Value {
		if e.OperationName.Value == "Microsoft.Network/virtualNetworks/write" && e.ResourceGroupName == "rg-al" {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected recorded virtualNetworks/write event, got: %s", body)
	}
}

// TestActivityLogEmptyWithoutMutation verifies the Activity Log stays empty when
// no mutating operation has run — the default, no-noise path.
func TestActivityLogEmptyWithoutMutation(t *testing.T) {
	p := cloudemu.NewAzure()
	srv := azureserver.NewFromProvider(p)

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	url := ts.URL + "/subscriptions/sub-x" +
		"/providers/Microsoft.Insights/eventtypes/management/values?api-version=2015-04-01"

	resp, err := ts.Client().Get(url)
	if err != nil {
		t.Fatalf("get activity log: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}

	if !strings.Contains(string(body), `"value":[]`) {
		t.Fatalf("expected empty activity log, got: %s", body)
	}
}
