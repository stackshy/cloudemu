package pubsub_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/api/googleapi"
	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// asGoogleAPIError extracts the wire error message from an SDK call error,
// failing the test if it isn't a *googleapi.Error.
func asGoogleAPIError(t *testing.T, err error) *googleapi.Error {
	t.Helper()

	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
	}

	return gErr
}

// TestSDKPubSubErrorMessagesOmitCodePrefix guards against the wire error
// message leaking the internal cerrors code name (e.g. "NotFound: ...",
// "AlreadyExists: ...", "InvalidArgument: ...") into the message an SDK caller
// sees. Real Pub/Sub never prefixes its error messages with an internal
// error-taxonomy name.
func TestSDKPubSubErrorMessagesOmitCodePrefix(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/errmsg")

	t.Run("NotFound topic", func(t *testing.T) {
		_, err := svc.Projects.Topics.Get("projects/demo/topics/does-not-exist").Context(ctx).Do()
		if err == nil {
			t.Fatal("Get on missing topic returned nil error")
		}

		gErr := asGoogleAPIError(t, err)
		if gErr.Code != http.StatusNotFound {
			t.Errorf("code = %d, want 404", gErr.Code)
		}

		assertNoCodePrefix(t, gErr.Message)
	})

	t.Run("NotFound publish to missing topic", func(t *testing.T) {
		_, err := svc.Projects.Topics.Publish("projects/demo/topics/does-not-exist",
			&pubsubv1.PublishRequest{Messages: []*pubsubv1.PubsubMessage{
				{Data: base64.StdEncoding.EncodeToString([]byte("x"))},
			}}).Context(ctx).Do()
		if err == nil {
			t.Fatal("Publish to missing topic returned nil error")
		}

		assertNoCodePrefix(t, asGoogleAPIError(t, err).Message)
	})

	t.Run("AlreadyExists duplicate topic", func(t *testing.T) {
		_, err := svc.Projects.Topics.Create("projects/demo/topics/errmsg", &pubsubv1.Topic{}).Context(ctx).Do()
		if err == nil {
			t.Fatal("duplicate Topic.Create returned nil error")
		}

		gErr := asGoogleAPIError(t, err)
		if gErr.Code != http.StatusConflict {
			t.Errorf("code = %d, want 409", gErr.Code)
		}

		assertNoCodePrefix(t, gErr.Message)
	})

	t.Run("InvalidArgument immutable subscription field", func(t *testing.T) {
		mustSub(t, svc, "projects/demo/subscriptions/errmsg", &pubsubv1.Subscription{Topic: "projects/demo/topics/errmsg"})

		_, err := svc.Projects.Subscriptions.Patch("projects/demo/subscriptions/errmsg",
			&pubsubv1.UpdateSubscriptionRequest{
				Subscription: &pubsubv1.Subscription{Topic: "projects/demo/topics/other"},
				UpdateMask:   "topic",
			}).Context(ctx).Do()
		if err == nil {
			t.Fatal("patching an immutable field returned nil error")
		}

		gErr := asGoogleAPIError(t, err)
		if gErr.Code != http.StatusBadRequest {
			t.Errorf("code = %d, want 400", gErr.Code)
		}

		assertNoCodePrefix(t, gErr.Message)
	})

	t.Run("InvalidArgument bad filter expression", func(t *testing.T) {
		_, err := svc.Projects.Subscriptions.Create("projects/demo/subscriptions/badfilter",
			&pubsubv1.Subscription{Topic: "projects/demo/topics/errmsg", Filter: "not a valid filter((("}).
			Context(ctx).Do()
		if err == nil {
			t.Fatal("Create with an invalid filter returned nil error")
		}

		assertNoCodePrefix(t, asGoogleAPIError(t, err).Message)
	})
}

// assertNoCodePrefix fails if msg contains one of cloudemu's internal
// canonical error-code names followed by a colon (the shape err.Error()
// produces via cerrors.Error, as opposed to cerrors.Message(err)) — whether
// that leak is at the very start of the message or embedded after a
// handler-added prefix like "invalid filter: ".
func assertNoCodePrefix(t *testing.T, msg string) {
	t.Helper()

	for _, prefix := range []string{"NotFound:", "AlreadyExists:", "InvalidArgument:", "FailedPrecondition:", "Internal:"} {
		if strings.Contains(msg, prefix) {
			t.Errorf("wire error message %q leaks internal code prefix %q", msg, prefix)
		}
	}
}

// TestSDKPubSubOrderingKeyBlocksSameKeyRedelivery guards enableMessageOrdering:
// while an earlier message with a given ordering key is outstanding
// (delivered, not yet acked), a later message sharing that key must not be
// delivered — real Pub/Sub holds it back to preserve per-key ordering. A
// message with a different key (or no key) is unaffected.
func TestSDKPubSubOrderingKeyBlocksSameKeyRedelivery(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/ordblock")
	mustSub(t, svc, "projects/demo/subscriptions/ordblock", &pubsubv1.Subscription{
		Topic:                 "projects/demo/topics/ordblock",
		EnableMessageOrdering: true,
	})

	publish(t, svc, "projects/demo/topics/ordblock",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("k1-a")), OrderingKey: "k1"},
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("k1-b")), OrderingKey: "k1"},
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("k2-a")), OrderingKey: "k2"},
	)

	first := pull(t, svc, "projects/demo/subscriptions/ordblock", 10)
	if len(first.ReceivedMessages) != 2 {
		t.Fatalf("first pull got %d messages, want 2 (k1-a and k2-a; k1-b must be held back)", len(first.ReceivedMessages))
	}

	var k1AckID string

	for _, m := range first.ReceivedMessages {
		body, _ := base64.StdEncoding.DecodeString(m.Message.Data)
		if string(body) == "k1-b" {
			t.Fatalf("k1-b delivered before k1-a was acked — ordering key not enforced")
		}

		if string(body) == "k1-a" {
			k1AckID = m.AckId
		}
	}

	if k1AckID == "" {
		t.Fatal("k1-a not found in first pull")
	}

	if _, err := svc.Projects.Subscriptions.Acknowledge("projects/demo/subscriptions/ordblock",
		&pubsubv1.AcknowledgeRequest{AckIds: []string{k1AckID}}).Context(ctx).Do(); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	second := pull(t, svc, "projects/demo/subscriptions/ordblock", 10)
	if len(second.ReceivedMessages) != 1 {
		t.Fatalf("after acking k1-a, pull got %d messages, want 1 (k1-b now unblocked)", len(second.ReceivedMessages))
	}

	body, _ := base64.StdEncoding.DecodeString(second.ReceivedMessages[0].Message.Data)
	if string(body) != "k1-b" {
		t.Errorf("unblocked message = %q, want k1-b", body)
	}
}

// TestSDKPubSubOrderingKeyIgnoredWithoutEnableMessageOrdering guards that
// ordering-key gating only applies when enableMessageOrdering is set — a
// subscription without it delivers same-key messages normally (both at once),
// matching real Pub/Sub, which ignores ordering keys unless the subscription
// opts in.
func TestSDKPubSubOrderingKeyIgnoredWithoutEnableMessageOrdering(t *testing.T) {
	svc := newSDKService(t)

	mustTopic(t, svc, "projects/demo/topics/ordoff")
	mustSub(t, svc, "projects/demo/subscriptions/ordoff", &pubsubv1.Subscription{Topic: "projects/demo/topics/ordoff"})

	publish(t, svc, "projects/demo/topics/ordoff",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("a")), OrderingKey: "k1"},
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("b")), OrderingKey: "k1"},
	)

	got := pull(t, svc, "projects/demo/subscriptions/ordoff", 10)
	if len(got.ReceivedMessages) != 2 {
		t.Fatalf("got %d messages, want 2 (ordering key must not gate delivery when enableMessageOrdering is false)",
			len(got.ReceivedMessages))
	}
}

// TestSDKPubSubPullAfterDetachFails guards subscriptions.detach: real Pub/Sub
// rejects Pull/StreamingPull on a detached subscription with
// FAILED_PRECONDITION rather than continuing to serve its backlog.
func TestSDKPubSubPullAfterDetachFails(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/detach")
	mustSub(t, svc, "projects/demo/subscriptions/detach", &pubsubv1.Subscription{Topic: "projects/demo/topics/detach"})

	publish(t, svc, "projects/demo/topics/detach",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("before-detach"))})

	if _, err := svc.Projects.Subscriptions.Detach("projects/demo/subscriptions/detach").Context(ctx).Do(); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/detach").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !got.Detached {
		t.Fatalf("subscription.detached = false after Detach, want true")
	}

	_, err = svc.Projects.Subscriptions.Pull("projects/demo/subscriptions/detach",
		&pubsubv1.PullRequest{MaxMessages: 10}).Context(ctx).Do()
	if err == nil {
		t.Fatal("Pull on a detached subscription returned nil error, want FAILED_PRECONDITION")
	}

	gErr := asGoogleAPIError(t, err)
	if gErr.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400 (FAILED_PRECONDITION)", gErr.Code)
	}
}
