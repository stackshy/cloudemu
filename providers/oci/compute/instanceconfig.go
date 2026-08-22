package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// instanceTypeCompute is the only instance configuration source OCI defines.
const instanceTypeCompute = "compute"

type configData struct {
	ID        string
	Name      string
	Version   int
	Config    driver.InstanceConfig
	Launch    LaunchSpec
	CreatedAt string
	Tags      map[string]string
}

// CreateLaunchTemplate creates an instance configuration.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) CreateLaunchTemplate(
	_ context.Context, cfg driver.LaunchTemplateConfig,
) (*driver.LaunchTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "instance configuration name is required")
	}

	if m.configByName(cfg.Name) != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "instance configuration %q already exists", cfg.Name)
	}

	c := m.addConfig(cfg.Name, cfg.InstanceConfig, specFromConfig(cfg.Name, cfg.InstanceConfig), nil)
	out := toLaunchTemplate(c)

	return &out, nil
}

// addConfig stores an instance configuration. The caller holds m.mu.
//
//nolint:gocritic // hugeParam: InstanceConfig and LaunchSpec are the stored shapes.
func (m *Mock) addConfig(
	name string, cfg driver.InstanceConfig, launch LaunchSpec, tags map[string]string,
) *configData {
	id := m.newOCID(typeInstanceConfig)
	c := &configData{
		ID:        id,
		Name:      name,
		Version:   1,
		Config:    cfg,
		Launch:    launch,
		CreatedAt: m.now(),
		Tags:      copyTags(tags),
	}

	m.configs.Set(id, c)
	m.record(id)

	return c
}

// DeleteLaunchTemplate deletes an instance configuration by display name,
// refusing while an instance pool still uses it.
func (m *Mock) DeleteLaunchTemplate(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c := m.configByName(name)
	if c == nil {
		return configNotFound(name)
	}

	return m.removeConfig(c.ID)
}

// DeleteInstanceConfiguration deletes an instance configuration by OCID, which
// is how OCI addresses it.
func (m *Mock) DeleteInstanceConfiguration(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.configs.Has(id) {
		return configNotFound(id)
	}

	return m.removeConfig(id)
}

// removeConfig drops an instance configuration. The caller holds m.mu.
func (m *Mock) removeConfig(id string) error {
	for _, p := range m.pools.All() {
		if p.ConfigurationID == id {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"instance configuration %q is still used by instance pool %q", id, p.ID)
		}
	}

	m.configs.Delete(id)
	m.forget(id)

	return nil
}

// GetLaunchTemplate returns an instance configuration by display name.
func (m *Mock) GetLaunchTemplate(_ context.Context, name string) (*driver.LaunchTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c := m.configByName(name)
	if c == nil {
		return nil, configNotFound(name)
	}

	out := toLaunchTemplate(c)

	return &out, nil
}

// ListLaunchTemplates returns every instance configuration.
func (m *Mock) ListLaunchTemplates(_ context.Context) ([]driver.LaunchTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.configs, nil, toLaunchTemplate), nil
}

// CreateInstanceConfiguration creates one from OCI's launch details, which the
// portable LaunchTemplateConfig cannot express.
//
//nolint:gocritic // hugeParam: LaunchSpec is the value type being stored.
func (m *Mock) CreateInstanceConfiguration(
	_ context.Context, displayName string, launch LaunchSpec, tags map[string]string,
) (*InstanceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if launch.Shape == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "instance configuration shape is required")
	}

	name := orDefault(displayName, launch.DisplayName)
	if name != "" && m.configByName(name) != nil {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "instance configuration %q already exists", name)
	}

	c := m.addConfig(name, configFromSpec(launch), launch, tags)
	out := toInstanceConfiguration(c)

	return &out, nil
}

// GetInstanceConfiguration returns one instance configuration by OCID.
func (m *Mock) GetInstanceConfiguration(_ context.Context, id string) (*InstanceConfiguration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.configs.Get(id)
	if !ok {
		return nil, configNotFound(id)
	}

	out := toInstanceConfiguration(c)

	return &out, nil
}

// ListInstanceConfigurations returns the instance configurations in a
// compartment.
func (m *Mock) ListInstanceConfigurations(_ context.Context, compartmentID string) ([]InstanceConfiguration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]InstanceConfiguration, 0)

	for _, c := range m.configs.SortedValues() {
		if s, _ := m.scopes.Get(c.ID); s.Compartment != compartmentID {
			continue
		}

		out = append(out, toInstanceConfiguration(c))
	}

	return out, nil
}

// UpdateInstanceConfiguration changes an instance configuration's display name
// and tags. OCI does not allow its launch details to be edited.
func (m *Mock) UpdateInstanceConfiguration(
	_ context.Context, id string, upd Update,
) (*InstanceConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.configs.Has(id) {
		return nil, configNotFound(id)
	}

	m.configs.Update(id, func(c *configData) *configData {
		if upd.DisplayName != nil {
			c.Name = *upd.DisplayName
			c.Launch.DisplayName = *upd.DisplayName
		}

		if upd.Tags != nil {
			c.Tags = mergeTags(c.Tags, upd.Tags)
		}

		return c
	})

	updated, _ := m.configs.Get(id)
	out := toInstanceConfiguration(updated)

	return &out, nil
}

// LaunchFromInstanceConfiguration launches a single instance from a saved
// configuration, OCI's launchCompute action.
func (m *Mock) LaunchFromInstanceConfiguration(
	ctx context.Context, id string, overrides *LaunchSpec,
) (*driver.Instance, error) {
	cfg, err := m.launchConfigOf(id, overrides)
	if err != nil {
		return nil, err
	}

	instances, err := m.RunInstances(ctx, cfg, 1)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.details.Update(instances[0].ID, func(d InstanceDetails) InstanceDetails {
		d.InstanceConfigurationID = id

		return d
	})

	return &instances[0], nil
}

// launchConfigOf resolves a configuration's launch details, applying the
// caller's overrides.
func (m *Mock) launchConfigOf(id string, overrides *LaunchSpec) (driver.InstanceConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.configs.Get(id)
	if !ok {
		return driver.InstanceConfig{}, configNotFound(id)
	}

	spec := c.Launch

	if overrides != nil {
		spec = mergeSpec(spec, *overrides)
	}

	return configFromSpec(spec), nil
}

// mergeSpec layers an override spec over a saved one, field by field.
//
//nolint:gocritic // hugeParam: LaunchSpec is the value type being merged.
func mergeSpec(base, over LaunchSpec) LaunchSpec {
	out := base

	for _, pair := range []struct {
		dst *string
		src string
	}{
		{&out.AvailabilityDomain, over.AvailabilityDomain},
		{&out.FaultDomain, over.FaultDomain},
		{&out.DisplayName, over.DisplayName},
		{&out.Shape, over.Shape},
		{&out.ImageID, over.ImageID},
		{&out.SubnetID, over.SubnetID},
	} {
		if pair.src != "" {
			*pair.dst = pair.src
		}
	}

	if over.ShapeConfig != nil {
		out.ShapeConfig = over.ShapeConfig
	}

	if len(over.NSGIDs) > 0 {
		out.NSGIDs = copyStrings(over.NSGIDs)
	}

	if len(over.Metadata) > 0 {
		out.Metadata = copyTags(over.Metadata)
	}

	if len(over.Tags) > 0 {
		out.Tags = mergeTags(out.Tags, over.Tags)
	}

	return out
}

// configByName finds an instance configuration by display name. The caller
// holds m.mu.
func (m *Mock) configByName(name string) *configData {
	for _, c := range m.configs.SortedValues() {
		if c.Name == name {
			return c
		}
	}

	return nil
}

// toInstanceConfiguration projects stored data onto OCI's shape.
func toInstanceConfiguration(c *configData) InstanceConfiguration {
	return InstanceConfiguration{
		ID:           c.ID,
		DisplayName:  c.Name,
		InstanceType: instanceTypeCompute,
		Launch:       c.Launch,
		TimeCreated:  c.CreatedAt,
		Tags:         copyTags(c.Tags),
	}
}

func toLaunchTemplate(c *configData) driver.LaunchTemplate {
	return driver.LaunchTemplate{
		ID:             c.ID,
		Name:           c.Name,
		Version:        c.Version,
		InstanceConfig: c.Config,
		CreatedAt:      c.CreatedAt,
	}
}

// specFromConfig projects a portable launch config onto OCI's launch details.
//
//nolint:gocritic // hugeParam: the driver value type is the input.
func specFromConfig(name string, cfg driver.InstanceConfig) LaunchSpec {
	return LaunchSpec{
		AvailabilityDomain: firstOr(cfg.Zones, ""),
		DisplayName:        name,
		Shape:              cfg.InstanceType,
		ImageID:            cfg.ImageID,
		SubnetID:           cfg.SubnetID,
		NSGIDs:             copyStrings(cfg.SecurityGroups),
		Metadata:           launchMetadata(cfg.UserData),
		IsPreemptible:      cfg.Priority == prioritySpot,
		Tags:               copyTags(cfg.Tags),
	}
}

// configFromSpec is specFromConfig's inverse.
//
//nolint:gocritic // hugeParam: LaunchSpec is the value type being converted.
func configFromSpec(spec LaunchSpec) driver.InstanceConfig {
	cfg := driver.InstanceConfig{
		ImageID:        spec.ImageID,
		InstanceType:   spec.Shape,
		Tags:           copyTags(spec.Tags),
		SubnetID:       spec.SubnetID,
		SecurityGroups: copyStrings(spec.NSGIDs),
		UserData:       spec.Metadata["user_data"],
		Priority:       priorityRegular,
	}

	if spec.IsPreemptible {
		cfg.Priority = prioritySpot
	}

	if spec.AvailabilityDomain != "" {
		cfg.Zones = []string{spec.AvailabilityDomain}
	}

	if spec.DisplayName != "" {
		cfg.Tags[TagDisplayName] = spec.DisplayName
	}

	return cfg
}

func configNotFound(id string) error {
	return cerrors.Newf(cerrors.NotFound, "instance configuration %q not found", id)
}
