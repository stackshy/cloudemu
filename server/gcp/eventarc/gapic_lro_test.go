package eventarc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	eventarc "cloud.google.com/go/eventarc/apiv1"
	"cloud.google.com/go/eventarc/apiv1/eventarcpb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGAPICCreateTriggerWait is the review's #3 check for eventarc: the finding
// targeted the apiv1 GAPIC client's LRO .Wait(), which the raw REST client
// never exercised. CreateTrigger(...).Wait() must resolve (not 404, not a
// missing-@type decode error) and return the created trigger.
func TestGAPICCreateTriggerWait(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(cloud))
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	client, err := eventarc.NewRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	op, err := client.CreateTrigger(ctx, &eventarcpb.CreateTriggerRequest{
		Parent:    "projects/demo/locations/us-central1",
		TriggerId: "gapic-trig",
		Trigger: &eventarcpb.Trigger{
			EventFilters: []*eventarcpb.EventFilter{
				{Attribute: "type", Value: "google.cloud.pubsub.topic.v1.messagePublished"},
			},
			Destination: &eventarcpb.Destination{
				Descriptor_: &eventarcpb.Destination_CloudRun{
					CloudRun: &eventarcpb.CloudRun{Service: "svc", Region: "us-central1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	trig, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("op.Wait (the #3 GAPIC LRO fix): %v", err)
	}

	if trig == nil || trig.GetName() == "" {
		t.Fatalf("Wait returned no trigger: %+v", trig)
	}
}
