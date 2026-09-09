package scheduler_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/scheduler"
	"github.com/stackshy/cloudemu/v2/services/scheduler/driver"
)

const parent = "projects/demo/locations/us-central1"

func newMock() *scheduler.Mock {
	return scheduler.New(config.NewOptions())
}

func httpCfg(name string) driver.JobConfig {
	return driver.JobConfig{
		Name:       name,
		Schedule:   "* * * * *",
		HTTPTarget: &driver.HTTPTarget{URI: "https://e.example", HTTPMethod: "POST"},
	}
}

func TestCreateDefaults(t *testing.T) {
	m := newMock()

	job, err := m.CreateJob(context.Background(), httpCfg(parent+"/jobs/a"))
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if job.State != driver.StateEnabled {
		t.Fatalf("state = %q, want ENABLED", job.State)
	}

	if job.AttemptDeadline != "180s" {
		t.Fatalf("attemptDeadline = %q, want 180s", job.AttemptDeadline)
	}

	// A job with no configured retry policy carries no retryConfig, so clients
	// don't see a default block they want to remove.
	if job.RetryConfig != nil {
		t.Fatalf("retryConfig = %+v, want nil for an unconfigured policy", job.RetryConfig)
	}

	// System headers are injected so the headers map is always present.
	if job.HTTPTarget.Headers[headerUserAgentKey] != "Google-Cloud-Scheduler" {
		t.Fatalf("User-Agent header = %q, want Google-Cloud-Scheduler", job.HTTPTarget.Headers[headerUserAgentKey])
	}
}

const headerUserAgentKey = "User-Agent"

// TestRetryConfigSubDefaults confirms a partially-set retry policy (only
// retryCount) has its computed sub-fields filled.
func TestRetryConfigSubDefaults(t *testing.T) {
	m := newMock()

	cfg := httpCfg(parent + "/jobs/rc")
	cfg.RetryConfig = &driver.RetryConfig{RetryCount: 4}

	job, err := m.CreateJob(context.Background(), cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	rc := job.RetryConfig
	if rc == nil || rc.RetryCount != 4 || rc.MinBackoffDuration != "5s" ||
		rc.MaxBackoffDuration != "3600s" || rc.MaxDoublings != 5 {
		t.Fatalf("retry sub-defaults not filled: %+v", rc)
	}
}

func TestCreateDuplicate(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, httpCfg(parent+"/jobs/dup")); err != nil {
		t.Fatalf("first CreateJob: %v", err)
	}

	_, err := m.CreateJob(ctx, httpCfg(parent+"/jobs/dup"))
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("second CreateJob err = %v, want AlreadyExists", err)
	}
}

func TestGetNotFound(t *testing.T) {
	m := newMock()

	_, err := m.GetJob(context.Background(), parent+"/jobs/nope")
	if !cerrors.IsNotFound(err) {
		t.Fatalf("GetJob err = %v, want NotFound", err)
	}
}

func TestListScopedToParent(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, _ = m.CreateJob(ctx, httpCfg(parent+"/jobs/b"))
	_, _ = m.CreateJob(ctx, httpCfg(parent+"/jobs/a"))
	_, _ = m.CreateJob(ctx, httpCfg("projects/demo/locations/europe-west1/jobs/z"))

	jobs, err := m.ListJobs(ctx, parent)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}

	if len(jobs) != 2 {
		t.Fatalf("len = %d, want 2 (scoped to %s)", len(jobs), parent)
	}

	// Deterministic name order.
	if jobs[0].Name != parent+"/jobs/a" || jobs[1].Name != parent+"/jobs/b" {
		t.Fatalf("order = %q, %q", jobs[0].Name, jobs[1].Name)
	}
}

func TestPatchMaskReplacesOnlyMaskedFields(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	name := parent + "/jobs/p"

	cfg := httpCfg(name)
	cfg.Description = "orig"
	cfg.RetryConfig = &driver.RetryConfig{RetryCount: 1}

	if _, err := m.CreateJob(ctx, cfg); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	patch := driver.JobConfig{
		Name:        name,
		Schedule:    "30 2 * * *",
		Description: "IGNORED",
		RetryConfig: &driver.RetryConfig{RetryCount: 7},
	}

	got, err := m.PatchJob(ctx, patch, []string{"schedule", "retryConfig"})
	if err != nil {
		t.Fatalf("PatchJob: %v", err)
	}

	if got.Schedule != "30 2 * * *" {
		t.Fatalf("schedule = %q, want patched", got.Schedule)
	}

	if got.RetryConfig.RetryCount != 7 {
		t.Fatalf("retryCount = %d, want 7", got.RetryConfig.RetryCount)
	}

	// description NOT in mask -> unchanged; httpTarget NOT in mask -> preserved.
	if got.Description != "orig" {
		t.Fatalf("description = %q, want orig (masked out)", got.Description)
	}

	if got.HTTPTarget == nil || got.HTTPTarget.URI != "https://e.example" {
		t.Fatalf("httpTarget lost on masked patch: %+v", got.HTTPTarget)
	}
}

func TestPauseResumeRun(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	name := parent + "/jobs/t"

	if _, err := m.CreateJob(ctx, httpCfg(name)); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	paused, err := m.PauseJob(ctx, name)
	if err != nil || paused.State != driver.StatePaused {
		t.Fatalf("PauseJob = %+v, %v", paused, err)
	}

	resumed, err := m.ResumeJob(ctx, name)
	if err != nil || resumed.State != driver.StateEnabled {
		t.Fatalf("ResumeJob = %+v, %v", resumed, err)
	}

	ran, err := m.RunJob(ctx, name)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	if ran.LastAttemptTime.IsZero() {
		t.Fatalf("RunJob did not set LastAttemptTime")
	}
}

func TestDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	name := parent + "/jobs/d"

	if _, err := m.CreateJob(ctx, httpCfg(name)); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := m.DeleteJob(ctx, name); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	if err := m.DeleteJob(ctx, name); !cerrors.IsNotFound(err) {
		t.Fatalf("second DeleteJob err = %v, want NotFound", err)
	}
}

// TestCloneIsolation confirms the store hands out deep copies: mutating a
// returned job must not affect stored state.
func TestCloneIsolation(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	name := parent + "/jobs/iso"

	cfg := httpCfg(name)
	cfg.RetryConfig = &driver.RetryConfig{RetryCount: 2}

	job, err := m.CreateJob(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	job.HTTPTarget.URI = "https://mutated"
	job.RetryConfig.RetryCount = 99

	got, err := m.GetJob(ctx, name)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.HTTPTarget.URI == "https://mutated" || got.RetryConfig.RetryCount == 99 {
		t.Fatalf("stored job mutated through returned pointer: %+v", got)
	}
}
