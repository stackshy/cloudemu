package cloudrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
)

// fakeEngine is a recording config.ContainerEngine: it captures the run spec
// and returns canned per-container statuses and logs, so RunJob can be tested
// without Docker.
type fakeEngine struct {
	ranSpecs []config.ContainerRunSpec
	handle   string
	statuses []config.ContainerStatus
	runErr   error
	logs     string
	stopped  []string
}

//nolint:gocritic // spec is the by-value DTO the ContainerEngine contract defines.
func (f *fakeEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	f.ranSpecs = append(f.ranSpecs, spec)

	return f.handle, f.runErr
}

func (f *fakeEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return f.statuses, nil
}

func (f *fakeEngine) Logs(_ context.Context, _, _ string, _ int) (string, error) {
	return f.logs, nil
}

func (f *fakeEngine) Exec(_ context.Context, _, _ string, _ []string) (config.ExecResult, error) {
	return config.ExecResult{}, nil
}

func (f *fakeEngine) Stop(_ context.Context, handle string) error {
	f.stopped = append(f.stopped, handle)

	return nil
}

func newMock(t *testing.T, eng config.ContainerEngine) *Mock {
	t.Helper()

	opts := config.NewOptions()
	opts.ContainerEngine = eng

	return New(opts)
}

func jobCfg() driver.JobConfig {
	return driver.JobConfig{
		Name: "batch",
		Containers: []driver.Container{{
			Name:    "worker",
			Image:   "busybox",
			Command: []string{"echo"},
			Args:    []string{"hi"},
			Env:     map[string]string{"K": "V"},
		}},
	}
}

func TestCreateGetListDeleteJob(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	job, err := m.CreateJob(ctx, jobCfg())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if job.Name != "batch" || job.UID == "" || job.Generation != 1 || job.TaskCount != 1 {
		t.Fatalf("unexpected job: %+v", job)
	}

	if _, err := m.CreateJob(ctx, jobCfg()); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	got, err := m.GetJob(ctx, "projects/p/locations/l/jobs/batch")
	if err != nil || got.Name != "batch" {
		t.Fatalf("GetJob by full name: got=%+v err=%v", got, err)
	}

	jobs, err := m.ListJobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("ListJobs: got %d err=%v", len(jobs), err)
	}

	if err := m.DeleteJob(ctx, "batch"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	if _, err := m.GetJob(ctx, "batch"); !cerrors.IsNotFound(err) {
		t.Fatalf("GetJob after delete err = %v, want NotFound", err)
	}
}

func TestRunJobWithEngineRunsContainersAndSucceeds(t *testing.T) {
	eng := &fakeEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "worker", State: "exited", ExitCode: 0}},
	}
	m := newMock(t, eng)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	exec, err := m.RunJob(ctx, "batch")
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	// The engine actually ran the job's containers, RunToCompletion, with the
	// command+args concatenated and env forwarded.
	if len(eng.ranSpecs) != 1 {
		t.Fatalf("engine Run called %d times, want 1", len(eng.ranSpecs))
	}

	spec := eng.ranSpecs[0]
	if !spec.RunToCompletion {
		t.Fatalf("spec.RunToCompletion = false, want true")
	}

	if got := spec.Containers[0]; got.Image != "busybox" ||
		len(got.Command) != 2 || got.Command[0] != "echo" || got.Command[1] != "hi" ||
		got.Env["K"] != "V" {
		t.Fatalf("engine container spec = %+v", got)
	}

	if exec.SucceededCount != 1 || exec.FailedCount != 0 {
		t.Fatalf("counts: succeeded=%d failed=%d", exec.SucceededCount, exec.FailedCount)
	}

	if len(exec.Tasks) != 1 || exec.Tasks[0].State != taskSucceeded {
		t.Fatalf("tasks = %+v", exec.Tasks)
	}

	if exec.Conditions[0].State != stateSucceeded {
		t.Fatalf("condition = %+v", exec.Conditions)
	}

	// GetExecution returns the finished execution by full name.
	full := "projects/p/locations/l/jobs/batch/executions/" + exec.Name
	if got, err := m.GetExecution(ctx, full); err != nil || got.Name != exec.Name {
		t.Fatalf("GetExecution: got=%+v err=%v", got, err)
	}
}

func TestRunJobReflectsContainerFailure(t *testing.T) {
	eng := &fakeEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "worker", State: "exited", ExitCode: 7}},
		logs:     "boom: bad input",
	}
	m := newMock(t, eng)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	exec, err := m.RunJob(ctx, "batch")
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	if exec.FailedCount != 1 || exec.SucceededCount != 0 {
		t.Fatalf("counts: succeeded=%d failed=%d", exec.SucceededCount, exec.FailedCount)
	}

	if exec.Tasks[0].State != taskFailed || exec.Tasks[0].ExitCode != 7 {
		t.Fatalf("task = %+v", exec.Tasks[0])
	}

	cond := exec.Conditions[0]
	if cond.State != stateFailed {
		t.Fatalf("condition state = %q", cond.State)
	}

	// The captured container output is surfaced into the failure message.
	if cond.Message == "" || !strings.Contains(cond.Message, "boom: bad input") {
		t.Fatalf("condition message = %q, want captured logs", cond.Message)
	}
}

func TestRunJobEngineRunErrorFailsExecution(t *testing.T) {
	eng := &fakeEngine{runErr: errors.New("no such image")}
	m := newMock(t, eng)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	exec, err := m.RunJob(ctx, "batch")
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	if exec.FailedCount != 1 || exec.Conditions[0].State != stateFailed {
		t.Fatalf("expected failed execution, got %+v", exec)
	}
}

func TestRunJobNilEngineIsSynthetic(t *testing.T) {
	m := newMock(t, nil)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	exec, err := m.RunJob(ctx, "batch")
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	if exec.SucceededCount != 1 || exec.FailedCount != 0 {
		t.Fatalf("synthetic run counts: succeeded=%d failed=%d", exec.SucceededCount, exec.FailedCount)
	}

	if len(exec.Tasks) != 1 || exec.Tasks[0].State != taskSucceeded {
		t.Fatalf("synthetic tasks = %+v", exec.Tasks)
	}
}

func TestDeleteJobStopsEngineWorkloads(t *testing.T) {
	eng := &fakeEngine{
		handle:   "handle-A",
		statuses: []config.ContainerStatus{{Name: "worker", State: "exited", ExitCode: 0}},
	}
	m := newMock(t, eng)
	ctx := context.Background()

	if _, err := m.CreateJob(ctx, jobCfg()); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if _, err := m.RunJob(ctx, "batch"); err != nil {
		t.Fatalf("RunJob: %v", err)
	}

	if err := m.DeleteJob(ctx, "batch"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	if len(eng.stopped) != 1 || eng.stopped[0] != "handle-A" {
		t.Fatalf("engine Stop calls = %v, want [handle-A]", eng.stopped)
	}

	// The execution is gone with the job.
	if _, err := m.ListJobs(ctx); err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
}

func TestRunJobUnknownJob(t *testing.T) {
	m := newMock(t, nil)

	if _, err := m.RunJob(context.Background(), "ghost"); !cerrors.IsNotFound(err) {
		t.Fatalf("RunJob unknown err = %v, want NotFound", err)
	}
}
