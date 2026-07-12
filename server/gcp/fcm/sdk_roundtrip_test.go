package fcm_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	fcm "google.golang.org/api/fcm/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const testProject = "demo"

func newFCMService(t *testing.T) *fcm.Service {
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

	return svc
}

func TestSDKFCMSendToTopic(t *testing.T) {
	svc := newFCMService(t)
	ctx := context.Background()

	resp, err := svc.Projects.Messages.Send("projects/"+testProject, &fcm.SendMessageRequest{
		Message: &fcm.Message{
			Topic: "weather",
			Notification: &fcm.Notification{
				Title: "Storm warning",
				Body:  "Batten down the hatches",
			},
			Data: map[string]string{"severity": "high"},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Messages.Send (topic): %v", err)
	}

	if !strings.HasPrefix(resp.Name, "projects/"+testProject+"/messages/") {
		t.Fatalf("message name = %q, want projects/%s/messages/…", resp.Name, testProject)
	}
}

func TestSDKFCMSendToToken(t *testing.T) {
	svc := newFCMService(t)
	ctx := context.Background()

	// A token-addressed message with no topic string must still round-trip to
	// a message id.
	resp, err := svc.Projects.Messages.Send("projects/"+testProject, &fcm.SendMessageRequest{
		Message: &fcm.Message{
			Token: "device-token-123",
			Data:  map[string]string{"k": "v"},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Messages.Send (token): %v", err)
	}

	if resp.Name == "" {
		t.Fatal("Messages.Send (token): empty message name")
	}
}

func TestSDKFCMSendErrors(t *testing.T) {
	svc := newFCMService(t)
	ctx := context.Background()

	// An empty request body (no message) is a 400.
	_, err := svc.Projects.Messages.Send("projects/"+testProject, &fcm.SendMessageRequest{}).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 400 {
		t.Fatalf("Send(empty): got %v, want 400", err)
	}
}
