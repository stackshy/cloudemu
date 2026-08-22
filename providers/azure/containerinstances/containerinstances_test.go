package containerinstances

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// recordingEngine is a config.ContainerEngine that records what the provider
// hands it and returns canned per-container statuses, so the wiring is tested
// without Docker.
type recordingEngine struct {
	ranSpec  config.ContainerRunSpec
	handle   string
	statuses []config.ContainerStatus
	logs     string
	stopped  []string
	logCalls int
	runCount int
}

func (e *recordingEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	e.ranSpec = spec
	e.runCount++

	return e.handle, nil
}

func (e *recordingEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return e.statuses, nil
}

func (e *recordingEngine) Logs(_ context.Context, _, _ string, _ int) (string, error) {
	e.logCalls++

	return e.logs, nil
}

func (e *recordingEngine) Exec(_ context.Context, _, _ string, _ []string) (config.ExecResult, error) {
	return config.ExecResult{}, nil
}

func (e *recordingEngine) Stop(_ context.Context, handle string) error {
	e.stopped = append(e.stopped, handle)

	return nil
}

func sampleConfig() driver.ContainerGroupConfig {
	return driver.ContainerGroupConfig{
		Name:          "cg1",
		Location:      "eastus",
		OSType:        "Linux",
		RestartPolicy: "Never",
		Containers: []driver.ContainerConfig{{
			Name:       "app",
			Image:      "busybox:latest",
			Command:    []string{"echo", "hi"},
			CPU:        1,
			MemoryInGB: 1.5,
			Env:        []driver.EnvVar{{Name: "FOO", Value: "bar"}},
		}},
		Scope: scope.Scope{Subscription: "sub1", ResourceGroup: "rg1"},
	}
}

func TestCreateRunsContainersOnEngine(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 7}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	group, err := m.CreateContainerGroup(context.Background(), sampleConfig())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The engine received the container spec, run-to-completion because Never.
	if !eng.ranSpec.RunToCompletion {
		t.Fatalf("Never restart policy should run to completion")
	}

	if len(eng.ranSpec.Containers) != 1 || eng.ranSpec.Containers[0].Image != "busybox:latest" {
		t.Fatalf("engine did not receive the container: %+v", eng.ranSpec.Containers)
	}

	if eng.ranSpec.Containers[0].Env["FOO"] != "bar" {
		t.Fatalf("env not passed to engine: %+v", eng.ranSpec.Containers[0].Env)
	}

	// The real exited state and exit code are reflected into the group.
	c := group.Containers[0]
	if c.Current.State != "Terminated" || !c.Current.HasExitCode || c.Current.ExitCode != 7 {
		t.Fatalf("container state not reflected: %+v", c.Current)
	}

	// All containers exited → the group rolls up to Succeeded.
	if group.State != "Succeeded" {
		t.Fatalf("group state = %q, want Succeeded", group.State)
	}
}

func onFailureConfig() driver.ContainerGroupConfig {
	cfg := sampleConfig()
	cfg.RestartPolicy = restartPolicyOnFailure

	return cfg
}

func TestOnFailurePolicyRestartsUntilExhausted(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 7}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	group, err := m.CreateContainerGroup(context.Background(), onFailureConfig())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// OnFailure runs to completion and re-runs on every non-zero exit until the
	// bound is hit: 1 initial run + maxOnFailureRestarts retries.
	if !eng.ranSpec.RunToCompletion {
		t.Fatalf("OnFailure should run to completion")
	}

	if eng.runCount != 1+maxOnFailureRestarts {
		t.Fatalf("engine Run called %d times, want %d", eng.runCount, 1+maxOnFailureRestarts)
	}

	if len(eng.stopped) != maxOnFailureRestarts {
		t.Fatalf("engine Stop called %d times, want %d", len(eng.stopped), maxOnFailureRestarts)
	}

	// Still failing after exhausting restarts → the group is Failed.
	if group.State != groupStateFailed {
		t.Fatalf("group state = %q, want %q", group.State, groupStateFailed)
	}
}

func TestOnFailurePolicySucceedsWithoutRestart(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 0}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	group, err := m.CreateContainerGroup(context.Background(), onFailureConfig())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A clean first exit needs no restart.
	if eng.runCount != 1 {
		t.Fatalf("engine Run called %d times, want 1", eng.runCount)
	}

	if group.State != groupStateSucceeded {
		t.Fatalf("group state = %q, want %q", group.State, groupStateSucceeded)
	}
}

func TestAlwaysPolicyStaysRunning(t *testing.T) {
	// Even though the container has already exited, an Always group is reported
	// Running — ACI keeps restarting it, so it never reaches a terminal state.
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 0}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	cfg := sampleConfig()
	cfg.RestartPolicy = restartPolicyAlways

	group, err := m.CreateContainerGroup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if eng.ranSpec.RunToCompletion {
		t.Fatalf("Always should run detached, not to completion")
	}

	if group.State != groupStateRunning {
		t.Fatalf("group state = %q, want %q", group.State, groupStateRunning)
	}

	if c := group.Containers[0].Current; c.State != containerStateRunning || c.HasExitCode {
		t.Fatalf("Always container state = %+v, want Running with no exit code", c)
	}
}

func TestEmptyPolicyDefaultsToAlways(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "running"}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	cfg := sampleConfig()
	cfg.RestartPolicy = "" // ACI default is Always

	group, err := m.CreateContainerGroup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if eng.ranSpec.RunToCompletion {
		t.Fatalf("empty policy (Always default) should run detached")
	}

	if group.State != groupStateRunning {
		t.Fatalf("group state = %q, want %q", group.State, groupStateRunning)
	}
}

func TestGetSurfacesEngineState(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "running"}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	cfg := sampleConfig()
	cfg.RestartPolicy = "Always"

	if _, err := m.CreateContainerGroup(context.Background(), cfg); err != nil {
		t.Fatalf("create: %v", err)
	}

	if eng.ranSpec.RunToCompletion {
		t.Fatalf("Always restart policy should run detached")
	}

	got, err := m.GetContainerGroup(context.Background(), "cg1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.Containers[0].Current.State != "Running" {
		t.Fatalf("running state not surfaced: %+v", got.Containers[0].Current)
	}

	if got.Containers[0].Current.HasExitCode {
		t.Fatalf("running container should carry no exit code")
	}

	if got.State != "Running" {
		t.Fatalf("group state = %q, want Running", got.State)
	}
}

func TestContainerLogsReturnEngineOutput(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited"}},
		logs:     "line1\nline2",
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	if _, err := m.CreateContainerGroup(context.Background(), sampleConfig()); err != nil {
		t.Fatalf("create: %v", err)
	}

	out, err := m.ContainerLogs(context.Background(), "cg1", "app", 10)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}

	if out != "line1\nline2" {
		t.Fatalf("logs = %q, want engine output", out)
	}

	if eng.logCalls != 1 {
		t.Fatalf("engine Logs called %d times, want 1", eng.logCalls)
	}
}

func TestDeleteStopsEngineWorkload(t *testing.T) {
	eng := &recordingEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited"}},
	}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))

	if _, err := m.CreateContainerGroup(context.Background(), sampleConfig()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.DeleteContainerGroup(context.Background(), "cg1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if len(eng.stopped) != 1 || eng.stopped[0] != "h1" {
		t.Fatalf("engine workload not stopped: %v", eng.stopped)
	}

	if _, err := m.GetContainerGroup(context.Background(), "cg1"); !cerrors.IsNotFound(err) {
		t.Fatalf("group should be gone after delete, got %v", err)
	}
}

func TestNilEngineStaysSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	group, err := m.CreateContainerGroup(context.Background(), sampleConfig())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// No engine: synthetic Running state, no exit code, group Running.
	c := group.Containers[0]
	if c.Current.State != "Running" || c.Current.HasExitCode {
		t.Fatalf("synthetic container state wrong: %+v", c.Current)
	}

	if group.State != "Running" {
		t.Fatalf("synthetic group state = %q, want Running", group.State)
	}

	// Logs are empty and no engine call is made for a synthetic group.
	out, err := m.ContainerLogs(context.Background(), "cg1", "app", 0)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}

	if out != "" {
		t.Fatalf("synthetic logs = %q, want empty", out)
	}
}

func TestListFiltersByScope(t *testing.T) {
	m := New(config.NewOptions())

	if _, err := m.CreateContainerGroup(context.Background(), sampleConfig()); err != nil {
		t.Fatalf("create: %v", err)
	}

	inRG, err := m.ListContainerGroups(context.Background(), scope.Scope{Subscription: "sub1", ResourceGroup: "rg1"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(inRG) != 1 {
		t.Fatalf("expected 1 group in rg1, got %d", len(inRG))
	}

	otherRG, err := m.ListContainerGroups(context.Background(), scope.Scope{Subscription: "sub1", ResourceGroup: "other"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(otherRG) != 0 {
		t.Fatalf("expected 0 groups in other rg, got %d", len(otherRG))
	}
}
