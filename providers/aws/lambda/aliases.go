package lambda

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// CreateAlias creates a new alias pointing to a specific function version.
func (m *Mock) CreateAlias(_ context.Context, cfg driver.AliasConfig) (*driver.Alias, error) {
	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	if fd.aliases.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "alias %s already exists", cfg.Name)
	}

	if !m.versionExists(&fd, cfg.FunctionVersion) {
		return nil, cerrors.Newf(cerrors.NotFound, "version %s not found", cfg.FunctionVersion)
	}

	if err := m.validateRoutingConfig(&fd, cfg.FunctionVersion, cfg.RoutingConfig); err != nil {
		return nil, err
	}

	aliasARN := idgen.AWSARN(
		"lambda", arnRegion(fd.info.ARN, m.opts.Region), m.opts.AccountID,
		"function:"+cfg.FunctionName+":"+cfg.Name,
	)

	a := driver.Alias{
		FunctionName:    cfg.FunctionName,
		Name:            cfg.Name,
		FunctionVersion: cfg.FunctionVersion,
		Description:     cfg.Description,
		RoutingConfig:   copyRoutingConfig(cfg.RoutingConfig),
		AliasARN:        aliasARN,
		CreatedAt:       m.opts.Clock.Now().UTC().Format(time.RFC3339),
		RevisionID:      newRevisionID(),
	}

	fd.aliases.Set(cfg.Name, &aliasData{alias: a})

	result := a

	return &result, nil
}

// UpdateAlias updates an existing alias configuration.
func (m *Mock) UpdateAlias(_ context.Context, cfg driver.AliasConfig) (*driver.Alias, error) {
	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	ad, ok := fd.aliases.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alias %s not found", cfg.Name)
	}

	// Compute the prospective effective FunctionVersion without touching the
	// live alias yet. UpdateAlias is atomic in real AWS: if any validation
	// fails the alias must be left completely unchanged.
	effectiveVersion := ad.alias.FunctionVersion

	if cfg.FunctionVersion != "" {
		if !m.versionExists(&fd, cfg.FunctionVersion) {
			return nil, cerrors.Newf(cerrors.NotFound, "version %s not found", cfg.FunctionVersion)
		}

		effectiveVersion = cfg.FunctionVersion
	}

	if cfg.RoutingConfig != nil {
		// Validate the routing config against the prospective FunctionVersion,
		// which is the effective primary target for this alias.
		if err := m.validateRoutingConfig(&fd, effectiveVersion, cfg.RoutingConfig); err != nil {
			return nil, err
		}
	}

	// All validation passed — commit the changes to the live alias.
	ad.alias.FunctionVersion = effectiveVersion

	if cfg.Description != "" {
		ad.alias.Description = cfg.Description
	}

	if cfg.RoutingConfig != nil {
		ad.alias.RoutingConfig = copyRoutingConfig(cfg.RoutingConfig)
	}

	ad.alias.RevisionID = newRevisionID()

	fd.aliases.Set(cfg.Name, ad)

	result := ad.alias

	return &result, nil
}

// DeleteAlias removes an alias from a function.
func (m *Mock) DeleteAlias(_ context.Context, functionName, aliasName string) error {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	if !fd.aliases.Has(aliasName) {
		return cerrors.Newf(cerrors.NotFound, "alias %s not found", aliasName)
	}

	fd.aliases.Delete(aliasName)

	return nil
}

// GetAlias retrieves a specific alias for a function.
func (m *Mock) GetAlias(_ context.Context, functionName, aliasName string) (*driver.Alias, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	ad, ok := fd.aliases.Get(aliasName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alias %s not found", aliasName)
	}

	result := ad.alias

	return &result, nil
}

// ListAliases returns all aliases for a function.
func (m *Mock) ListAliases(_ context.Context, functionName string) ([]driver.Alias, error) {
	fd, ok := m.funcs.Get(functionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", functionName)
	}

	all := fd.aliases.All()
	aliases := make([]driver.Alias, 0, len(all))

	for _, ad := range all {
		aliases = append(aliases, ad.alias)
	}

	return aliases, nil
}

func copyRoutingConfig(rc *driver.AliasRoutingConfig) *driver.AliasRoutingConfig {
	if rc == nil {
		return nil
	}

	cp := *rc

	return &cp
}

// validateRoutingConfig enforces the RoutingConfig.AdditionalVersionWeights
// rules real Lambda applies: neither the alias's own version nor the additional
// version can be $LATEST (InvalidParameterValueException), and the additional
// version must exist (ResourceNotFoundException). effectiveVersion is the alias's
// own FunctionVersion after the operation. An absent additional version is a no-op.
func (m *Mock) validateRoutingConfig(fd *funcData, effectiveVersion string, rc *driver.AliasRoutingConfig) error {
	if rc == nil || rc.AdditionalVersion == "" {
		return nil
	}

	// A weighted alias cannot point to $LATEST — this restriction applies to the
	// alias's own FunctionVersion (the primary target) as well as the additional
	// version. Both must be published.
	if effectiveVersion == latestVersion || rc.AdditionalVersion == latestVersion {
		return cerrors.New(cerrors.InvalidArgument,
			"Alias with weights can not be created with function version $LATEST")
	}

	if !m.versionExists(fd, rc.AdditionalVersion) {
		return cerrors.Newf(cerrors.NotFound, "version %s not found", rc.AdditionalVersion)
	}

	return nil
}

// versionExists checks whether a version string exists for the given function.
func (*Mock) versionExists(fd *funcData, version string) bool {
	if version == latestVersion {
		return true
	}

	for _, v := range fd.versions {
		if v.version == version {
			return true
		}
	}

	return false
}
