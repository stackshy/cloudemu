package gcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	fcm "google.golang.org/api/fcm/v1"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestFCMCompat drives Firebase Cloud Messaging's messages:send through the
// real google.golang.org/api/fcm/v1 REST client against the in-process wire
// server. FCM v1 is a send-only API: its single method maps onto the portable
// "notification" driver's Publish (messages:send → Notification.Publish), so
// that operation name matches SNS's in docs/coverage/coverage.json. FCM exposes
// no topic/subscription control plane, so the remaining notification ops
// (CreateTopic, GetTopic, ListTopics, UpdateTopic, DeleteTopic, Subscribe,
// Unsubscribe, ListSubscriptions) are not routed by the handler and stay amber
// in the matrix rather than being asserted here.
func TestFCMCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{FCM: provider.FCM})
	ctx := context.Background()

	svc, err := fcm.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("fcm NewService: %v", err)
	}

	const (
		service = "notification"
		project = "projects/" + compat.GCPProject
		topic   = "compat-topic"
	)

	sess.Op(service, "Publish", func() error {
		resp, err := svc.Projects.Messages.Send(project, &fcm.SendMessageRequest{
			Message: &fcm.Message{
				Topic: topic,
				Notification: &fcm.Notification{
					Title: "compat",
					Body:  "hello cloudemu",
				},
				Data: map[string]string{"severity": "high"},
			},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !strings.HasPrefix(resp.Name, project+"/messages/") {
			return fmt.Errorf("message name = %q, want prefix %s/messages/", resp.Name, project)
		}

		return nil
	})
}
