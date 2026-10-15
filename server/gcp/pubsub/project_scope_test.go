package pubsub_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/gcp/pubsub"
)

const (
	projA = "p-a"
	projB = "p-b"
)

// newScopedService serves a Pub/Sub handler whose default project is p-a and
// returns a client plus the handler, so tests can drive the in-process
// cross-service publish path too.
func newScopedService(t *testing.T) (*pubsubv1.Service, *pubsub.Handler) {
	t.Helper()

	cloud := cloudemu.NewGCP()
	h := pubsub.New(cloud.PubSub, projA)

	ts := httptest.NewServer(server.New(h))
	t.Cleanup(ts.Close)

	svc, err := pubsubv1.NewService(context.Background(),
		option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc, h
}

func topicPath(project, name string) string { return "projects/" + project + "/topics/" + name }

func subPath(project, name string) string { return "projects/" + project + "/subscriptions/" + name }

func isStatus(err error, code int) bool {
	var gerr *googleapi.Error

	return errors.As(err, &gerr) && gerr.Code == code
}

func mustCreateTopic(t *testing.T, svc *pubsubv1.Service, project, name string, labels map[string]string) {
	t.Helper()

	if _, err := svc.Projects.Topics.Create(topicPath(project, name),
		&pubsubv1.Topic{Labels: labels}).Do(); err != nil {
		t.Fatalf("create %s: %v", topicPath(project, name), err)
	}
}

func mustCreateSub(t *testing.T, svc *pubsubv1.Service, project, name, topic string) {
	t.Helper()

	if _, err := svc.Projects.Subscriptions.Create(subPath(project, name),
		&pubsubv1.Subscription{Topic: topic}).Do(); err != nil {
		t.Fatalf("create %s: %v", subPath(project, name), err)
	}
}

func TestPubSubSameNameTwoProjects(t *testing.T) {
	svc, _ := newScopedService(t)

	mustCreateTopic(t, svc, projA, "t9", map[string]string{"owner": "a"})
	mustCreateTopic(t, svc, projB, "t9", map[string]string{"owner": "b"})
	mustCreateSub(t, svc, projA, "s9", topicPath(projA, "t9"))
	mustCreateSub(t, svc, projB, "s9", topicPath(projB, "t9"))

	for _, p := range []string{projA, projB} {
		got, err := svc.Projects.Topics.Get(topicPath(p, "t9")).Do()
		if err != nil {
			t.Fatalf("get %s topic: %v", p, err)
		}

		if got.Name != topicPath(p, "t9") || got.Labels["owner"] != p[2:] {
			t.Fatalf("%s topic = %q labels %v", p, got.Name, got.Labels)
		}

		sub, err := svc.Projects.Subscriptions.Get(subPath(p, "s9")).Do()
		if err != nil {
			t.Fatalf("get %s sub: %v", p, err)
		}

		if sub.Topic != topicPath(p, "t9") {
			t.Fatalf("%s sub topic = %q", p, sub.Topic)
		}
	}
}

func TestPubSubIsolatedGetListDelete(t *testing.T) {
	svc, _ := newScopedService(t)

	mustCreateTopic(t, svc, projA, "only-a", nil)
	mustCreateTopic(t, svc, projA, "t9", nil)
	mustCreateTopic(t, svc, projB, "t9", nil)
	mustCreateSub(t, svc, projA, "s9", topicPath(projA, "t9"))
	mustCreateSub(t, svc, projB, "s9", topicPath(projB, "t9"))

	if _, err := svc.Projects.Topics.Get(topicPath(projB, "only-a")).Do(); !isStatus(err, http.StatusNotFound) {
		t.Fatalf("get p-a topic under p-b: err = %v, want 404", err)
	}

	topics, err := svc.Projects.Topics.List("projects/" + projB).Do()
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}

	if len(topics.Topics) != 1 || topics.Topics[0].Name != topicPath(projB, "t9") {
		t.Fatalf("p-b topics = %+v, want only p-b/t9", topics.Topics)
	}

	subs, err := svc.Projects.Subscriptions.List("projects/" + projB).Do()
	if err != nil {
		t.Fatalf("list subs: %v", err)
	}

	if len(subs.Subscriptions) != 1 || subs.Subscriptions[0].Name != subPath(projB, "s9") {
		t.Fatalf("p-b subs = %+v, want only p-b/s9", subs.Subscriptions)
	}

	if _, err := svc.Projects.Subscriptions.Delete(subPath(projB, "s9")).Do(); err != nil {
		t.Fatalf("delete p-b sub: %v", err)
	}

	if _, err := svc.Projects.Topics.Delete(topicPath(projB, "t9")).Do(); err != nil {
		t.Fatalf("delete p-b topic: %v", err)
	}

	if _, err := svc.Projects.Topics.Get(topicPath(projA, "t9")).Do(); err != nil {
		t.Fatalf("p-a topic after p-b delete: %v", err)
	}

	sub, err := svc.Projects.Subscriptions.Get(subPath(projA, "s9")).Do()
	if err != nil {
		t.Fatalf("p-a sub after p-b delete: %v", err)
	}

	if sub.Topic != topicPath(projA, "t9") {
		t.Fatalf("p-a sub topic = %q, want it still attached", sub.Topic)
	}
}

// TestPubSubCrossProjectSubscription pins real Pub/Sub's cross-project attach:
// a subscription in p-b may read a topic in p-a by its full name.
func TestPubSubCrossProjectSubscription(t *testing.T) {
	svc, _ := newScopedService(t)

	mustCreateTopic(t, svc, projA, "shared", nil)
	mustCreateSub(t, svc, projB, "reader", topicPath(projA, "shared"))

	if _, err := svc.Projects.Topics.Publish(topicPath(projA, "shared"), &pubsubv1.PublishRequest{
		Messages: []*pubsubv1.PubsubMessage{{Data: "aGk="}},
	}).Do(); err != nil {
		t.Fatalf("publish: %v", err)
	}

	pulled, err := svc.Projects.Subscriptions.Pull(subPath(projB, "reader"),
		&pubsubv1.PullRequest{MaxMessages: 5}).Do()
	if err != nil {
		t.Fatalf("pull: %v", err)
	}

	if len(pulled.ReceivedMessages) != 1 {
		t.Fatalf("pulled %d messages, want 1", len(pulled.ReceivedMessages))
	}

	attached, err := svc.Projects.Topics.Subscriptions.List(topicPath(projA, "shared")).Do()
	if err != nil {
		t.Fatalf("list topic subs: %v", err)
	}

	if len(attached.Subscriptions) != 1 || attached.Subscriptions[0] != subPath(projB, "reader") {
		t.Fatalf("topic subs = %v, want [%s]", attached.Subscriptions, subPath(projB, "reader"))
	}
}

// TestPubSubCrossServicePublishProject pins that an in-process publish (the
// GCS notification and Monitoring channel path) reaches only the named
// project's topic.
func TestPubSubCrossServicePublishProject(t *testing.T) {
	svc, h := newScopedService(t)

	for _, p := range []string{projA, projB} {
		mustCreateTopic(t, svc, p, "t", nil)
		mustCreateSub(t, svc, p, "s", topicPath(p, "t"))
	}

	h.PublishToTopic(context.Background(), projB, "t", []byte("to-b"), nil)
	h.PublishMessage(context.Background(), projB, "t", []byte("to-b-2"), nil)

	want := map[string]int{projA: 0, projB: 2}
	for p, n := range want {
		pulled, err := svc.Projects.Subscriptions.Pull(subPath(p, "s"),
			&pubsubv1.PullRequest{MaxMessages: 10}).Do()
		if err != nil {
			t.Fatalf("pull %s: %v", p, err)
		}

		if len(pulled.ReceivedMessages) != n {
			t.Fatalf("%s pulled %d messages, want %d", p, len(pulled.ReceivedMessages), n)
		}
	}
}
