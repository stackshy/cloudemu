package lambda

import (
	"context"
	"maps"
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
// Like real Lambda it fails with ResourceConflictException while the function is
// still Pending creation or an update is InProgress (only observable under
// config.Options.AsyncSettle).
func (m *Mock) PublishVersion(ctx context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	return m.publishVersion(ctx, functionName, description, true)
}

// PublishVersionOnWrite publishes a version as part of the write that just
// changed the function: CreateFunction / UpdateFunctionCode with Publish=true.
// AWS performs that publish atomically with the create/update, so unlike a
// standalone PublishVersion it is not rejected by the Pending/InProgress window
// the write itself opened. It is an AWS-only capability the Lambda server
// handler type-asserts for.
func (m *Mock) PublishVersionOnWrite(ctx context.Context, functionName, description string) (*driver.FunctionVersion, error) {
	return m.publishVersion(ctx, functionName, description, false)
}

func (m *Mock) publishVersion(
	ctx context.Context, functionName, description string, gate bool,
) (*driver.FunctionVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, m.errFunctionNotFound(ctx, functionName, "")
	}

	// A standalone PublishVersion is refused while the function is Pending or an
	// update is InProgress; a code deploy still running in the engine always
	// blocks it.
	if gate {
		if err := m.checkUpdatable(&fd); err != nil {
			return nil, err
		}
	} else if m.engineBusy(functionName) {
		return nil, updateInProgress(fd.info.ARN)
	}

	// AWS Lambda doesn't publish a new version if the function's configuration and
	// code haven't changed since the last version. It returns that existing
	// version instead. Every configuration/code update mints a fresh $LATEST
	// RevisionID, so a last-published version cut from the current $LATEST revision
	// means nothing changed and no new version is created.
	if n := len(fd.versions); n > 0 && fd.versions[n-1].revisionID == fd.info.RevisionID {
		return versionResult(functionName, fd.versions[n-1], description), nil
	}

	verNum := fd.nextVersion
	fd.nextVersion++

	// The PublishVersion Description overrides the function's description; when
	// omitted the version keeps the description the function had when it was cut.
	verDesc := description
	if verDesc == "" {
		verDesc = fd.info.Description
	}

	vd := &versionData{
		config:      snapshotConfig(&fd.info),
		version:     strconv.Itoa(verNum),
		codeSHA:     fd.info.CodeSHA256,
		codeSize:    fd.info.CodeSize,
		revisionID:  fd.info.RevisionID,
		createdAt:   m.opts.Clock.Now().UTC().Format(time.RFC3339),
		description: verDesc,
		awsConfig:   cloneAWSConfig(&fd.awsConfig),
	}
	fd.versions = append(fd.versions, vd)
	m.funcs.Set(functionName, fd)

	return versionResult(functionName, vd, ""), nil
}

// versionResult renders a published version's driver.FunctionVersion, used both
// when a fresh version is cut and when a no-change PublishVersion returns the
// existing version. A non-empty description overrides the stored version
// description, matching PublishVersion's Description parameter.
func versionResult(functionName string, v *versionData, description string) *driver.FunctionVersion {
	out := versionInfo(functionName, v)
	if description != "" {
		out.Description = description
	}

	return &out
}

// versionInfo renders a published version from its frozen snapshot only, never
// from the mutable $LATEST state.
func versionInfo(functionName string, v *versionData) driver.FunctionVersion {
	return driver.FunctionVersion{
		FunctionName: functionName,
		Version:      v.version,
		Description:  v.description,
		CodeSHA256:   v.codeSHA,
		CodeSize:     v.codeSize,
		RevisionID:   v.revisionID,
		CreatedAt:    v.createdAt,
		Runtime:      v.config.Runtime,
		Handler:      v.config.Handler,
		Memory:       v.config.Memory,
		Timeout:      v.config.Timeout,
		Role:         v.config.Role,
		Environment:  maps.Clone(v.config.Environment),
	}
}

// ListVersions returns all published versions for a function.
func (m *Mock) ListVersions(ctx context.Context, functionName string) ([]driver.FunctionVersion, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, m.errFunctionNotFound(ctx, functionName, "")
	}

	result := make([]driver.FunctionVersion, 0, len(fd.versions)+1)
	// $LATEST is always present
	result = append(result, driver.FunctionVersion{
		FunctionName: functionName,
		Version:      latestVersion,
		Description:  fd.info.Description,
		CodeSHA256:   fd.info.CodeSHA256,
		CodeSize:     fd.info.CodeSize,
		RevisionID:   fd.info.RevisionID,
		Runtime:      fd.info.Runtime,
		Handler:      fd.info.Handler,
		Memory:       fd.info.Memory,
		Timeout:      fd.info.Timeout,
		Role:         fd.info.Role,
		Environment:  maps.Clone(fd.info.Environment),
	})

	for _, v := range fd.versions {
		result = append(result, versionInfo(functionName, v))
	}

	return result, nil
}

// GetVersionAWSConfig returns the AWS-only settings (VpcConfig,
// DeadLetterConfig, TracingConfig, Architectures, EphemeralStorage, Layers) of
// one function version: the live settings for "" / $LATEST, or the settings a
// published version froze when it was cut. It is an AWS-only capability the
// Lambda server handler type-asserts for, so a published version reports its own
// configuration rather than whatever $LATEST was changed to afterwards.
func (m *Mock) GetVersionAWSConfig(ctx context.Context, name, version string) (driver.AWSFunctionConfig, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return driver.AWSFunctionConfig{}, m.errFunctionNotFound(ctx, name, "")
	}

	if version == "" || version == latestVersion {
		return cloneAWSConfig(&fd.awsConfig), nil
	}

	for _, v := range fd.versions {
		if v.version == version {
			return cloneAWSConfig(&v.awsConfig), nil
		}
	}

	return driver.AWSFunctionConfig{}, m.errFunctionNotFound(ctx, name, version)
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
func (m *Mock) DeleteVersion(ctx context.Context, name, qualifier string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return m.errFunctionNotFound(ctx, name, "")
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
	dropQualifierState(&fd, qualifier)

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

// aliasReferencing returns the name of an alias that still references version,
// either as its primary FunctionVersion or through a weighted RoutingConfig, or
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
