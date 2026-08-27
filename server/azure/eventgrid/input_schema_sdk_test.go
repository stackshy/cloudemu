package eventgrid_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2"
)

// TestSDKTopicInputSchemaRoundTrips locks the HIGH fix: a topic created with
// a non-default InputSchema must echo that schema back, not the hardcoded
// EventGridSchema default.
func TestSDKTopicInputSchemaRoundTrips(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	poller, err := topics.BeginCreateOrUpdate(ctx, testRG, "custom-schema-topic", armeventgrid.Topic{
		Location: to.Ptr("eastus"),
		Properties: &armeventgrid.TopicProperties{
			InputSchema: to.Ptr(armeventgrid.InputSchemaCloudEventSchemaV10),
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.InputSchema == nil ||
		*created.Properties.InputSchema != armeventgrid.InputSchemaCloudEventSchemaV10 {
		t.Fatalf("create response inputSchema = %v, want CloudEventSchemaV1_0", created.Properties)
	}

	got, err := topics.Get(ctx, testRG, "custom-schema-topic", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Properties == nil || got.Properties.InputSchema == nil ||
		*got.Properties.InputSchema != armeventgrid.InputSchemaCloudEventSchemaV10 {
		t.Fatalf("Get inputSchema = %v, want CloudEventSchemaV1_0", got.Properties)
	}
}

// TestSDKTopicInputSchemaDefaultsToEventGridSchema covers the unset case: a
// topic created without an explicit InputSchema still gets the real default.
func TestSDKTopicInputSchemaDefaultsToEventGridSchema(t *testing.T) {
	cf, _ := newEGFactory(t)
	topics := cf.NewTopicsClient()
	ctx := context.Background()

	poller, err := topics.BeginCreateOrUpdate(ctx, testRG, "default-schema-topic", armeventgrid.Topic{
		Location: to.Ptr("eastus"),
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	if created.Properties == nil || created.Properties.InputSchema == nil ||
		*created.Properties.InputSchema != armeventgrid.InputSchemaEventGridSchema {
		t.Fatalf("inputSchema = %v, want EventGridSchema default", created.Properties)
	}
}
