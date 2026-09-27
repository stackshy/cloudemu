package ssm

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/parameterstore/driver"
)

// Compile-time check that Mock implements driver.ServiceSettings.
var _ driver.ServiceSettings = (*Mock)(nil)

// Parameter Store service setting ids and their values. See
// UpdateServiceSetting in the Systems Manager API reference.
const (
	settingDefaultTier    = "/ssm/parameter-store/default-parameter-tier"
	settingHighThroughput = "/ssm/parameter-store/high-throughput-enabled"

	settingFalse = "false"

	settingStatusDefault    = "Default"
	settingStatusCustomized = "Customized"

	// settingSystemUser is the LastModifiedUser of a setting nobody changed.
	settingSystemUser = "System"
)

// settingValue is a customized value of one service setting.
type settingValue struct {
	value        string
	lastModified string
	user         string
}

// serviceSettings holds the customized service settings. A setting with no
// entry has its default value.
type serviceSettings struct {
	mu      sync.RWMutex
	values  map[string]settingValue
	created string
}

func newServiceSettings(now time.Time) *serviceSettings {
	return &serviceSettings{values: map[string]settingValue{}, created: now.UTC().Format(time.RFC3339)}
}

// settingDefault returns the default value of a known setting id.
func settingDefault(id string) (string, bool) {
	switch id {
	case settingDefaultTier:
		return tierStandard, true
	case settingHighThroughput:
		return settingFalse, true
	default:
		return "", false
	}
}

// validSettingValue reports whether value is allowed for the setting id.
func validSettingValue(id, value string) bool {
	switch id {
	case settingDefaultTier:
		return value == tierStandard || value == tierAdvanced || value == tierIntelligent
	case settingHighThroughput:
		return value == "true" || value == settingFalse
	default:
		return false
	}
}

// defaultTier returns the tier a new parameter gets when PutParameter omits
// Tier.
func (s *serviceSettings) defaultTier() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if v, ok := s.values[settingDefaultTier]; ok {
		return v.value
	}

	return tierStandard
}

// settingARN builds the ARN of a service setting.
func (m *Mock) settingARN(id string) string {
	return idgen.AWSARN("ssm", m.opts.Region, m.opts.AccountID, "servicesetting"+id)
}

// settingID normalizes a setting id given as a path or as its ARN, and
// checks it is a setting this service provides.
func (m *Mock) settingID(raw string) (string, error) {
	id := raw

	if strings.HasPrefix(raw, "arn:") {
		prefix := m.settingARN("")
		if !strings.HasPrefix(raw, prefix) {
			return "", driver.ErrServiceSettingNotFound
		}

		id = strings.TrimPrefix(raw, prefix)
	}

	if _, ok := settingDefault(id); !ok {
		return "", driver.ErrServiceSettingNotFound
	}

	return id, nil
}

// GetServiceSetting returns the current value of a service setting.
func (m *Mock) GetServiceSetting(_ context.Context, settingID string) (*driver.ServiceSetting, error) {
	id, err := m.settingID(settingID)
	if err != nil {
		return nil, err
	}

	return m.readSetting(id), nil
}

// UpdateServiceSetting sets a service setting to a custom value.
func (m *Mock) UpdateServiceSetting(_ context.Context, settingID, value string) error {
	id, err := m.settingID(settingID)
	if err != nil {
		return err
	}

	if !validSettingValue(id, value) {
		return errors.Newf(errors.InvalidArgument, "The value %q isn't valid for the service setting %s.", value, id)
	}

	m.settings.mu.Lock()
	m.settings.values[id] = settingValue{
		value:        value,
		lastModified: m.now(),
		user:         idgen.AWSARN("iam", "", m.opts.AccountID, "user/cloudemu"),
	}
	m.settings.mu.Unlock()

	return nil
}

// ResetServiceSetting returns a service setting to its default value.
func (m *Mock) ResetServiceSetting(_ context.Context, settingID string) (*driver.ServiceSetting, error) {
	id, err := m.settingID(settingID)
	if err != nil {
		return nil, err
	}

	m.settings.mu.Lock()
	delete(m.settings.values, id)
	m.settings.mu.Unlock()

	return m.readSetting(id), nil
}

// readSetting renders a known setting id.
func (m *Mock) readSetting(id string) *driver.ServiceSetting {
	m.settings.mu.RLock()
	v, customized := m.settings.values[id]
	created := m.settings.created
	m.settings.mu.RUnlock()

	out := &driver.ServiceSetting{SettingID: id, ARN: m.settingARN(id)}

	if customized {
		out.SettingValue, out.LastModifiedDate, out.LastModifiedUser = v.value, v.lastModified, v.user
		out.Status = settingStatusCustomized

		return out
	}

	out.SettingValue, _ = settingDefault(id)
	out.LastModifiedDate, out.LastModifiedUser, out.Status = created, settingSystemUser, settingStatusDefault

	return out
}
