// Package containerengine holds the shared wiring that lets any container
// provider (AWS ECS, Azure Container Instances, a Kubernetes data plane) back
// its workloads with an opt-in config.ContainerEngine that runs real
// containers. Keeping the Run/Status/Logs/Exec/Stop glue here means the
// providers can't drift in how they translate between their task specs and the
// engine contract, mirroring services/serverless/funcengine.
package containerengine

import (
	"context"

	"github.com/stackshy/cloudemu/v2/config"
)

// Run starts spec's containers on the engine and returns the opaque handle plus
// the initial per-container status. It is a no-op returning an empty handle when
// no engine is configured, so a provider wired without an engine stays fully
// synthetic. When spec.RunToCompletion is set the engine blocks until the
// containers exit, so the returned status already carries their exit codes.
func Run(
	ctx context.Context, engine config.ContainerEngine, spec config.ContainerRunSpec,
) (handle string, statuses []config.ContainerStatus, err error) {
	if engine == nil {
		return "", nil, nil
	}

	handle, err = engine.Run(ctx, spec)
	if err != nil {
		return "", nil, err
	}

	statuses, err = engine.Status(ctx, handle)
	if err != nil {
		return handle, nil, err
	}

	return handle, statuses, nil
}

// Status reports the current per-container state of an engine-backed workload.
// It is a no-op returning nil when no engine is configured or the handle is
// empty (the workload was never engine-backed).
func Status(ctx context.Context, engine config.ContainerEngine, handle string) ([]config.ContainerStatus, error) {
	if engine == nil || handle == "" {
		return nil, nil
	}

	return engine.Status(ctx, handle)
}

// Logs returns the captured stdout/stderr for one container in an engine-backed
// workload. A non-positive tailLines returns the full log. It is a no-op
// returning an empty string when no engine is configured or the handle is empty.
func Logs(
	ctx context.Context, engine config.ContainerEngine, handle, container string, tailLines int,
) (string, error) {
	if engine == nil || handle == "" {
		return "", nil
	}

	return engine.Logs(ctx, handle, container, tailLines)
}

// Exec runs a command inside one container of an engine-backed workload and
// returns its output and exit code. It is a no-op returning a zero result when
// no engine is configured or the handle is empty; callers gate on engine-backing
// before routing an exec through here.
func Exec(
	ctx context.Context, engine config.ContainerEngine, handle, container string, cmd []string,
) (config.ExecResult, error) {
	if engine == nil || handle == "" {
		return config.ExecResult{}, nil
	}

	return engine.Exec(ctx, handle, container, cmd)
}

// Stop tears down an engine-backed workload's containers. It is a no-op when no
// engine is configured or the handle is empty.
func Stop(ctx context.Context, engine config.ContainerEngine, handle string) error {
	if engine == nil || handle == "" {
		return nil
	}

	return engine.Stop(ctx, handle)
}
