package fcm_test

import (
	"context"
	"net/http/httptest"
	"testing"

	fcm "google.golang.org/api/fcm/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// newFCMServiceWithDriver mirrors newFCMService but also returns the backing
// notification driver. FCM v1 is a send-only API with no ListTopics on the
// wire, so ordering of the topics the handler auto-provisions is observed
// through the driver.
func newFCMServiceWithDriver(t *testing.T) (*fcm.Service, notifdriver.Notification) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{FCM: cloud.FCM})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := fcm.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("fcm.NewService: %v", err)
	}

	return svc, cloud.FCM
}

// TestSDKListOrderingDeterministic locks the #259 ordering fix for FCM:
// sending to five topics via the real SDK auto-provisions them, and listing
// the topics must return the same sequence on every call, regardless of the
// order the topics were created in.
func TestSDKListOrderingDeterministic(t *testing.T) {
	svc, driver := newFCMServiceWithDriver(t)
	ctx := context.Background()

	// Sends to five topics in a deliberately non-sorted order; each send
	// auto-provisions its topic.
	for _, topic := range []string{"zeta", "alpha", "mid", "beta", "omega"} {
		if _, err := svc.Projects.Messages.Send("projects/"+testProject, &fcm.SendMessageRequest{
			Message: &fcm.Message{
				Topic: topic,
				Data:  map[string]string{"k": "v"},
			},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Messages.Send(%s): %v", topic, err)
		}
	}

	listTopics := func() []string {
		topics, err := driver.ListTopics(ctx, scope.Scope{})
		if err != nil {
			t.Fatalf("ListTopics: %v", err)
		}

		names := make([]string, 0, len(topics))
		for _, tp := range topics {
			names = append(names, tp.Name)
		}

		return names
	}

	first := listTopics()
	if len(first) != 5 {
		t.Fatalf("got %d topics, want 5: %v", len(first), first)
	}

	for i := 0; i < 4; i++ {
		got := listTopics()
		if len(got) != len(first) {
			t.Fatalf("list #%d returned %d topics, want %d: %v", i+2, len(got), len(first), got)
		}

		for j := range first {
			if got[j] != first[j] {
				t.Fatalf("list #%d order diverged at index %d: got %q, want %q (full: %v vs %v)",
					i+2, j, got[j], first[j], got, first)
			}
		}
	}
}
