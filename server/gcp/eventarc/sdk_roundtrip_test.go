package eventarc_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	eventarc "google.golang.org/api/eventarc/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const (
	testProject  = "demo"
	testLocation = "us-central1"
)

func newEventarcService(t *testing.T) *eventarc.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Eventarc: cloud.Eventarc})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := eventarc.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("eventarc.NewService: %v", err)
	}

	return svc
}

func parent() string {
	return "projects/" + testProject + "/locations/" + testLocation
}

func TestSDKEventarcTriggerLifecycle(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{
			{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"},
		},
		Destination: &eventarc.Destination{
			CloudRun: &eventarc.CloudRun{Service: "my-service", Region: testLocation},
		},
	}

	op, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("obj-created").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("Create operation not done: %+v", op)
	}

	got, err := svc.Projects.Locations.Triggers.Get(parent() + "/triggers/obj-created").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Get: %v", err)
	}

	if got.Name != parent()+"/triggers/obj-created" {
		t.Fatalf("trigger name = %q, want .../triggers/obj-created", got.Name)
	}

	if len(got.EventFilters) != 1 || got.EventFilters[0].Value != "google.cloud.storage.object.v1.finalized" {
		t.Fatalf("event filters = %+v, want one storage-finalized filter", got.EventFilters)
	}

	if got.Destination == nil || got.Destination.CloudRun == nil || got.Destination.CloudRun.Service != "my-service" {
		t.Fatalf("destination = %+v, want cloudRun service my-service", got.Destination)
	}

	list, err := svc.Projects.Locations.Triggers.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.List: %v", err)
	}

	if len(list.Triggers) != 1 || list.Triggers[0].Name != parent()+"/triggers/obj-created" {
		t.Fatalf("List = %+v, want one trigger obj-created", list.Triggers)
	}

	delOp, err := svc.Projects.Locations.Triggers.Delete(parent() + "/triggers/obj-created").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("Delete operation not done: %+v", delOp)
	}

	_, err = svc.Projects.Locations.Triggers.Get(parent() + "/triggers/obj-created").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKEventarcErrors(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	// Get on a location with no triggers yet is a 404.
	_, err := svc.Projects.Locations.Triggers.Get(parent() + "/triggers/missing").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}

	// List on an empty location returns an empty list, not an error.
	list, err := svc.Projects.Locations.Triggers.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(empty): %v", err)
	}

	if len(list.Triggers) != 0 {
		t.Fatalf("List(empty) = %+v, want no triggers", list.Triggers)
	}

	// Duplicate create is a conflict.
	tr := &eventarc.Trigger{
		Destination: &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "s"}},
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), tr).TriggerId("dup").Context(ctx).Do(); err != nil {
		t.Fatalf("Create(dup): %v", err)
	}

	_, err = svc.Projects.Locations.Triggers.Create(parent(), tr).TriggerId("dup").Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 409 {
		t.Fatalf("duplicate Create: got %v, want 409", err)
	}
}
