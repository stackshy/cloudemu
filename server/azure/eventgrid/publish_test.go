package eventgrid_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"

	"github.com/stackshy/cloudemu/v2"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

const eventsBody = `[{"id":"e1","subject":"orders/1","eventType":"Order.Created",` +
	`"eventTime":"2024-01-02T03:04:05Z","data":{"total":42},"dataVersion":"1.0"}]`

func TestDataPlanePublish(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{EventGrid: cloudP.EventGrid})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	// Create the topic over ARM first.
	myCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: "https://login.microsoftonline.com/",
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {Endpoint: ts.URL, Audience: "https://management.azure.com"},
		},
	}
	opts := &arm.ClientOptions{ClientOptions: azcore.ClientOptions{
		Cloud: myCloud, Transport: ts.Client(), Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	cf, err := armeventgrid.NewClientFactory(testSub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("NewClientFactory: %v", err)
	}

	poller, err := cf.NewTopicsClient().BeginCreateOrUpdate(context.Background(), testRG, "orders",
		armeventgrid.Topic{Location: to.Ptr("eastus")}, nil)
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}

	if _, err := poller.PollUntilDone(context.Background(), nil); err != nil {
		t.Fatalf("create topic poll: %v", err)
	}

	// Publish to the topic's data-plane endpoint. The topic is taken from the
	// request Host, matching the advertised endpoint hostname.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/events?api-version=2018-01-01", bytes.NewBufferString(eventsBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Host = "orders.eastus-1.eventgrid.azure.net"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("aeg-sas-key", "ignored")

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status = %d, want 200", resp.StatusCode)
	}

	// The event must have reached the topic's history.
	history, err := cloudP.EventGrid.GetEventHistory(context.Background(), "orders", 0)
	if err != nil {
		t.Fatalf("GetEventHistory: %v", err)
	}

	if len(history) != 1 || history[0].DetailType != "Order.Created" {
		t.Fatalf("history = %+v, want 1 Order.Created event", history)
	}
}

func TestDataPlanePublishMissingTopic(t *testing.T) {
	cloudP := cloudemu.NewAzure()
	srv := azureserver.New(azureserver.Drivers{EventGrid: cloudP.EventGrid})

	ts := httptest.NewTLSServer(srv)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		ts.URL+"/api/events", bytes.NewBufferString(eventsBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Host = "ghost.eastus-1.eventgrid.azure.net"

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for missing topic", resp.StatusCode)
	}
}
