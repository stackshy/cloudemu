package cloudfunctions

import (
	"context"
	"time"

	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/serverless/driver"
)

// CreateAlias creates a new traffic split alias pointing to a specific function version.
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

	aliasARN := idgen.GCPID(
		m.opts.ProjectID, "functions",
		cfg.FunctionName+"/aliases/"+cfg.Name,
	)

	a := driver.Alias{
		FunctionName:    cfg.FunctionName,
		Name:            cfg.Name,
		FunctionVersion: cfg.FunctionVersion,
		Description:     cfg.Description,
		RoutingConfig:   cfg.RoutingConfig,
		AliasARN:        aliasARN,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	fd.aliases.Set(cfg.Name, &aliasData{alias: a})

	result := a

	return &result, nil
}

// UpdateAlias updates an existing traffic split alias.
func (m *Mock) UpdateAlias(_ context.Context, cfg driver.AliasConfig) (*driver.Alias, error) {
	fd, ok := m.funcs.Get(cfg.FunctionName)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", cfg.FunctionName)
	}

	ad, ok := fd.aliases.Get(cfg.Name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "alias %s not found", cfg.Name)
	}

	if cfg.FunctionVersion != "" {
		if !m.versionExists(&fd, cfg.FunctionVersion) {
			return nil, cerrors.Newf(cerrors.NotFound, "version %s not found", cfg.FunctionVersion)
		}

		ad.alias.FunctionVersion = cfg.FunctionVersion
	}

	if cfg.Description != "" {
		ad.alias.Description = cfg.Description
	}

	if cfg.RoutingConfig != nil {
		ad.alias.RoutingConfig = cfg.RoutingConfig
	}

	fd.aliases.Set(cfg.Name, ad)

	result := ad.alias

	return &result, nil
}

// DeleteAlias removes a traffic split alias from a function.
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

// GetAlias retrieves a specific traffic split alias for a function.
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

// ListAliases returns all traffic split aliases for a function.
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
