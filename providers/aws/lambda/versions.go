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

// latestVersionPublished is the ".PUBLISHED" spelling of $LATEST the DeleteFunction
// Qualifier pattern also accepts; like $LATEST it names the mutable code, so it is
// rejected for version-scoped deletion.
const latestVersionPublished = "$LATEST.PUBLISHED"

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

// DeleteVersion deletes a single published version of a function, matching the
// AWS Lambda DeleteFunction Qualifier semantics: only a numeric published version
// can be removed, deleting $LATEST is rejected, and a version an alias still
// references cannot be removed until the alias is deleted. It backs the AWS
// server handler's version-scoped DeleteFunction (?Qualifier= or a
// "name:qualifier" FunctionName); an unqualified DeleteFunction still removes the
// whole function (all versions and aliases) via DeleteFunction. It has no Azure
// Functions / GCP Cloud Functions equivalent, so it is an AWS-only capability
// asserted by the handler rather than part of the portable Serverless driver.
func (m *Mock) DeleteVersion(_ context.Context, name, qualifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	if qualifier == latestVersion || qualifier == latestVersionPublished {
		return cerrors.New(cerrors.InvalidArgument,
			"$LATEST version cannot be deleted without deleting the function.")
	}

	// A qualifier that names an alias is not a version: real Lambda's
	// DeleteFunction Qualifier deletes only a version, so an alias name is
	// rejected rather than silently deleting the version the alias points at.
	if _, isAlias := fd.aliases.Get(qualifier); isAlias {
		return cerrors.Newf(cerrors.InvalidArgument,
			"DeleteFunction Qualifier %q is an alias, not a version; use DeleteAlias to remove an alias", qualifier)
	}

	if !m.versionExists(&fd, qualifier) {
		return cerrors.Newf(cerrors.NotFound, "function version %s not found", qualifier)
	}

	if alias := aliasReferencing(&fd, qualifier); alias != "" {
		return cerrors.Newf(cerrors.AlreadyExists,
			"version %s cannot be deleted because alias %s references it; delete the alias first", qualifier, alias)
	}

	fd.versions = removeVersionEntry(fd.versions, qualifier)

	// Drop the per-version resource state AWS also removes with the version.
	delete(fd.policies, qualifier)
	delete(fd.urlConfigs, qualifier)
	delete(fd.eventInvokeConfigs, qualifier)
	delete(fd.provisionedConcurrencyConfigs, qualifier)

	m.funcs.Set(name, fd)

	return nil
}

// removeVersionEntry returns a new slice with the entry for the given version
// removed, leaving the input slice's backing array untouched so a concurrent
// reader holding the pre-delete funcData copy is unaffected.
func removeVersionEntry(versions []*versionData, version string) []*versionData {
	next := make([]*versionData, 0, len(versions))

	for _, v := range versions {
		if v.version != version {
			next = append(next, v)
		}
	}

	return next
}

// aliasReferencing returns the name of an alias that still references version —
// either as its primary FunctionVersion or through a weighted RoutingConfig — or
// "" when no alias depends on it. AWS refuses to delete a version an alias points
// at (ResourceConflictException).
func aliasReferencing(fd *funcData, version string) string {
	for name, ad := range fd.aliases.All() {
		ad.mu.Lock()
		referenced := ad.alias.FunctionVersion == version

		if !referenced && ad.alias.RoutingConfig != nil {
			_, referenced = ad.alias.RoutingConfig.AdditionalVersionWeights[version]
		}

		ad.mu.Unlock()

		if referenced {
			return name
		}
	}

	return ""
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
