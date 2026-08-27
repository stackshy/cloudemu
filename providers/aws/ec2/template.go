package ec2

import (
	"context"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

var (
	_ driver.LaunchTemplateVersioner = (*Mock)(nil)
	_ driver.LaunchTemplateModifier  = (*Mock)(nil)
)

// templateVersionKey is the store key for one launch-template version.
func templateVersionKey(name string, version int) string {
	return name + "#" + strconv.Itoa(version)
}

// CreateLaunchTemplate creates a new launch template with an initial version 1.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLaunchTemplate(
	_ context.Context, cfg driver.LaunchTemplateConfig,
) (*driver.LaunchTemplate, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "template name is required")
	}

	if m.templates.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "template %q already exists", cfg.Name)
	}

	now := m.opts.Clock.Now().UTC().Format(timeFormat)
	createdBy := idgen.AWSARN("iam", "", m.opts.AccountID, "root")

	tmpl := &driver.LaunchTemplate{
		ID:             idgen.GenerateID("lt-"),
		Name:           cfg.Name,
		Version:        1,
		InstanceConfig: cfg.InstanceConfig,
		CreatedAt:      now,
		DefaultVersion: 1,
		LatestVersion:  1,
		CreatedBy:      createdBy,
		Tags:           copyTags(cfg.Tags),
	}

	m.templates.Set(cfg.Name, tmpl)
	m.templateVersions.Set(templateVersionKey(cfg.Name, 1), &driver.LaunchTemplateVersion{
		LaunchTemplateID:   tmpl.ID,
		LaunchTemplateName: tmpl.Name,
		VersionNumber:      1,
		DefaultVersion:     true,
		CreatedBy:          createdBy,
		CreateTime:         now,
		VersionDescription: cfg.VersionDescription,
		InstanceConfig:     cfg.InstanceConfig,
	})

	result := *tmpl

	return &result, nil
}

// DeleteLaunchTemplate deletes a launch template by name and all its versions.
func (m *Mock) DeleteLaunchTemplate(_ context.Context, name string) error {
	tmpl, ok := m.templates.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "template %q not found", name)
	}

	for v := 1; v <= tmpl.LatestVersion; v++ {
		m.templateVersions.Delete(templateVersionKey(name, v))
	}

	m.templates.Delete(name)

	return nil
}

// GetLaunchTemplate returns a launch template by name.
func (m *Mock) GetLaunchTemplate(_ context.Context, name string) (*driver.LaunchTemplate, error) {
	tmpl, ok := m.templates.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "template %q not found", name)
	}

	result := *tmpl

	return &result, nil
}

// ListLaunchTemplates returns all launch templates.
func (m *Mock) ListLaunchTemplates(_ context.Context) ([]driver.LaunchTemplate, error) {
	all := m.templates.All()
	results := make([]driver.LaunchTemplate, 0, len(all))

	for _, tmpl := range all {
		results = append(results, *tmpl)
	}

	return results, nil
}

// resolveTemplate finds a template by name (preferred) or by id. Real EC2
// accepts either LaunchTemplateName or LaunchTemplateId on the versioning ops.
func (m *Mock) resolveTemplate(name, id string) (*driver.LaunchTemplate, error) {
	if name != "" {
		tmpl, ok := m.templates.Get(name)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "template %q not found", name)
		}

		return tmpl, nil
	}

	if id != "" {
		for _, tmpl := range m.templates.All() {
			if tmpl.ID == id {
				return tmpl, nil
			}
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "template %q not found", id)
}

// CreateLaunchTemplateVersion appends a new immutable version to a template.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLaunchTemplateVersion(
	_ context.Context, input driver.CreateLaunchTemplateVersionInput,
) (*driver.LaunchTemplateVersion, error) {
	tmpl, err := m.resolveTemplate(input.Name, input.ID)
	if err != nil {
		return nil, err
	}

	base, err := m.sourceInstanceConfig(tmpl, input.SourceVersion)
	if err != nil {
		return nil, err
	}

	merged := mergeInstanceConfig(base, input.InstanceConfig)
	next := tmpl.LatestVersion + 1
	now := m.opts.Clock.Now().UTC().Format(timeFormat)

	ver := &driver.LaunchTemplateVersion{
		LaunchTemplateID:   tmpl.ID,
		LaunchTemplateName: tmpl.Name,
		VersionNumber:      next,
		DefaultVersion:     next == tmpl.DefaultVersion,
		CreatedBy:          tmpl.CreatedBy,
		CreateTime:         now,
		VersionDescription: input.VersionDescription,
		InstanceConfig:     merged,
	}

	m.templateVersions.Set(templateVersionKey(tmpl.Name, next), ver)
	tmpl.LatestVersion = next
	tmpl.Version = next
	m.templates.Set(tmpl.Name, tmpl)

	result := *ver

	return &result, nil
}

// sourceInstanceConfig returns the InstanceConfig a new version inherits from.
// An empty SourceVersion inherits nothing (real EC2 builds the version solely
// from the supplied data).
func (m *Mock) sourceInstanceConfig(tmpl *driver.LaunchTemplate, sourceVersion string) (driver.InstanceConfig, error) {
	if sourceVersion == "" {
		return driver.InstanceConfig{}, nil
	}

	n, err := resolveVersionNumber(tmpl, sourceVersion)
	if err != nil {
		return driver.InstanceConfig{}, err
	}

	src, ok := m.templateVersions.Get(templateVersionKey(tmpl.Name, n))
	if !ok {
		return driver.InstanceConfig{}, cerrors.Newf(cerrors.NotFound, "launch template version %q not found", sourceVersion)
	}

	return src.InstanceConfig, nil
}

// resolveVersionNumber maps a version token ("$Latest", "$Default", or a
// numeric string) to a concrete version number for the template.
func resolveVersionNumber(tmpl *driver.LaunchTemplate, token string) (int, error) {
	switch token {
	case "$Latest":
		return tmpl.LatestVersion, nil
	case "$Default":
		return tmpl.DefaultVersion, nil
	default:
		n, err := strconv.Atoi(token)
		if err != nil {
			return 0, cerrors.Newf(cerrors.InvalidArgument, "invalid launch template version %q", token)
		}

		return n, nil
	}
}

// mergeInstanceConfig overlays the non-zero fields of override onto base, so a
// version inheriting from a source only replaces the parameters explicitly set.
//
//nolint:gocritic // hugeParam: value semantics are intentional for the overlay.
func mergeInstanceConfig(base, override driver.InstanceConfig) driver.InstanceConfig {
	out := base

	if override.ImageID != "" {
		out.ImageID = override.ImageID
	}

	if override.InstanceType != "" {
		out.InstanceType = override.InstanceType
	}

	if override.KeyName != "" {
		out.KeyName = override.KeyName
	}

	if override.SubnetID != "" {
		out.SubnetID = override.SubnetID
	}

	if override.UserData != "" {
		out.UserData = override.UserData
	}

	if len(override.SecurityGroups) > 0 {
		out.SecurityGroups = override.SecurityGroups
	}

	if len(override.Tags) > 0 {
		out.Tags = override.Tags
	}

	return out
}

// ModifyLaunchTemplate promotes an existing version to the template's default,
// matching AWS EC2 ModifyLaunchTemplate (SetDefaultVersion). The version must
// already exist.
func (m *Mock) ModifyLaunchTemplate(
	_ context.Context, input driver.ModifyLaunchTemplateInput,
) (*driver.LaunchTemplate, error) {
	tmpl, err := m.resolveTemplate(input.Name, input.ID)
	if err != nil {
		return nil, err
	}

	if input.DefaultVersion != "" {
		n, err := resolveVersionNumber(tmpl, input.DefaultVersion)
		if err != nil {
			return nil, err
		}

		if n < 1 || !m.templateVersions.Has(templateVersionKey(tmpl.Name, n)) {
			return nil, cerrors.Newf(cerrors.InvalidArgument,
				"launch template version %q does not exist", input.DefaultVersion)
		}

		tmpl.DefaultVersion = n
		m.templates.Set(tmpl.Name, tmpl)
	}

	result := *tmpl

	return &result, nil
}

// DescribeLaunchTemplateVersions returns a template's versions, filtered by the
// input's explicit version list and Min/Max bounds, sorted ascending.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) DescribeLaunchTemplateVersions(
	_ context.Context, input driver.DescribeLaunchTemplateVersionsInput,
) ([]driver.LaunchTemplateVersion, error) {
	tmpl, err := m.resolveTemplate(input.Name, input.ID)
	if err != nil {
		return nil, err
	}

	wanted, err := explicitVersions(tmpl, input.Versions)
	if err != nil {
		return nil, err
	}

	minV, maxV, err := versionBounds(input.MinVersion, input.MaxVersion)
	if err != nil {
		return nil, err
	}

	var out []driver.LaunchTemplateVersion

	for v := 1; v <= tmpl.LatestVersion; v++ {
		if wanted != nil {
			if _, ok := wanted[v]; !ok {
				continue
			}
		}

		if v < minV || v > maxV {
			continue
		}

		ver, ok := m.templateVersions.Get(templateVersionKey(tmpl.Name, v))
		if !ok {
			continue
		}

		copyVer := *ver
		copyVer.DefaultVersion = v == tmpl.DefaultVersion
		out = append(out, copyVer)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].VersionNumber < out[j].VersionNumber })

	return out, nil
}

// explicitVersions resolves the request's Version list into a set of concrete
// version numbers. A nil result means "all versions".
func explicitVersions(tmpl *driver.LaunchTemplate, tokens []string) (map[int]struct{}, error) {
	if len(tokens) == 0 {
		return nil, nil //nolint:nilnil // nil set means "no explicit filter"; matches caller contract
	}

	set := make(map[int]struct{}, len(tokens))

	for _, tok := range tokens {
		n, err := resolveVersionNumber(tmpl, tok)
		if err != nil {
			return nil, err
		}

		set[n] = struct{}{}
	}

	return set, nil
}

// versionBounds parses MinVersion/MaxVersion, defaulting to the full range.
func versionBounds(minStr, maxStr string) (minV, maxV int, err error) {
	minV, maxV = 1, int(^uint(0)>>1)

	if minStr != "" {
		minV, err = strconv.Atoi(minStr)
		if err != nil {
			return 0, 0, cerrors.Newf(cerrors.InvalidArgument, "invalid MinVersion %q", minStr)
		}
	}

	if maxStr != "" {
		maxV, err = strconv.Atoi(maxStr)
		if err != nil {
			return 0, 0, cerrors.Newf(cerrors.InvalidArgument, "invalid MaxVersion %q", maxStr)
		}
	}

	return minV, maxV, nil
}

// GetLaunchTemplateData synthesizes launch-template data from a running
// instance, matching the AWS EC2 GetLaunchTemplateData operation.
func (m *Mock) GetLaunchTemplateData(_ context.Context, instanceID string) (*driver.InstanceConfig, error) {
	inst, ok := m.instances.Get(instanceID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %q not found", instanceID)
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	sg := make([]string, len(inst.SecurityGroups))
	copy(sg, inst.SecurityGroups)

	cfg := &driver.InstanceConfig{
		ImageID:        inst.ImageID,
		InstanceType:   inst.InstanceType,
		KeyName:        inst.keyName,
		SubnetID:       inst.SubnetID,
		SecurityGroups: sg,
		Tags:           copyTags(inst.Tags),
	}

	return cfg, nil
}
