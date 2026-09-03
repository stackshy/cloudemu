package pubsub_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	pubsubv1 "google.golang.org/api/pubsub/v1"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestSDKPubSubTopicIAMPolicyEtagRoundTrips proves getIamPolicy returns a
// stable etag for an unset policy (repeat reads must not mint a new one each
// time), and every setIamPolicy advances it.
func TestSDKPubSubTopicIAMPolicyEtagRoundTrips(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/etag-rt")
	const res = "projects/demo/topics/etag-rt"

	first, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	second, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (repeat): %v", err)
	}

	if first.Etag == "" || first.Etag != second.Etag {
		t.Fatalf("unset policy etag should be stable across reads, got %q then %q", first.Etag, second.Etag)
	}

	set, err := svc.Projects.Topics.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.viewer", Members: []string{"user:a@b.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == "" || set.Etag == first.Etag {
		t.Fatalf("etag should change on setIamPolicy, got %q (was %q)", set.Etag, first.Etag)
	}

	// A second unconditional set (no etag on the request) further advances it.
	set2, err := svc.Projects.Topics.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.editor", Members: []string{"user:c@d.com"}}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy (second): %v", err)
	}

	if set2.Etag == "" || set2.Etag == set.Etag {
		t.Fatalf("etag should change again, stayed %q", set2.Etag)
	}
}

// TestSDKPubSubTopicIAMPolicyStaleEtagRejected proves a setIamPolicy built
// from a stale read (someone else changed the policy in between) is rejected
// with a conflict instead of silently clobbering the newer policy.
func TestSDKPubSubTopicIAMPolicyStaleEtagRejected(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/etag-stale")
	const res = "projects/demo/topics/etag-stale"

	stale, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (stale read): %v", err)
	}

	// Someone else updates the policy first, advancing the etag.
	if _, wErr := svc.Projects.Topics.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Etag:     stale.Etag,
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.viewer", Members: []string{"user:winner@example.com"}}},
		},
	}).Context(ctx).Do(); wErr != nil {
		t.Fatalf("SetIamPolicy (winning writer): %v", wErr)
	}

	// The stale writer's set, built from the pre-update etag, must fail.
	_, err = svc.Projects.Topics.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Etag:     stale.Etag,
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.admin", Members: []string{"user:loser@example.com"}}},
		},
	}).Context(ctx).Do()
	if err == nil {
		t.Fatalf("SetIamPolicy with a stale etag should have failed")
	}

	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
	}

	if gErr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %v", gErr.Code, gErr)
	}

	// The winning writer's binding must be intact — the stale write must not
	// have partially applied.
	final, err := svc.Projects.Topics.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (final): %v", err)
	}

	if len(final.Bindings) != 1 || final.Bindings[0].Members[0] != "user:winner@example.com" {
		t.Fatalf("winning writer's binding should be intact, unaffected by the stale write: %+v", final.Bindings)
	}
}

// TestSDKPubSubSubscriptionIAMPolicyStaleEtagRejected mirrors the topic case
// for subscriptions.setIamPolicy.
func TestSDKPubSubSubscriptionIAMPolicyStaleEtagRejected(t *testing.T) {
	svc := newSDKService(t)
	ctx := context.Background()

	mustTopic(t, svc, "projects/demo/topics/etag-sub-t")
	mustSub(t, svc, "projects/demo/subscriptions/etag-sub-s",
		&pubsubv1.Subscription{Topic: "projects/demo/topics/etag-sub-t"})

	const res = "projects/demo/subscriptions/etag-sub-s"

	stale, err := svc.Projects.Subscriptions.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (stale read): %v", err)
	}

	if _, wErr := svc.Projects.Subscriptions.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Etag:     stale.Etag,
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.subscriber", Members: []string{"user:winner@example.com"}}},
		},
	}).Context(ctx).Do(); wErr != nil {
		t.Fatalf("SetIamPolicy (winning writer): %v", wErr)
	}

	_, err = svc.Projects.Subscriptions.SetIamPolicy(res, &pubsubv1.SetIamPolicyRequest{
		Policy: &pubsubv1.Policy{
			Etag:     stale.Etag,
			Bindings: []*pubsubv1.Binding{{Role: "roles/pubsub.editor", Members: []string{"user:loser@example.com"}}},
		},
	}).Context(ctx).Do()
	if err == nil {
		t.Fatalf("SetIamPolicy with a stale etag should have failed")
	}

	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("expected a googleapi.Error, got %T: %v", err, err)
	}

	if gErr.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %v", gErr.Code, gErr)
	}

	final, err := svc.Projects.Subscriptions.GetIamPolicy(res).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (final): %v", err)
	}

	if len(final.Bindings) != 1 || final.Bindings[0].Members[0] != "user:winner@example.com" {
		t.Fatalf("winning writer's binding should be intact: %+v", final.Bindings)
	}
}

// concurrentSetIAMWriters is how many goroutines race to setIamPolicy from
// the same starting etag in the concurrency tests below.
const concurrentSetIAMWriters = 10

// TestSDKPubSubTopicIAMPolicyConcurrentSetIsAtomic proves the etag
// precondition check and the write happen atomically: N goroutines all
// submit a setIamPolicy built from the SAME starting etag (each with its own
// distinct binding, replacing the whole policy). A separate
// read-etag-then-write pair would let every one of them pass the check
// before any of them writes, silently losing all but the last write applied;
// the atomic compare-and-set must let exactly one of them win and every
// loser must see a 409.
func TestSDKPubSubTopicIAMPolicyConcurrentSetIsAtomic(t *testing.T) {
	ctx := context.Background()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{PubSub: cloudP.PubSub})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := pubsubv1.NewService(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	const topicName = "projects/demo/topics/etag-concurrent"

	mustTopic(t, svc, topicName)

	initial, err := svc.Projects.Topics.GetIamPolicy(topicName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (initial): %v", err)
	}

	etag0 := initial.Etag
	iamURL := ts.URL + "/v1/" + topicName + ":setIamPolicy"

	statuses := make([]int, concurrentSetIAMWriters)

	var wg sync.WaitGroup

	for i := range concurrentSetIAMWriters {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			member := fmt.Sprintf("user:writer-%d@example.com", i)
			body := fmt.Sprintf(
				`{"policy":{"bindings":[{"role":"roles/pubsub.viewer","members":["%s"]}],"etag":%q}}`,
				member, etag0,
			)

			req, rErr := http.NewRequestWithContext(ctx, http.MethodPost, iamURL, strings.NewReader(body))
			if rErr != nil {
				t.Errorf("writer %d: NewRequest: %v", i, rErr)
				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, dErr := ts.Client().Do(req)
			if dErr != nil {
				t.Errorf("writer %d: Do: %v", i, dErr)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			statuses[i] = resp.StatusCode
		}(i)
	}

	wg.Wait()

	successes := 0

	for i, code := range statuses {
		switch code {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			// expected for every loser
		default:
			t.Errorf("writer %d: unexpected status %d", i, code)
		}
	}

	if successes != 1 {
		t.Fatalf("expected exactly 1 of %d concurrent setIamPolicy calls from the same etag to "+
			"succeed, got %d — a lost update slipped through the TOCTOU window", concurrentSetIAMWriters, successes)
	}

	final, err := svc.Projects.Topics.GetIamPolicy(topicName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy (final): %v", err)
	}

	if len(final.Bindings) != 1 {
		t.Fatalf("expected exactly the single winning binding to persist, got %v", final.Bindings)
	}
}
