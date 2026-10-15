package eventarc_test

import (
	"context"
	"net/http/httptest"
	"testing"

	eventarc "cloud.google.com/go/eventarc/apiv1"
	"cloud.google.com/go/eventarc/apiv1/eventarcpb"
	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
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

	// The trigger below routes to Cloud Run service "svc": DriversFrom wires
	// the CloudRun driver, so create the service the destination validation
	// resolves against.
	if _, err := cloud.CloudRun.CreateService(ctx, crdriver.ServiceConfig{Name: "svc", Location: "us-central1"}); err != nil {
		t.Fatalf("CloudRun.CreateService: %v", err)
	}

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

	// GLRO-05 / GAR-02: the delete mints its own operation (it used to reuse
	// and overwrite "op-<trigger>") and resolves to the deleted Trigger.
	del, err := client.DeleteTrigger(ctx, &eventarcpb.DeleteTriggerRequest{Name: trig.GetName()})
	if err != nil {
		t.Fatalf("DeleteTrigger: %v", err)
	}

	if del.Name() == op.Name() {
		t.Fatalf("create and delete share operation name %q", del.Name())
	}

	gone, err := del.Wait(ctx)
	if err != nil || gone.GetName() != trig.GetName() {
		t.Fatalf("delete Wait: trigger=%v err=%v, want the deleted trigger", gone, err)
	}

	if _, err := client.GetOperation(ctx, &longrunningpb.GetOperationRequest{Name: op.Name()}); err != nil {
		t.Fatalf("create operation lost after delete: %v", err)
	}
}
