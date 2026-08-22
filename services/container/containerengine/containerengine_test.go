package containerengine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/config"
)

// stubEngine is an in-process config.ContainerEngine that records calls and
// returns canned results, so the shared wiring can be tested without Docker.
type stubEngine struct {
	ranSpec  config.ContainerRunSpec
	handle   string
	statuses []config.ContainerStatus
	runErr   error
	statErr  error
	logs     string
	execRes  config.ExecResult
	stopped  []string
	execName string
	execCmd  []string
}

//nolint:gocritic // spec is the by-value DTO defined by the ContainerEngine contract.
func (s *stubEngine) Run(_ context.Context, spec config.ContainerRunSpec) (string, error) {
	s.ranSpec = spec

	return s.handle, s.runErr
}

func (s *stubEngine) Status(_ context.Context, _ string) ([]config.ContainerStatus, error) {
	return s.statuses, s.statErr
}

func (s *stubEngine) Logs(_ context.Context, _, _ string, _ int) (string, error) {
	return s.logs, nil
}

func (s *stubEngine) Exec(_ context.Context, _, container string, cmd []string) (config.ExecResult, error) {
	s.execName = container
	s.execCmd = cmd

	return s.execRes, nil
}

func (s *stubEngine) Stop(_ context.Context, handle string) error {
	s.stopped = append(s.stopped, handle)

	return nil
}

func TestRunNilEngineIsNoop(t *testing.T) {
	handle, statuses, err := Run(context.Background(), nil, config.ContainerRunSpec{Name: "t"})
	require.NoError(t, err)
	assert.Empty(t, handle)
	assert.Nil(t, statuses)
}

func TestRunPassesSpecAndReturnsStatus(t *testing.T) {
	eng := &stubEngine{
		handle:   "h1",
		statuses: []config.ContainerStatus{{Name: "app", State: "exited", ExitCode: 3}},
	}

	handle, statuses, err := Run(context.Background(), eng, config.ContainerRunSpec{
		Name:            "task",
		Containers:      []config.ContainerSpec{{Name: "app", Image: "img"}},
		RunToCompletion: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "h1", handle)
	require.Len(t, statuses, 1)
	assert.Equal(t, 3, statuses[0].ExitCode)
	assert.True(t, eng.ranSpec.RunToCompletion)
	assert.Equal(t, "img", eng.ranSpec.Containers[0].Image)
}

func TestRunSurfacesRunError(t *testing.T) {
	eng := &stubEngine{runErr: errors.New("boom")}

	handle, _, err := Run(context.Background(), eng, config.ContainerRunSpec{})
	require.Error(t, err)
	assert.Empty(t, handle)
}

func TestStatusNilEngineOrHandle(t *testing.T) {
	got, err := Status(context.Background(), nil, "h")
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = Status(context.Background(), &stubEngine{}, "")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestLogsNoop(t *testing.T) {
	got, err := Logs(context.Background(), nil, "h", "c", 0)
	require.NoError(t, err)
	assert.Empty(t, got)

	eng := &stubEngine{logs: "line1\nline2"}
	got, err = Logs(context.Background(), eng, "h", "c", 0)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2", got)
}

func TestExecRoutesToEngine(t *testing.T) {
	eng := &stubEngine{execRes: config.ExecResult{Stdout: "ok", ExitCode: 0}}

	res, err := Exec(context.Background(), eng, "h", "app", []string{"ls", "-la"})
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Stdout)
	assert.Equal(t, "app", eng.execName)
	assert.Equal(t, []string{"ls", "-la"}, eng.execCmd)
}

func TestExecNoop(t *testing.T) {
	res, err := Exec(context.Background(), nil, "h", "app", []string{"ls"})
	require.NoError(t, err)
	assert.Equal(t, config.ExecResult{}, res)
}

func TestStopDelegatesAndNoops(t *testing.T) {
	assert.NoError(t, Stop(context.Background(), nil, "h"))

	eng := &stubEngine{}
	require.NoError(t, Stop(context.Background(), eng, "h1"))
	assert.Equal(t, []string{"h1"}, eng.stopped)

	require.NoError(t, Stop(context.Background(), eng, ""))
	assert.Len(t, eng.stopped, 1)
}
