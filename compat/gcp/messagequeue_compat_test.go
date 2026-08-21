package gcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPMessageQueueCompat drives a Pub/Sub topic + subscription lifecycle
// through the real google.golang.org/api/pubsub/v1 REST client against the
// in-process wire server. Pub/Sub topics/messages map onto the portable
// "messagequeue" driver, so operation names match SQS's in
// docs/coverage/coverage.json (Topics.Create → CreateQueue, Topics.Publish →
// SendMessage, Subscriptions.Pull → ReceiveMessages, Subscriptions.Acknowledge
// → DeleteMessage). Only the operations the Pub/Sub wire handler routes to the
// driver are asserted; queue-attribute, batch, purge and visibility ops are not
// wired, so they stay amber in the matrix rather than being asserted here.
func TestGCPMessageQueueCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{PubSub: provider.PubSub})
	ctx := context.Background()

	svc, err := pubsubv1.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("pubsub NewService: %v", err)
	}

	const (
		service   = "messagequeue"
		project   = "projects/" + compat.GCPProject
		topicName = project + "/topics/compat-topic"
		subName   = project + "/subscriptions/compat-sub"
		payload   = "hello cloudemu"
	)

	sess.Op(service, "CreateQueue", func() error {
		_, err := svc.Projects.Topics.Create(topicName, &pubsubv1.Topic{
			Labels: map[string]string{"env": "compat"},
		}).Context(ctx).Do()

		return err
	})

	sess.Op(service, "GetQueueInfo", func() error {
		got, err := svc.Projects.Topics.Get(topicName).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !strings.HasSuffix(got.Name, "/topics/compat-topic") {
			return fmt.Errorf("topic name = %q, want suffix /topics/compat-topic", got.Name)
		}

		return nil
	})

	sess.Op(service, "ListQueues", func() error {
		out, err := svc.Projects.Topics.List(project).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(out.Topics) == 0 {
			return fmt.Errorf("expected at least one topic, got none")
		}

		return nil
	})

	// A subscription is required to pull; its creation is not a portable
	// messagequeue coverage op, so it is set up outside sess.Op.
	if _, err := svc.Projects.Subscriptions.Create(subName, &pubsubv1.Subscription{
		Topic: topicName,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Subscriptions.Create: %v", err)
	}

	sess.Op(service, "SendMessage", func() error {
		out, err := svc.Projects.Topics.Publish(topicName, &pubsubv1.PublishRequest{
			Messages: []*pubsubv1.PubsubMessage{
				{Data: base64.StdEncoding.EncodeToString([]byte(payload))},
			},
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(out.MessageIds) != 1 {
			return fmt.Errorf("expected 1 message id, got %d", len(out.MessageIds))
		}

		return nil
	})

	var ackID string

	sess.Op(service, "ReceiveMessages", func() error {
		pull, err := svc.Projects.Subscriptions.Pull(subName, &pubsubv1.PullRequest{
			MaxMessages: 1,
		}).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(pull.ReceivedMessages) != 1 {
			return fmt.Errorf("expected 1 received message, got %d", len(pull.ReceivedMessages))
		}

		body, err := base64.StdEncoding.DecodeString(pull.ReceivedMessages[0].Message.Data)
		if err != nil {
			return err
		}

		if string(body) != payload {
			return fmt.Errorf("message body = %q, want %q", body, payload)
		}

		ackID = pull.ReceivedMessages[0].AckId

		return nil
	})

	sess.Op(service, "DeleteMessage", func() error {
		_, err := svc.Projects.Subscriptions.Acknowledge(subName, &pubsubv1.AcknowledgeRequest{
			AckIds: []string{ackID},
		}).Context(ctx).Do()

		return err
	})

	sess.Op(service, "DeleteQueue", func() error {
		_, err := svc.Projects.Topics.Delete(topicName).Context(ctx).Do()
		return err
	})
}
