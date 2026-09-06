package cloudtasks_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	tasks "google.golang.org/api/cloudtasks/v2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const testParent = "projects/demo/locations/us-central1"

func newTasksService(t *testing.T) *tasks.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudTasks: cloud.CloudTasks})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := tasks.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudtasks.NewService: %v", err)
	}

	return svc
}

func TestSDKQueueLifecycleAndDefaults(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/emails"

	created, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{
		Name: name,
		RateLimits: &tasks.RateLimits{
			MaxDispatchesPerSecond:  100,
			MaxConcurrentDispatches: 20,
		},
		RetryConfig: &tasks.RetryConfig{MaxAttempts: 5, MaxBackoff: "10s"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if created.Name != name {
		t.Fatalf("name = %q, want %q", created.Name, name)
	}

	if created.State != "RUNNING" {
		t.Fatalf("state = %q, want RUNNING", created.State)
	}

	// maxBurstSize is output-only + computed; user rate values are preserved.
	if created.RateLimits.MaxDispatchesPerSecond != 100 || created.RateLimits.MaxConcurrentDispatches != 20 {
		t.Fatalf("rateLimits not preserved: %+v", created.RateLimits)
	}

	if created.RateLimits.MaxBurstSize == 0 {
		t.Fatalf("maxBurstSize not computed: %+v", created.RateLimits)
	}

	// retryConfig is fully populated: user values kept, the rest defaulted.
	rc := created.RetryConfig
	if rc.MaxAttempts != 5 || rc.MaxBackoff != "10s" {
		t.Fatalf("retryConfig user values lost: %+v", rc)
	}

	if rc.MinBackoff != "0.100s" || rc.MaxDoublings != 16 || rc.MaxRetryDuration != "0s" {
		t.Fatalf("retryConfig defaults not filled: %+v", rc)
	}

	got, err := svc.Projects.Locations.Queues.Get(name).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.RateLimits.MaxBurstSize != created.RateLimits.MaxBurstSize {
		t.Fatalf("maxBurstSize not stable on Get: %d vs %d", got.RateLimits.MaxBurstSize, created.RateLimits.MaxBurstSize)
	}
}

func TestSDKDefaultQueueBurstSize(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/plain"

	created, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{Name: name}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	rl := created.RateLimits
	if rl.MaxDispatchesPerSecond != 500 || rl.MaxConcurrentDispatches != 1000 || rl.MaxBurstSize != 100 {
		t.Fatalf("default rateLimits = %+v, want dps=500 concurrent=1000 burst=100", rl)
	}
}

func TestSDKPatchUpdateMask(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/patchme"

	if _, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{
		Name:        name,
		RetryConfig: &tasks.RetryConfig{MaxAttempts: 3},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	patched, err := svc.Projects.Locations.Queues.Patch(name, &tasks.Queue{
		RetryConfig: &tasks.RetryConfig{MaxAttempts: 9, MaxBackoff: "42s"},
	}).UpdateMask("retryConfig").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if patched.RetryConfig.MaxAttempts != 9 || patched.RetryConfig.MaxBackoff != "42s" {
		t.Fatalf("retryConfig not patched: %+v", patched.RetryConfig)
	}

	// rateLimits was not in the mask, so its create-time defaults survive.
	if patched.RateLimits == nil || patched.RateLimits.MaxDispatchesPerSecond != 500 {
		t.Fatalf("rateLimits lost across masked patch: %+v", patched.RateLimits)
	}
}

func TestSDKPauseResumePurge(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/togglable"

	if _, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{Name: name}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	paused, err := svc.Projects.Locations.Queues.Pause(name, &tasks.PauseQueueRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	if paused.State != "PAUSED" {
		t.Fatalf("state after pause = %q, want PAUSED", paused.State)
	}

	resumed, err := svc.Projects.Locations.Queues.Resume(name, &tasks.ResumeQueueRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if resumed.State != "RUNNING" {
		t.Fatalf("state after resume = %q, want RUNNING", resumed.State)
	}

	purged, err := svc.Projects.Locations.Queues.Purge(name, &tasks.PurgeQueueRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}

	if purged.PurgeTime == "" {
		t.Fatalf("purgeTime empty after purge")
	}
}

func TestSDKListScoped(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{
		Name: testParent + "/queues/a",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create a: %v", err)
	}

	list, err := svc.Projects.Locations.Queues.List(testParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Queues) != 1 || list.Queues[0].Name != testParent+"/queues/a" {
		t.Fatalf("list = %d queues, want 1 (a)", len(list.Queues))
	}
}

func TestSDKIAMPolicy(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/iam"

	if _, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{Name: name}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	set, err := svc.Projects.Locations.Queues.SetIamPolicy(name, &tasks.SetIamPolicyRequest{
		Policy: &tasks.Policy{
			Bindings: []*tasks.Binding{{
				Role:    "roles/cloudtasks.enqueuer",
				Members: []string{"user:a@b.com"},
			}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetIamPolicy: %v", err)
	}

	if set.Etag == "" {
		t.Fatalf("setIamPolicy returned no etag")
	}

	got, err := svc.Projects.Locations.Queues.GetIamPolicy(name, &tasks.GetIamPolicyRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("GetIamPolicy: %v", err)
	}

	if len(got.Bindings) != 1 || got.Bindings[0].Role != "roles/cloudtasks.enqueuer" {
		t.Fatalf("policy round-trip: %+v", got.Bindings)
	}

	test, err := svc.Projects.Locations.Queues.TestIamPermissions(name, &tasks.TestIamPermissionsRequest{
		Permissions: []string{"cloudtasks.tasks.create"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("TestIamPermissions: %v", err)
	}

	if len(test.Permissions) != 1 || test.Permissions[0] != "cloudtasks.tasks.create" {
		t.Fatalf("testIamPermissions = %+v", test.Permissions)
	}
}

func TestSDKDeleteThenGet404(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()
	name := testParent + "/queues/ephemeral"

	if _, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{Name: name}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Queues.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := svc.Projects.Locations.Queues.Get(name).Context(ctx).Do()
	assertGoogleErr(t, err, 404)
}

func TestSDKDuplicateCreate409(t *testing.T) {
	svc := newTasksService(t)
	ctx := context.Background()

	mk := func() error {
		_, err := svc.Projects.Locations.Queues.Create(testParent, &tasks.Queue{
			Name: testParent + "/queues/dup",
		}).Context(ctx).Do()

		return err
	}

	if err := mk(); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	assertGoogleErr(t, mk(), 409)
}

func assertGoogleErr(t *testing.T, err error, wantCode int) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error with code %d, got nil", wantCode)
	}

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		t.Fatalf("error %v is not a *googleapi.Error", err)
	}

	if gerr.Code != wantCode {
		t.Fatalf("error code = %d, want %d", gerr.Code, wantCode)
	}
}
