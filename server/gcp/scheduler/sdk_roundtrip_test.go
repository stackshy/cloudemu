package scheduler_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	sched "google.golang.org/api/cloudscheduler/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const testParent = "projects/demo/locations/us-central1"

func newSchedulerService(t *testing.T) *sched.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Scheduler: cloud.Scheduler})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := sched.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudscheduler.NewService: %v", err)
	}

	return svc
}

func b64(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func TestSDKHTTPTargetLifecycle(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()

	created, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:     testParent + "/jobs/nightly",
		Schedule: "*/5 * * * *",
		TimeZone: "America/New_York",
		HttpTarget: &sched.HttpTarget{
			Uri:        "https://example.com/hook",
			HttpMethod: "POST",
			Body:       b64("hello"),
			Headers:    map[string]string{"X-Test": "1"},
		},
		RetryConfig: &sched.RetryConfig{RetryCount: 3},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantName := testParent + "/jobs/nightly"
	if created.Name != wantName {
		t.Fatalf("name = %q, want %q", created.Name, wantName)
	}

	// Defaults-on-create: state ENABLED, retry + attemptDeadline defaults.
	if created.State != "ENABLED" {
		t.Fatalf("state = %q, want ENABLED", created.State)
	}

	if created.AttemptDeadline != "180s" {
		t.Fatalf("attemptDeadline = %q, want 180s", created.AttemptDeadline)
	}

	if created.RetryConfig.MinBackoffDuration != "5s" || created.RetryConfig.MaxBackoffDuration != "3600s" ||
		created.RetryConfig.MaxDoublings != 5 {
		t.Fatalf("retry defaults not filled: %+v", created.RetryConfig)
	}

	got, err := svc.Projects.Locations.Jobs.Get(wantName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Full round-trip of schedule / timeZone / target / retry / state.
	if got.Schedule != "*/5 * * * *" || got.TimeZone != "America/New_York" {
		t.Fatalf("schedule/tz round-trip: %q %q", got.Schedule, got.TimeZone)
	}

	if got.HttpTarget.Uri != "https://example.com/hook" || got.HttpTarget.HttpMethod != "POST" {
		t.Fatalf("httpTarget round-trip: %+v", got.HttpTarget)
	}

	if got.HttpTarget.Body != b64("hello") {
		t.Fatalf("body base64 = %q, want %q", got.HttpTarget.Body, b64("hello"))
	}

	if got.HttpTarget.Headers["X-Test"] != "1" {
		t.Fatalf("headers = %v", got.HttpTarget.Headers)
	}

	if got.RetryConfig.RetryCount != 3 {
		t.Fatalf("retryCount = %d, want 3", got.RetryConfig.RetryCount)
	}
}

func TestSDKPatchUpdateMask(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()
	name := testParent + "/jobs/patchme"

	_, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:        name,
		Schedule:    "0 * * * *",
		HttpTarget:  &sched.HttpTarget{Uri: "https://a.example/x", HttpMethod: "GET"},
		RetryConfig: &sched.RetryConfig{RetryCount: 1},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Patch schedule + body + retryCount, masked. httpTarget.uri is NOT in the
	// mask, so it must survive the retarget.
	patched, err := svc.Projects.Locations.Jobs.Patch(name, &sched.Job{
		Schedule:    "30 2 * * *",
		HttpTarget:  &sched.HttpTarget{Uri: "https://a.example/x", HttpMethod: "GET", Body: b64("v2")},
		RetryConfig: &sched.RetryConfig{RetryCount: 9},
	}).UpdateMask("schedule,httpTarget,retryConfig").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if patched.Schedule != "30 2 * * *" {
		t.Fatalf("schedule = %q, want 30 2 * * *", patched.Schedule)
	}

	if patched.HttpTarget.Body != b64("v2") {
		t.Fatalf("body = %q, want %q", patched.HttpTarget.Body, b64("v2"))
	}

	if patched.RetryConfig.RetryCount != 9 {
		t.Fatalf("retryCount = %d, want 9", patched.RetryConfig.RetryCount)
	}

	// A masked-out field (description) is untouched.
	if patched.Description != "" {
		t.Fatalf("description = %q, want empty", patched.Description)
	}
}

func TestSDKPauseResumeRun(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()
	name := testParent + "/jobs/togglable"

	if _, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:       name,
		Schedule:   "* * * * *",
		HttpTarget: &sched.HttpTarget{Uri: "https://x.example/y"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	paused, err := svc.Projects.Locations.Jobs.Pause(name, &sched.PauseJobRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}

	if paused.State != "PAUSED" {
		t.Fatalf("state after pause = %q, want PAUSED", paused.State)
	}

	resumed, err := svc.Projects.Locations.Jobs.Resume(name, &sched.ResumeJobRequest{}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}

	if resumed.State != "ENABLED" {
		t.Fatalf("state after resume = %q, want ENABLED", resumed.State)
	}

	if _, err := svc.Projects.Locations.Jobs.Run(name, &sched.RunJobRequest{}).Context(ctx).Do(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestSDKPubsubTargetAndList(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:     testParent + "/jobs/pub",
		Schedule: "0 0 * * *",
		PubsubTarget: &sched.PubsubTarget{
			TopicName:  "projects/demo/topics/events",
			Data:       b64("payload"),
			Attributes: map[string]string{"k": "v"},
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create pubsub: %v", err)
	}

	got, err := svc.Projects.Locations.Jobs.Get(testParent + "/jobs/pub").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.PubsubTarget == nil || got.PubsubTarget.TopicName != "projects/demo/topics/events" {
		t.Fatalf("pubsubTarget round-trip: %+v", got.PubsubTarget)
	}

	if got.PubsubTarget.Data != b64("payload") || got.PubsubTarget.Attributes["k"] != "v" {
		t.Fatalf("pubsub data/attrs round-trip: %+v", got.PubsubTarget)
	}

	list, err := svc.Projects.Locations.Jobs.List(testParent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list.Jobs) != 1 || list.Jobs[0].Name != testParent+"/jobs/pub" {
		t.Fatalf("list = %d jobs, want 1 (pub)", len(list.Jobs))
	}
}

func TestSDKDeleteThenGet404(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()
	name := testParent + "/jobs/ephemeral"

	if _, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:       name,
		Schedule:   "* * * * *",
		HttpTarget: &sched.HttpTarget{Uri: "https://z.example"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Jobs.Delete(name).Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := svc.Projects.Locations.Jobs.Get(name).Context(ctx).Do()
	assertGoogleErr(t, err, 404)
}

func TestSDKDuplicateCreate409(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()

	mk := func() error {
		_, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
			Name:       testParent + "/jobs/dup",
			Schedule:   "* * * * *",
			HttpTarget: &sched.HttpTarget{Uri: "https://dup.example"},
		}).Context(ctx).Do()

		return err
	}

	if err := mk(); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	assertGoogleErr(t, mk(), 409)
}

func TestSDKMissingTarget400(t *testing.T) {
	svc := newSchedulerService(t)
	ctx := context.Background()

	_, err := svc.Projects.Locations.Jobs.Create(testParent, &sched.Job{
		Name:     testParent + "/jobs/notarget",
		Schedule: "* * * * *",
	}).Context(ctx).Do()
	assertGoogleErr(t, err, 400)
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
