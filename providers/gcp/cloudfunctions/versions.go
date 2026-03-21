package cloudfunctions

import (
	"context"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// latestVersion is the symbolic version for the current function code.
const latestVersion = "$LATEST"

// PublishVersion snapshots the current function state as an immutable generation.
func (m *Mock) PublishVersion(_ context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	verNum := fd.nextVersion
	fd.nextVersion++

	verStr := strconv.Itoa(verNum)
	sha := codeSHA(&fd.info)
	now := time.Now().UTC().Format(time.RFC3339)

	vd := &versionData{
		config:    snapshotConfig(&fd.info),
		version:   verStr,
		codeSHA:   sha,
		createdAt: now,
	}
	fd.versions = append(fd.versions, vd)
	m.funcs.Set(functionName, fd)

	return &driver.FunctionVersion{
		FunctionName: functionName,
		Version:      verStr,
		Description:  description,
		CodeSHA256:   sha,
		CreatedAt:    now,
	}, nil
}

// ListVersions returns all published generations for a function.
func (m *Mock) ListVersions(_ context.Context, functionName string) ([]driver.FunctionVersion, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	result := make([]driver.FunctionVersion, 0, len(fd.versions)+1)
	result = append(result, driver.FunctionVersion{
		FunctionName: functionName,
		Version:      latestVersion,
		CodeSHA256:   codeSHA(&fd.info),
	})

	for _, v := range fd.versions {
		result = append(result, driver.FunctionVersion{
			FunctionName: functionName,
			Version:      v.version,
			CodeSHA256:   v.codeSHA,
			CreatedAt:    v.createdAt,
		})
	}

	return result, nil
}

// snapshotConfig creates a FunctionConfig snapshot from a FunctionInfo.
func snapshotConfig(info *driver.FunctionInfo) driver.FunctionConfig {
	env := make(map[string]string, len(info.Environment))
	for k, v := range info.Environment {
		env[k] = v
	}

	tags := make(map[string]string, len(info.Tags))
	for k, v := range info.Tags {
		tags[k] = v
	}

	return driver.FunctionConfig{
		Name:        info.Name,
		Runtime:     info.Runtime,
		Handler:     info.Handler,
		Memory:      info.Memory,
		Timeout:     info.Timeout,
		Environment: env,
		Tags:        tags,
	}
}
