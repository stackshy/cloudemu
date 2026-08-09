package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateConfigurationSet registers a configuration set.
func (m *Mock) CreateConfigurationSet(_ context.Context, in driver.CreateConfigurationSetInput) error {
	if in.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "ConfigurationSetName is required")
	}

	cs := driver.ConfigurationSet{
		Name:           in.Name,
		SendingEnabled: in.SendingEnabled,
		ReputationOn:   in.ReputationOn,
		TLSPolicy:      in.TLSPolicy,
		SendingPoolN:   in.SendingPoolN,
		CreatedAt:      m.now(),
		Tags:           copyTags(in.Tags),
	}

	if !m.configSets.SetIfAbsent(in.Name, &configSetData{cs: cs}) {
		return cerrors.Newf(cerrors.AlreadyExists, "configuration set %q already exists", in.Name)
	}

	return nil
}

// GetConfigurationSet returns a configuration set by name.
func (m *Mock) GetConfigurationSet(_ context.Context, name string) (*driver.ConfigurationSet, error) {
	d, ok := m.configSets.Get(name)
	if !ok {
		return nil, errConfigSetNotFound(name)
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := d.cs
	out.Tags = copyTags(d.cs.Tags)

	return &out, nil
}

// DeleteConfigurationSet removes a configuration set.
func (m *Mock) DeleteConfigurationSet(_ context.Context, name string) error {
	if !m.configSets.Delete(name) {
		return errConfigSetNotFound(name)
	}

	return nil
}

// ListConfigurationSets returns all configuration-set names ordered.
func (m *Mock) ListConfigurationSets(_ context.Context) ([]string, error) {
	all := m.configSets.SortedValues()
	out := make([]string, 0, len(all))

	for _, d := range all {
		d.mu.RLock()
		out = append(out, d.cs.Name)
		d.mu.RUnlock()
	}

	return out, nil
}

func (m *Mock) configSetExists(name string) bool {
	return m.configSets.Has(name)
}

func errConfigSetNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "configuration set %q does not exist", name)
}
