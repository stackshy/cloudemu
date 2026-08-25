package eventarc_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	eventarc "google.golang.org/api/eventarc/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
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

// TestSDKEventarcOperationAndMetadata guards two #321 fixes: the LRO operation
// endpoint resolves (Operations.Get), and serviceAccount + labels round-trip
// on the trigger instead of being dropped.
func TestSDKEventarcOperationAndMetadata(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{
			{Attribute: "type", Value: "google.cloud.pubsub.topic.v1.messagePublished"},
		},
		Destination:    &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "svc", Region: testLocation}},
		ServiceAccount: "runner@demo.iam.gserviceaccount.com",
		Labels:         map[string]string{"env": "prod"},
	}

	op, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("meta-trig").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Create: %v", err)
	}

	polled, err := svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Operations.Get (the #321 LRO route): %v", err)
	}

	if !polled.Done {
		t.Errorf("polled operation not done: %+v", polled)
	}

	got, err := svc.Projects.Locations.Triggers.Get(parent() + "/triggers/meta-trig").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Get: %v", err)
	}

	if got.ServiceAccount != "runner@demo.iam.gserviceaccount.com" {
		t.Errorf("serviceAccount=%q dropped", got.ServiceAccount)
	}

	if got.Labels["env"] != "prod" {
		t.Errorf("labels=%v want env=prod", got.Labels)
	}
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

// TestSDKEventarcMissingDestination: a trigger with no destination is a 400
// INVALID_ARGUMENT, not a silently-stored dead route.
func TestSDKEventarcMissingDestination(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
	}

	_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("no-dest").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("Create(no destination): got %v, want 400", err)
	}
}

// TestSDKEventarcServerFields: create assigns a uid, etag, and an
// auto-provisioned Pub/Sub transport topic that round-trip on Get.
func TestSDKEventarcServerFields(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "svc", Region: testLocation}},
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("fields").Context(ctx).Do(); err != nil {
		t.Fatalf("Triggers.Create: %v", err)
	}

	got, err := svc.Projects.Locations.Triggers.Get(parent() + "/triggers/fields").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Get: %v", err)
	}

	if got.Uid == "" {
		t.Error("uid empty, want a server-assigned uid")
	}

	if got.Etag == "" {
		t.Error("etag empty, want a server-assigned etag")
	}

	if got.Transport == nil || got.Transport.Pubsub == nil || got.Transport.Pubsub.Topic == "" {
		t.Fatalf("transport = %+v, want an auto-provisioned pubsub topic", got.Transport)
	}
}

// TestSDKEventarcPatch: updateTrigger changes the destination (and etag) via the
// updateMask, previously a 404.
func TestSDKEventarcPatch(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "old", Region: testLocation}},
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("patch-me").Context(ctx).Do(); err != nil {
		t.Fatalf("Triggers.Create: %v", err)
	}

	name := parent() + "/triggers/patch-me"

	before, err := svc.Projects.Locations.Triggers.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Get (before): %v", err)
	}

	upd := &eventarc.Trigger{
		Destination: &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "new", Region: testLocation}},
	}

	op, err := svc.Projects.Locations.Triggers.Patch(name, upd).
		UpdateMask("destination").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Patch: %v", err)
	}

	if !op.Done {
		t.Fatalf("Patch operation not done: %+v", op)
	}

	after, err := svc.Projects.Locations.Triggers.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Triggers.Get (after): %v", err)
	}

	if after.Destination == nil || after.Destination.CloudRun == nil || after.Destination.CloudRun.Service != "new" {
		t.Fatalf("destination = %+v, want cloudRun service new", after.Destination)
	}

	if after.Etag == before.Etag {
		t.Errorf("etag unchanged after patch: %q", after.Etag)
	}
}

// TestSDKEventarcListPagination: pageSize splits the results and a pageToken
// walks to the next page.
func TestSDKEventarcListPagination(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		tr := &eventarc.Trigger{
			Destination: &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "s", Region: testLocation}},
		}
		if _, err := svc.Projects.Locations.Triggers.Create(parent(), tr).TriggerId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}

	first, err := svc.Projects.Locations.Triggers.List(parent()).PageSize(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(page 1): %v", err)
	}

	if len(first.Triggers) != 2 || first.NextPageToken == "" {
		t.Fatalf("page 1 = %d triggers, token=%q; want 2 + a continuation token", len(first.Triggers), first.NextPageToken)
	}

	second, err := svc.Projects.Locations.Triggers.List(parent()).
		PageSize(2).PageToken(first.NextPageToken).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(page 2): %v", err)
	}

	if len(second.Triggers) != 1 {
		t.Fatalf("page 2 = %d triggers, want 1", len(second.Triggers))
	}
}
