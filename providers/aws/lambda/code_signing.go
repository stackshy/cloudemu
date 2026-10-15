package lambda

import (
	"context"
	"maps"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Code signing config constraints from the Lambda API model
// (CreateCodeSigningConfig / AllowedPublishers / CodeSigningPolicies). See
// https://docs.aws.amazon.com/lambda/latest/api/API_CreateCodeSigningConfig.html
const (
	codeSigningPolicyWarn    = "Warn"
	codeSigningPolicyEnforce = "Enforce"

	minSigningProfiles      = 1
	maxSigningProfiles      = 20
	maxCodeSigningDescLen   = 256
	codeSigningConfigPrefix = "csc-"
	// codeSigningTimeFormat is the ISO-8601 LastModified shape Lambda uses
	// (YYYY-MM-DDThh:mm:ss.sTZD).
	codeSigningTimeFormat = "2006-01-02T15:04:05.000-0700"
)

// The Mock methods below are the AWS-only code signing surface the Lambda server
// handler type-asserts for: code signing configs have no Azure Functions or GCP
// Cloud Functions equivalent. cloudemu stores and associates the configs but
// does not verify deployment-package signatures.

// CreateCodeSigningConfig creates a code signing config. UntrustedArtifactOnDeployment
// defaults to "Warn", as in AWS.
//
//nolint:gocritic // hugeParam: value mirrors the create request.
func (m *Mock) CreateCodeSigningConfig(ctx context.Context, cfg driver.CodeSigningConfig) (*driver.CodeSigningConfig, error) {
	if cfg.UntrustedArtifactOnDeployment == "" {
		cfg.UntrustedArtifactOnDeployment = codeSigningPolicyWarn
	}

	if err := validateCodeSigningConfig(cfg.Description, cfg.SigningProfileVersionARNs,
		cfg.UntrustedArtifactOnDeployment); err != nil {
		return nil, err
	}

	id, err := idgen.GenerateLongID(codeSigningConfigPrefix)
	if err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "generate code signing config id: %v", err)
	}

	out := driver.CodeSigningConfig{
		ID: id,
		ARN: idgen.AWSARN("lambda", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID,
			"code-signing-config:"+id),
		Description:                   cfg.Description,
		SigningProfileVersionARNs:     append([]string(nil), cfg.SigningProfileVersionARNs...),
		UntrustedArtifactOnDeployment: cfg.UntrustedArtifactOnDeployment,
		LastModified:                  m.opts.Clock.Now().UTC().Format(codeSigningTimeFormat),
		Tags:                          maps.Clone(cfg.Tags),
	}

	m.cscs.Set(out.ARN, out)

	return cloneCodeSigningConfig(&out), nil
}

// GetCodeSigningConfig returns the code signing config with the given ARN.
func (m *Mock) GetCodeSigningConfig(_ context.Context, arn string) (*driver.CodeSigningConfig, error) {
	c, ok := m.cscs.Get(arn)
	if !ok {
		return nil, errCodeSigningConfigNotFound(arn)
	}

	return cloneCodeSigningConfig(&c), nil
}

// ListCodeSigningConfigs returns every code signing config, sorted by ARN so
// paginated listings stay stable.
func (m *Mock) ListCodeSigningConfigs(_ context.Context) ([]driver.CodeSigningConfig, error) {
	all := m.cscs.All()
	out := make([]driver.CodeSigningConfig, 0, len(all))

	for _, c := range all {
		out = append(out, *cloneCodeSigningConfig(&c))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ARN < out[j].ARN })

	return out, nil
}

// UpdateCodeSigningConfig applies the non-nil fields of upd to a code signing config.
func (m *Mock) UpdateCodeSigningConfig(
	_ context.Context, arn string, upd driver.CodeSigningConfigUpdate,
) (*driver.CodeSigningConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.cscs.Get(arn)
	if !ok {
		return nil, errCodeSigningConfigNotFound(arn)
	}

	if upd.Description != nil {
		c.Description = *upd.Description
	}

	if upd.SigningProfileVersionARNs != nil {
		c.SigningProfileVersionARNs = append([]string(nil), upd.SigningProfileVersionARNs...)
	}

	if upd.UntrustedArtifactOnDeployment != nil {
		c.UntrustedArtifactOnDeployment = *upd.UntrustedArtifactOnDeployment
	}

	if err := validateCodeSigningConfig(c.Description, c.SigningProfileVersionARNs,
		c.UntrustedArtifactOnDeployment); err != nil {
		return nil, err
	}

	c.LastModified = m.opts.Clock.Now().UTC().Format(codeSigningTimeFormat)
	m.cscs.Set(arn, c)

	return cloneCodeSigningConfig(&c), nil
}

// DeleteCodeSigningConfig deletes a code signing config. AWS allows it "only if
// no function is using it", so a config still attached to a function is a
// ResourceConflictException.
func (m *Mock) DeleteCodeSigningConfig(_ context.Context, arn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.cscs.Get(arn); !ok {
		return errCodeSigningConfigNotFound(arn)
	}

	if len(m.functionsUsingCodeSigningConfig(arn)) > 0 {
		return cerrors.Newf(cerrors.AlreadyExists,
			"Code signing configuration %s is used by one or more functions and cannot be deleted.", arn)
	}

	m.cscs.Delete(arn)

	return nil
}

// PutFunctionCodeSigningConfig attaches a code signing config to a function,
// replacing any config already attached.
func (m *Mock) PutFunctionCodeSigningConfig(ctx context.Context, name, arn string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return m.errFunctionNotFound(ctx, name, "")
	}

	if _, ok := m.cscs.Get(arn); !ok {
		return errCodeSigningConfigNotFound(arn)
	}

	fd.codeSigningConfigARN = arn
	m.funcs.Set(name, fd)

	return nil
}

// GetFunctionCodeSigningConfig returns the ARN of the code signing config
// attached to a function, or "" when none is.
func (m *Mock) GetFunctionCodeSigningConfig(ctx context.Context, name string) (string, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return "", m.errFunctionNotFound(ctx, name, "")
	}

	return fd.codeSigningConfigARN, nil
}

// DeleteFunctionCodeSigningConfig detaches a function's code signing config. A
// function with none attached is left unchanged.
func (m *Mock) DeleteFunctionCodeSigningConfig(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return m.errFunctionNotFound(ctx, name, "")
	}

	fd.codeSigningConfigARN = ""
	m.funcs.Set(name, fd)

	return nil
}

// ListFunctionsByCodeSigningConfig returns the ARNs of the functions a code
// signing config is attached to, sorted.
func (m *Mock) ListFunctionsByCodeSigningConfig(_ context.Context, arn string) ([]string, error) {
	if _, ok := m.cscs.Get(arn); !ok {
		return nil, errCodeSigningConfigNotFound(arn)
	}

	return m.functionsUsingCodeSigningConfig(arn), nil
}

// functionsUsingCodeSigningConfig returns the sorted ARNs of the functions arn
// is attached to.
func (m *Mock) functionsUsingCodeSigningConfig(arn string) []string {
	var out []string

	all := m.funcs.All()
	for name := range all {
		if all[name].codeSigningConfigARN == arn {
			out = append(out, all[name].info.ARN)
		}
	}

	sort.Strings(out)

	return out
}

// validateCodeSigningConfig enforces the API model constraints: 1-20 signing
// profile version ARNs, a policy of Warn or Enforce, and a description of at
// most 256 characters. Violations are InvalidParameterValueException.
func validateCodeSigningConfig(description string, profiles []string, policy string) error {
	if len(profiles) < minSigningProfiles || len(profiles) > maxSigningProfiles {
		return cerrors.Newf(cerrors.InvalidArgument,
			"AllowedPublishers.SigningProfileVersionArns must contain between %d and %d ARNs",
			minSigningProfiles, maxSigningProfiles)
	}

	if policy != codeSigningPolicyWarn && policy != codeSigningPolicyEnforce {
		return cerrors.Newf(cerrors.InvalidArgument,
			"value '%s' at 'codeSigningPolicies.untrustedArtifactOnDeployment' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [%s, %s]", policy, codeSigningPolicyWarn, codeSigningPolicyEnforce)
	}

	if len(description) > maxCodeSigningDescLen {
		return cerrors.Newf(cerrors.InvalidArgument,
			"value at 'description' failed to satisfy constraint: Member must have length less than or equal to %d",
			maxCodeSigningDescLen)
	}

	return nil
}

func errCodeSigningConfigNotFound(arn string) error {
	return cerrors.Newf(cerrors.NotFound, "Code signing configuration not found: %s", arn)
}

func cloneCodeSigningConfig(c *driver.CodeSigningConfig) *driver.CodeSigningConfig {
	out := *c
	out.SigningProfileVersionARNs = append([]string(nil), c.SigningProfileVersionARNs...)
	out.Tags = maps.Clone(c.Tags)

	return &out
}

// TagCodeSigningConfig adds or overwrites tags on a code signing config
// (TagResource on a code signing config ARN).
func (m *Mock) TagCodeSigningConfig(_ context.Context, arn string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.cscs.Get(arn)
	if !ok {
		return errCodeSigningConfigNotFound(arn)
	}

	next := make(map[string]string, len(c.Tags)+len(tags))
	maps.Copy(next, c.Tags)
	maps.Copy(next, tags)
	c.Tags = next
	m.cscs.Set(arn, c)

	return nil
}

// UntagCodeSigningConfig removes tags by key from a code signing config.
func (m *Mock) UntagCodeSigningConfig(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.cscs.Get(arn)
	if !ok {
		return errCodeSigningConfigNotFound(arn)
	}

	next := maps.Clone(c.Tags)
	for _, k := range keys {
		delete(next, k)
	}

	c.Tags = next
	m.cscs.Set(arn, c)

	return nil
}

// ListCodeSigningConfigTags returns a code signing config's tags.
func (m *Mock) ListCodeSigningConfigTags(_ context.Context, arn string) (map[string]string, error) {
	c, ok := m.cscs.Get(arn)
	if !ok {
		return nil, errCodeSigningConfigNotFound(arn)
	}

	return maps.Clone(c.Tags), nil
}
