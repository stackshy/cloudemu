package eventarc_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	eventarc "google.golang.org/api/eventarc/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpprovider "github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// newFullEventarcService boots a server with Eventarc, Cloud Functions and
// Cloud Run all wired (mirroring DriversFrom / the standalone binary), so
// destination-existence validation is exercised end-to-end.
func newFullEventarcService(t *testing.T) (*eventarc.Service, *gcpprovider.Provider) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.DriversFrom(cloud))

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := eventarc.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("eventarc.NewService: %v", err)
	}

	return svc, cloud
}

func wantGoogleAPIError(t *testing.T, err error, code int, contains string) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != code {
		t.Fatalf("got %v, want a %d googleapi.Error", err, code)
	}

	if contains != "" && !strings.Contains(gerr.Message, contains) {
		t.Fatalf("error message = %q, want it to contain %q", gerr.Message, contains)
	}
}

// TestSDKEventarcMissingTypeFilter: an eventFilters set with no "type" filter
// is a 400 INVALID_ARGUMENT, mirroring real Eventarc's admission check.
func TestSDKEventarcMissingTypeFilter(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "source", Value: "my.app"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "svc"}},
	}

	_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("no-type").Context(ctx).Do()
	wantGoogleAPIError(t, err, 400, "type")
}

// TestSDKEventarcUnknownEventType: a "type" filter naming an event type
// outside the catalog CloudEmu validates against is a 400 INVALID_ARGUMENT.
func TestSDKEventarcUnknownEventType(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "not.a.real.event.type"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "svc"}},
	}

	_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("bad-type").Context(ctx).Do()
	wantGoogleAPIError(t, err, 400, "not a known Eventarc event type")
}

// TestSDKEventarcAuditLogRequiresServiceAndMethod: an audit-log trigger
// (google.cloud.audit.log.v1.written) is rejected unless it also carries
// serviceName and methodName filters pinning it to one API method; with both
// present, it is created successfully.
func TestSDKEventarcAuditLogRequiresServiceAndMethod(t *testing.T) {
	svc := newEventarcService(t)
	ctx := context.Background()

	dest := &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "svc"}}

	cases := []struct {
		name    string
		filters []*eventarc.EventFilter
		wantErr bool
	}{
		{
			name:    "missing both",
			filters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.audit.log.v1.written"}},
			wantErr: true,
		},
		{
			name: "missing methodName",
			filters: []*eventarc.EventFilter{
				{Attribute: "type", Value: "google.cloud.audit.log.v1.written"},
				{Attribute: "serviceName", Value: "storage.googleapis.com"},
			},
			wantErr: true,
		},
		{
			name: "both present",
			filters: []*eventarc.EventFilter{
				{Attribute: "type", Value: "google.cloud.audit.log.v1.written"},
				{Attribute: "serviceName", Value: "storage.googleapis.com"},
				{Attribute: "methodName", Value: "storage.objects.create"},
			},
			wantErr: false,
		},
	}

	for i, c := range cases {
		trigger := &eventarc.Trigger{EventFilters: c.filters, Destination: dest}

		_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
			TriggerId("audit-" + string(rune('a'+i))).Context(ctx).Do()

		if c.wantErr {
			wantGoogleAPIError(t, err, 400, "")
		} else if err != nil {
			t.Fatalf("%s: Create: %v", c.name, err)
		}
	}
}

// TestSDKEventarcDestinationCloudRunNotFound: a trigger routing to a Cloud
// Run service that doesn't exist is a 404 NOT_FOUND, not a silently-stored
// dead route; once the service exists, the identical Create succeeds.
func TestSDKEventarcDestinationCloudRunNotFound(t *testing.T) {
	svc, cloud := newFullEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "ghost-run", Region: testLocation}},
	}

	_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("dest-run").Context(ctx).Do()
	wantGoogleAPIError(t, err, 404, "ghost-run")

	if _, err := cloud.CloudRun.CreateService(ctx, crdriver.ServiceConfig{Name: "ghost-run", Location: testLocation}); err != nil {
		t.Fatalf("CloudRun.CreateService: %v", err)
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("dest-run").Context(ctx).Do(); err != nil {
		t.Fatalf("Create after the destination exists: %v", err)
	}
}

// TestSDKEventarcDestinationCloudFunctionNotFound is the Cloud Function analog
// of TestSDKEventarcDestinationCloudRunNotFound.
func TestSDKEventarcDestinationCloudFunctionNotFound(t *testing.T) {
	svc, cloud := newFullEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{CloudFunction: "ghost-fn"},
	}

	_, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("dest-fn").Context(ctx).Do()
	wantGoogleAPIError(t, err, 404, "ghost-fn")

	if _, err := cloud.CloudFunctions.CreateFunction(ctx, sdrv.FunctionConfig{Name: "ghost-fn"}); err != nil {
		t.Fatalf("CloudFunctions.CreateFunction: %v", err)
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).TriggerId("dest-fn").Context(ctx).Do(); err != nil {
		t.Fatalf("Create after the destination exists: %v", err)
	}
}

// TestSDKEventarcPatchValidation: patching eventFilters or destination goes
// through the same validation as Create, and a rejected patch leaves the
// stored trigger unchanged.
func TestSDKEventarcPatchValidation(t *testing.T) {
	svc, cloud := newFullEventarcService(t)
	ctx := context.Background()

	if _, err := cloud.CloudRun.CreateService(ctx, crdriver.ServiceConfig{Name: "live", Location: testLocation}); err != nil {
		t.Fatalf("CloudRun.CreateService: %v", err)
	}

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "live", Region: testLocation}},
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("patch-validate").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	name := parent() + "/triggers/patch-validate"

	// A patch with an unknown event type is rejected.
	badFilters := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "nope"}},
	}

	_, err := svc.Projects.Locations.Triggers.Patch(name, badFilters).
		UpdateMask("eventFilters").Context(ctx).Do()
	wantGoogleAPIError(t, err, 400, "")

	// A patch routing to a nonexistent Cloud Run service is rejected.
	badDest := &eventarc.Trigger{
		Destination: &eventarc.Destination{CloudRun: &eventarc.CloudRun{Service: "ghost", Region: testLocation}},
	}

	_, err = svc.Projects.Locations.Triggers.Patch(name, badDest).
		UpdateMask("destination").Context(ctx).Do()
	wantGoogleAPIError(t, err, 404, "")

	// Both rejected patches left the trigger exactly as created.
	got, err := svc.Projects.Locations.Triggers.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(got.EventFilters) != 1 || got.EventFilters[0].Value != "google.cloud.storage.object.v1.finalized" {
		t.Fatalf("eventFilters = %+v, want the original storage-finalized filter unchanged", got.EventFilters)
	}

	if got.Destination == nil || got.Destination.CloudRun == nil || got.Destination.CloudRun.Service != "live" {
		t.Fatalf("destination = %+v, want the original service \"live\" unchanged", got.Destination)
	}
}

// TestSDKEventarcWorkflowDestinationUnvalidated: a Workflow destination isn't
// backed by a driver, so it is accepted unchecked even on the full server —
// documenting the deliberate scope limit rather than silently 404ing every
// workflow trigger.
func TestSDKEventarcWorkflowDestinationUnvalidated(t *testing.T) {
	svc, _ := newFullEventarcService(t)
	ctx := context.Background()

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"}},
		Destination:  &eventarc.Destination{Workflow: "projects/demo/locations/us-central1/workflows/ghost"},
	}

	if _, err := svc.Projects.Locations.Triggers.Create(parent(), trigger).
		TriggerId("wf-dest").Context(ctx).Do(); err != nil {
		t.Fatalf("Create with an unmodeled Workflow destination: %v", err)
	}
}
