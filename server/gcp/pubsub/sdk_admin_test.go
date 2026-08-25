package pubsub_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	pubsubv1 "google.golang.org/api/pubsub/v1"
)

// TestSDKPubSubTopicIAM guards topic IAM: getIamPolicy returns a policy with an
// etag, setIamPolicy persists bindings, and testIamPermissions echoes the held
// permissions.
func TestSDKPubSubTopicIAM(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/iam")
	const res = "projects/demo/topics/iam"

	pol, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if pol.Etag == "" {
		t.Errorf("initial policy etag empty, want non-empty")
	}

	set, err := svc.Projects.Topics.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.viewer", Members: []string{"user:a@b.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if len(set.Bindings) != 1 || set.Bindings[0].Role != "roles/pubsub.viewer" {
		t.Fatalf("SetIamPolicy bindings not persisted: %+v", set.Bindings)
	}

	got, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy after set: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Members[0] != "user:a@b.com" {
		t.Fatalf("policy not round-tripped: %+v", got.Bindings)
	}

	test, err := svc.Projects.Topics.TestIamPermissions(res, &pubsubv1.TestIamPermissionsRequest{
		Permissions: []string{"pubsub.topics.publish"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(test.Permissions) != 1 || test.Permissions[0] != "pubsub.topics.publish" {
		t.Fatalf("TestIamPermissions=%v", test.Permissions)
	}
}

// TestSDKPubSubTopicSubscriptionsList guards topics.subscriptions.list — it
// returns the topic's subscription names, not an empty list.
func TestSDKPubSubTopicSubscriptionsList(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/lt")
	mustSub(t, svc, "projects/demo/subscriptions/ls1", &pubsubv1.Subscription{Topic: "projects/demo/topics/lt"})
	mustSub(t, svc, "projects/demo/subscriptions/ls2", &pubsubv1.Subscription{Topic: "projects/demo/topics/lt"})

	resp, err := svc.Projects.Topics.Subscriptions.List("projects/demo/topics/lt").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Topics.Subscriptions.List: %v", err)
	}

	if len(resp.Subscriptions) != 2 {
		t.Fatalf("listed %d subscriptions, want 2: %v", len(resp.Subscriptions), resp.Subscriptions)
	}

	if !strings.HasSuffix(resp.Subscriptions[0], "/subscriptions/ls1") {
		t.Errorf("subscriptions[0]=%q want .../ls1", resp.Subscriptions[0])
	}
}

// TestSDKPubSubListPagination guards pageSize/pageToken + nextPageToken on
// topics.list.
func TestSDKPubSubListPagination(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	for i := range 5 {
		mustTopic(t, svc, fmt.Sprintf("projects/demo/topics/pg%d", i))
	}

	first, err := svc.Projects.Topics.List("projects/demo").PageSize(2).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Topics.List page 1: %v", err)
	}

	if len(first.Topics) != 2 {
		t.Fatalf("page 1 returned %d topics, want 2", len(first.Topics))
	}

	if first.NextPageToken == "" {
		t.Fatal("page 1 nextPageToken empty, want a continuation token")
	}

	seen := len(first.Topics)
	token := first.NextPageToken

	for token != "" {
		page, perr := svc.Projects.Topics.List("projects/demo").PageSize(2).PageToken(token).Context(ctx).Do()
		if perr != nil {
			t.Fatalf("Topics.List page: %v", perr)
		}

		seen += len(page.Topics)
		token = page.NextPageToken
	}

	if seen != 5 {
		t.Fatalf("paged through %d topics, want 5", seen)
	}
}

// TestSDKPubSubModifyPushConfig guards subscriptions.modifyPushConfig.
func TestSDKPubSubModifyPushConfig(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/mpc")
	mustSub(t, svc, "projects/demo/subscriptions/mpc", &pubsubv1.Subscription{Topic: "projects/demo/topics/mpc"})

	if _, err := svc.Projects.Subscriptions.ModifyPushConfig("projects/demo/subscriptions/mpc",
		&pubsubv1.ModifyPushConfigRequest{
			PushConfig: &pubsubv1.PushConfig{PushEndpoint: "https://example.com/hook"},
		}).Context(ctx).Do(); err != nil {
		t.Fatalf("ModifyPushConfig: %v", err)
	}

	got, err := svc.Projects.Subscriptions.Get("projects/demo/subscriptions/mpc").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.PushConfig == nil || got.PushConfig.PushEndpoint != "https://example.com/hook" {
		t.Fatalf("push config not applied: %+v", got.PushConfig)
	}
}

// TestSDKPubSubSnapshotsAndSeek guards snapshot CRUD plus seek-to-snapshot and
// seek-to-time replay.
func TestSDKPubSubSnapshotsAndSeek(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/sk")
	mustSub(t, svc, "projects/demo/subscriptions/sk", &pubsubv1.Subscription{Topic: "projects/demo/topics/sk"})

	// Snapshot the empty backlog, then publish + consume a message.
	snap, err := svc.Projects.Snapshots.Create("projects/demo/snapshots/snap1",
		&pubsubv1.CreateSnapshotRequest{Subscription: "projects/demo/subscriptions/sk"}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Snapshots.Create: %v", err)
	}

	if !strings.HasSuffix(snap.Topic, "/topics/sk") {
		t.Errorf("snapshot topic=%q want .../topics/sk", snap.Topic)
	}

	if _, err := svc.Projects.Snapshots.Get("projects/demo/snapshots/snap1").Context(ctx).Do(); err != nil {
		t.Fatalf("Snapshots.Get: %v", err)
	}

	list, err := svc.Projects.Snapshots.List("projects/demo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Snapshots.List: %v", err)
	}

	if len(list.Snapshots) != 1 {
		t.Fatalf("listed %d snapshots, want 1", len(list.Snapshots))
	}

	publish(t, svc, "projects/demo/topics/sk",
		&pubsubv1.PubsubMessage{Data: base64.StdEncoding.EncodeToString([]byte("s"))})

	first := pull(t, svc, "projects/demo/subscriptions/sk", 1)
	if len(first.ReceivedMessages) != 1 {
		t.Fatalf("pull got %d, want 1", len(first.ReceivedMessages))
	}

	// Ack it so it would not normally redeliver.
	if _, err := svc.Projects.Subscriptions.Acknowledge("projects/demo/subscriptions/sk",
		&pubsubv1.AcknowledgeRequest{AckIds: []string{first.ReceivedMessages[0].AckId}}).Context(ctx).Do(); err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}

	// Seek to the snapshot (empty-backlog cursor): the acked message replays.
	if _, err := svc.Projects.Subscriptions.Seek("projects/demo/subscriptions/sk",
		&pubsubv1.SeekRequest{Snapshot: "projects/demo/snapshots/snap1"}).Context(ctx).Do(); err != nil {
		t.Fatalf("Seek(snapshot): %v", err)
	}

	replay := pull(t, svc, "projects/demo/subscriptions/sk", 1)
	if len(replay.ReceivedMessages) != 1 {
		t.Fatalf("after seek-to-snapshot, pull got %d, want 1 (replay)", len(replay.ReceivedMessages))
	}

	// Seek to a far-future time marks everything acknowledged: no replay.
	if _, err := svc.Projects.Subscriptions.Seek("projects/demo/subscriptions/sk",
		&pubsubv1.SeekRequest{Time: "2999-01-01T00:00:00Z"}).Context(ctx).Do(); err != nil {
		t.Fatalf("Seek(time): %v", err)
	}

	none := pull(t, svc, "projects/demo/subscriptions/sk", 1)
	if len(none.ReceivedMessages) != 0 {
		t.Fatalf("after seek-to-future-time, pull got %d, want 0", len(none.ReceivedMessages))
	}

	if _, err := svc.Projects.Snapshots.Delete("projects/demo/snapshots/snap1").Context(ctx).Do(); err != nil {
		t.Fatalf("Snapshots.Delete: %v", err)
	}
}
