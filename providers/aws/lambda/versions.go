package lambda

import (
	"context"
	"strconv"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// latestVersion is the symbolic version for the current function code.
const latestVersion = "$LATEST"

// PublishVersion snapshots the current function state as an immutable version.
func (m *Mock) PublishVersion(_ context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	// AWS Lambda doesn't publish a new version if the function's configuration and
	// code haven't changed since the last version — it returns that existing
	// version instead. Every configuration/code update mints a fresh $LATEST
	// RevisionID, so a last-published version cut from the current $LATEST revision
	// means nothing changed and no new version is created.
	if n := len(fd.versions); n > 0 && fd.versions[n-1].revisionID == fd.info.RevisionID {
		return versionResult(functionName, fd.versions[n-1], description), nil
	}

	verNum := fd.nextVersion
	fd.nextVersion++

	verStr := strconv.Itoa(verNum)
	sha := fd.info.CodeSHA256
	rev := fd.info.RevisionID
	now := m.opts.Clock.Now().UTC().Format(time.RFC3339)

	vd := &versionData{
		config:     snapshotConfig(&fd.info),
		version:    verStr,
		codeSHA:    sha,
		revisionID: rev,
		createdAt:  now,
	}
	fd.versions = append(fd.versions, vd)
	m.funcs.Set(functionName, fd)

	return versionResult(functionName, vd, description), nil
}

// versionResult renders a published version's driver.FunctionVersion, used both
// when a fresh version is cut and when a no-change PublishVersion returns the
// existing version. description overrides the stored version description, matching
// PublishVersion's Description parameter.
func versionResult(functionName string, v *versionData, description string) *driver.FunctionVersion {
	return &driver.FunctionVersion{
		FunctionName: functionName,
		Version:      v.version,
		Description:  description,
		CodeSHA256:   v.codeSHA,
		RevisionID:   v.revisionID,
		CreatedAt:    v.createdAt,
		Runtime:      v.config.Runtime,
		Handler:      v.config.Handler,
		Memory:       v.config.Memory,
		Timeout:      v.config.Timeout,
		Role:         v.config.Role,
	}
}

// ListVersions returns all published versions for a function.
func (m *Mock) ListVersions(_ context.Context, functionName string) ([]driver.FunctionVersion, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	result := make([]driver.FunctionVersion, 0, len(fd.versions)+1)
	// $LATEST is always present
	result = append(result, driver.FunctionVersion{
		FunctionName: functionName,
		Version:      latestVersion,
		CodeSHA256:   fd.info.CodeSHA256,
		RevisionID:   fd.info.RevisionID,
		Runtime:      fd.info.Runtime,
		Handler:      fd.info.Handler,
		Memory:       fd.info.Memory,
		Timeout:      fd.info.Timeout,
		Role:         fd.info.Role,
	})

	for _, v := range fd.versions {
		result = append(result, driver.FunctionVersion{
			FunctionName: functionName,
			Version:      v.version,
			CodeSHA256:   v.codeSHA,
			RevisionID:   v.revisionID,
			CreatedAt:    v.createdAt,
			Runtime:      v.config.Runtime,
			Handler:      v.config.Handler,
			Memory:       v.config.Memory,
			Timeout:      v.config.Timeout,
			Role:         v.config.Role,
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
		Role:        info.Role,
		Description: info.Description,
		Memory:      info.Memory,
		Timeout:     info.Timeout,
		Environment: env,
		Tags:        tags,
	}
}
