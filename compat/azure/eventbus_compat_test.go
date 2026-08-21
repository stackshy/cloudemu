package azure

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestCompatAzureEventBusEventGrid drives the real armeventgrid SDK against
// CloudEmu's in-process Event Grid wire server and records one compat result
// per portable eventbus operation the handler routes. Event Grid topics map
// onto the eventbus driver's event buses, so create/get/list/delete of a topic
// exercise CreateEventBus/GetEventBus/ListEventBuses/DeleteEventBus.
func TestCompatAzureEventBusEventGrid(t *testing.T) {
	const (
		service = "eventbus"
		testSub = "sub-1"
		testRG  = "rg-1"
		topic   = "orders-topic"
	)

	cloudP := cloudemu.NewAzure()
	sess := compat.BootAzureTLS(t, azureserver.Drivers{EventGrid: cloudP.EventGrid})

	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: sess.Endpoint(),
				Audience: "https://management.azure.com",
			},
		},
	}

	opts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud:     myCloud,
			Transport: sess.Transport(),
			Retry:     policy.RetryOptions{MaxRetries: -1},
		},
	}

	cf, err := armeventgrid.NewClientFactory(testSub, compat.FakeAzureCred(), opts)
	if err != nil {
		t.Fatalf("armeventgrid.NewClientFactory: %v", err)
	}

	topics := cf.NewTopicsClient()
	ctx := context.Background()

	sess.Op(service, "CreateEventBus", func() error {
		poller, cerr := topics.BeginCreateOrUpdate(ctx, testRG, topic, armeventgrid.Topic{
			Location: to.Ptr("global"),
			Tags:     map[string]*string{"env": to.Ptr("test")},
		}, nil)
		if cerr != nil {
			return cerr
		}

		_, cerr = poller.PollUntilDone(ctx, nil)

		return cerr
	})

	sess.Op(service, "GetEventBus", func() error {
		_, gerr := topics.Get(ctx, testRG, topic, nil)

		return gerr
	})

	sess.Op(service, "ListEventBuses", func() error {
		pager := topics.NewListByResourceGroupPager(testRG, nil)
		for pager.More() {
			if _, perr := pager.NextPage(ctx); perr != nil {
				return perr
			}
		}

		return nil
	})

	sess.Op(service, "DeleteEventBus", func() error {
		poller, derr := topics.BeginDelete(ctx, testRG, topic, nil)
		if derr != nil {
			return derr
		}

		_, derr = poller.PollUntilDone(ctx, nil)

		return derr
	})

	if _, gerr := topics.Get(ctx, testRG, topic, nil); gerr != nil {
		var respErr *azcore.ResponseError
		if !errors.As(gerr, &respErr) || respErr.StatusCode != 404 {
			t.Fatalf("Get after delete: got %v, want 404", gerr)
		}
	}
}
