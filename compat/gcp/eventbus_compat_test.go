package gcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	eventarc "google.golang.org/api/eventarc/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestEventarcCompat drives Eventarc's trigger control plane through the real
// google.golang.org/api/eventarc/v1 REST client against the in-process wire
// server. Eventarc models triggers as rules under a per-location bus, so the
// routed methods map onto the portable "eventbus" driver's rule operations:
// Triggers.Create → PutRule, Triggers.Get → GetRule, Triggers.List → ListRules,
// Triggers.Delete → DeleteRule. Those names match EventBridge's in
// docs/coverage/coverage.json.
//
// Eventarc exposes no bus management, target, event-publishing, or
// enable/disable surface, so the remaining eventbus ops (CreateEventBus,
// GetEventBus, ListEventBuses, UpdateEventBus, DeleteEventBus, PutEvents,
// GetEventHistory, PutTargets, ListTargets, RemoveTargets, EnableRule,
// DisableRule) are not routed by the handler and stay amber in the matrix
// rather than being asserted here.
func TestEventarcCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Eventarc: provider.Eventarc})
	ctx := context.Background()

	svc, err := eventarc.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("eventarc NewService: %v", err)
	}

	const (
		service   = "eventbus"
		location  = "us-central1"
		triggerID = "compat-trigger"
	)

	parent := "projects/" + compat.GCPProject + "/locations/" + location
	triggerName := parent + "/triggers/" + triggerID

	trigger := &eventarc.Trigger{
		EventFilters: []*eventarc.EventFilter{
			{Attribute: "type", Value: "google.cloud.storage.object.v1.finalized"},
		},
		Destination: &eventarc.Destination{
			CloudRun: &eventarc.CloudRun{Service: "compat-svc", Region: location},
		},
	}

	sess.Op(service, "PutRule", func() error {
		op, err := svc.Projects.Locations.Triggers.Create(parent, trigger).
			TriggerId(triggerID).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("create operation not done: %+v", op)
		}

		return nil
	})

	sess.Op(service, "GetRule", func() error {
		got, err := svc.Projects.Locations.Triggers.Get(triggerName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Name != triggerName {
			return fmt.Errorf("trigger name = %q, want %q", got.Name, triggerName)
		}

		return nil
	})

	sess.Op(service, "ListRules", func() error {
		list, err := svc.Projects.Locations.Triggers.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Triggers) != 1 || list.Triggers[0].Name != triggerName {
			return fmt.Errorf("list = %+v, want one trigger %q", list.Triggers, triggerName)
		}

		return nil
	})

	sess.Op(service, "DeleteRule", func() error {
		op, err := svc.Projects.Locations.Triggers.Delete(triggerName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("delete operation not done: %+v", op)
		}

		_, err = svc.Projects.Locations.Triggers.Get(triggerName).Context(ctx).Do()

		var gerr *googleapi.Error
		if !errors.As(err, &gerr) || gerr.Code != 404 {
			return fmt.Errorf("get after delete: got %v, want 404", err)
		}

		return nil
	})
}
