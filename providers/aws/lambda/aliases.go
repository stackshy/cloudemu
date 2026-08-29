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

	// ad is a shared pointer held in the store; guard the read-validate-mutate of
	// its alias fields so a concurrent Update/Get/List cannot race it.
	ad.mu.Lock()
	defer ad.mu.Unlock()

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

	// ad is the shared pointer already held in the store, so the in-place
	// mutations above are already visible — no re-Set needed.
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

	ad.mu.Lock()
	result := ad.alias
	ad.mu.Unlock()

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
		ad.mu.Lock()
		aliases = append(aliases, ad.alias)
		ad.mu.Unlock()
	}

	return aliases, nil
}

func copyRoutingConfig(rc *driver.AliasRoutingConfig) *driver.AliasRoutingConfig {
	if rc == nil {
		return nil
	}

	if len(rc.AdditionalVersionWeights) == 0 {
		return &driver.AliasRoutingConfig{}
	}

	weights := make(map[string]float64, len(rc.AdditionalVersionWeights))
	for v, w := range rc.AdditionalVersionWeights {
		weights[v] = w
	}

	return &driver.AliasRoutingConfig{AdditionalVersionWeights: weights}
}

// validateRoutingConfig enforces the RoutingConfig.AdditionalVersionWeights
// rules real Lambda applies: neither the alias's own version nor any additional
// version can be $LATEST (InvalidParameterValueException), every additional
// version must exist (ResourceNotFoundException), each weight must be within
// [0.0, 1.0], and the additional weights must sum to at most 1.0
// (InvalidParameterValueException). effectiveVersion is the alias's own
// FunctionVersion after the operation. An empty weights map is a no-op.
func (m *Mock) validateRoutingConfig(fd *funcData, effectiveVersion string, rc *driver.AliasRoutingConfig) error {
	if rc == nil || len(rc.AdditionalVersionWeights) == 0 {
		return nil
	}

	// A weighted alias cannot point to $LATEST — this restriction applies to the
	// alias's own FunctionVersion (the primary target) as well as every
	// additional version. All must be published.
	if effectiveVersion == latestVersion {
		return cerrors.New(cerrors.InvalidArgument,
			"Alias with weights can not be created with function version $LATEST")
	}

	var weightSum float64

	for version, weight := range rc.AdditionalVersionWeights {
		if version == latestVersion {
			return cerrors.New(cerrors.InvalidArgument,
				"Alias with weights can not be created with function version $LATEST")
		}

		if !m.versionExists(fd, version) {
			return cerrors.Newf(cerrors.NotFound, "version %s not found", version)
		}

		if weight < driver.MinVersionWeight || weight > driver.MaxVersionWeight {
			return cerrors.Newf(cerrors.InvalidArgument,
				"Weight for version %s must be between 0.0 and 1.0", version)
		}

		weightSum += weight
	}

	// The additional weights share traffic with the primary version, so their
	// sum cannot exceed 1.0 (the primary keeps the remainder).
	if weightSum > driver.MaxVersionWeight {
		return cerrors.New(cerrors.InvalidArgument,
			"Sum of the additional version weights must not exceed 1.0")
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
