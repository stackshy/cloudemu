package cloudtasks_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/cloudtasks"
	"github.com/stackshy/cloudemu/v2/services/cloudtasks/driver"
)

const parent = "projects/demo/locations/us-central1"

func newMock() *cloudtasks.Mock {
	return cloudtasks.New(config.NewOptions())
}

func TestCreateFillsDefaults(t *testing.T) {
	m := newMock()

	q, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: parent + "/queues/a"})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if q.State != driver.StateRunning {
		t.Fatalf("state = %q, want RUNNING", q.State)
	}

	// Cloud Tasks always emits fully-populated rateLimits + retryConfig blocks.
	if q.RateLimits == nil || q.RetryConfig == nil {
		t.Fatalf("rateLimits/retryConfig must be populated: %+v %+v", q.RateLimits, q.RetryConfig)
	}

	rl := q.RateLimits
	if rl.MaxDispatchesPerSecond != 500 || rl.MaxConcurrentDispatches != 1000 || rl.MaxBurstSize != 100 {
		t.Fatalf("rateLimits defaults = %+v, want dps=500 concurrent=1000 burst=100", rl)
	}

	rc := q.RetryConfig
	if rc.MaxAttempts != 100 || rc.MinBackoff != "0.100s" || rc.MaxBackoff != "3600s" ||
		rc.MaxDoublings != 16 || rc.MaxRetryDuration != "0s" {
		t.Fatalf("retryConfig defaults = %+v", rc)
	}
}

func TestCreateRecomputesMaxBurstSize(t *testing.T) {
	m := newMock()

	// User-supplied maxBurstSize is ignored; it is recomputed from the rate.
	cfg := driver.QueueConfig{
		Name:       parent + "/queues/burst",
		RateLimits: &driver.RateLimits{MaxDispatchesPerSecond: 5, MaxBurstSize: 999, MaxConcurrentDispatches: 7},
	}

	q, err := m.CreateQueue(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if q.RateLimits.MaxBurstSize == 999 {
		t.Fatalf("maxBurstSize was echoed (999) instead of recomputed")
	}

	if q.RateLimits.MaxConcurrentDispatches != 7 || q.RateLimits.MaxDispatchesPerSecond != 5 {
		t.Fatalf("user rate settings not preserved: %+v", q.RateLimits)
	}
}

func TestCreatePreservesMaxAttemptsUnlimited(t *testing.T) {
	m := newMock()

	cfg := driver.QueueConfig{
		Name:        parent + "/queues/unlimited",
		RetryConfig: &driver.RetryConfig{MaxAttempts: -1},
	}

	q, err := m.CreateQueue(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	if q.RetryConfig.MaxAttempts != -1 {
		t.Fatalf("maxAttempts = %d, want -1 (unlimited preserved)", q.RetryConfig.MaxAttempts)
	}
}

func TestDuplicateCreate(t *testing.T) {
	m := newMock()
	name := parent + "/queues/dup"

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name}); err != nil {
		t.Fatalf("first CreateQueue: %v", err)
	}

	_, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name})
	assertCode(t, err, cerrors.AlreadyExists)
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.GetQueue(context.Background(), parent+"/queues/missing")
	assertCode(t, err, cerrors.NotFound)
}

func TestPauseResume(t *testing.T) {
	m := newMock()
	name := parent + "/queues/toggle"

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	paused, err := m.PauseQueue(context.Background(), name)
	if err != nil {
		t.Fatalf("PauseQueue: %v", err)
	}

	if paused.State != driver.StatePaused {
		t.Fatalf("state after pause = %q, want PAUSED", paused.State)
	}

	resumed, err := m.ResumeQueue(context.Background(), name)
	if err != nil {
		t.Fatalf("ResumeQueue: %v", err)
	}

	if resumed.State != driver.StateRunning {
		t.Fatalf("state after resume = %q, want RUNNING", resumed.State)
	}
}

func TestPurgeSetsPurgeTime(t *testing.T) {
	m := newMock()
	name := parent + "/queues/purgeable"

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	q, err := m.PurgeQueue(context.Background(), name)
	if err != nil {
		t.Fatalf("PurgeQueue: %v", err)
	}

	if q.PurgeTime.IsZero() {
		t.Fatalf("purgeTime not set after purge")
	}
}

func TestPatchUpdateMask(t *testing.T) {
	m := newMock()
	name := parent + "/queues/patch"

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{
		Name:        name,
		RetryConfig: &driver.RetryConfig{MaxAttempts: 3},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	// Patch only rateLimits; retryConfig must survive.
	patched, err := m.PatchQueue(context.Background(), driver.QueueConfig{
		Name:       name,
		RateLimits: &driver.RateLimits{MaxDispatchesPerSecond: 10, MaxConcurrentDispatches: 2},
	}, []string{"rateLimits"})
	if err != nil {
		t.Fatalf("PatchQueue: %v", err)
	}

	if patched.RateLimits.MaxDispatchesPerSecond != 10 || patched.RateLimits.MaxConcurrentDispatches != 2 {
		t.Fatalf("rateLimits not patched: %+v", patched.RateLimits)
	}

	if patched.RetryConfig.MaxAttempts != 3 {
		t.Fatalf("retryConfig clobbered by masked patch: maxAttempts=%d, want 3", patched.RetryConfig.MaxAttempts)
	}
}

func TestListScopedToParent(t *testing.T) {
	m := newMock()

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: parent + "/queues/one"}); err != nil {
		t.Fatalf("CreateQueue one: %v", err)
	}

	other := "projects/demo/locations/europe-west1"
	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: other + "/queues/two"}); err != nil {
		t.Fatalf("CreateQueue two: %v", err)
	}

	list, err := m.ListQueues(context.Background(), parent)
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}

	if len(list) != 1 || list[0].Name != parent+"/queues/one" {
		t.Fatalf("list = %+v, want only the us-central1 queue", list)
	}
}

func TestIAMPolicyRoundTrip(t *testing.T) {
	m := newMock()
	name := parent + "/queues/iam"

	if _, err := m.CreateQueue(context.Background(), driver.QueueConfig{Name: name}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}

	// getIamPolicy on an existing queue with no policy returns an empty policy.
	empty, err := m.GetIamPolicy(context.Background(), name)
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(empty.Bindings) != 0 {
		t.Fatalf("empty policy has bindings: %+v", empty)
	}

	set, err := m.SetIamPolicy(context.Background(), name, driver.IAMPolicy{
		Bindings: []driver.IAMBinding{{Role: "roles/cloudtasks.enqueuer", Members: []string{"user:a@b.com"}}},
	})
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == "" || set.Version != 1 {
		t.Fatalf("set policy etag/version = %q/%d", set.Etag, set.Version)
	}

	got, err := m.GetIamPolicy(context.Background(), name)
	if err != nil {
		t.Fatalf("GetIamPolicy after set: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/cloudtasks.enqueuer" {
		t.Fatalf("policy round-trip: %+v", got)
	}

	granted, err := m.TestIamPermissions(context.Background(), name, []string{"cloudtasks.tasks.create"})
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(granted) != 1 || granted[0] != "cloudtasks.tasks.create" {
		t.Fatalf("testIamPermissions echo = %+v", granted)
	}
}

func TestIAMOnMissingQueue(t *testing.T) {
	m := newMock()

	_, err := m.GetIamPolicy(context.Background(), parent+"/queues/nope")
	assertCode(t, err, cerrors.NotFound)
}

func assertCode(t *testing.T, err error, want cerrors.Code) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}

	if got := cerrors.GetCode(err); got != want {
		t.Fatalf("error code = %v, want %v (err: %v)", got, want, err)
	}
}
