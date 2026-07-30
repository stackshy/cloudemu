package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// PutAccountSetting sets an account setting for the authenticated principal and
// returns it. Any name/value is accepted.
func (m *Mock) PutAccountSetting(_ context.Context, name, value string) (*driver.AccountSetting, error) {
	return m.putSetting(name, value)
}

// PutAccountSettingDefault sets the account-wide default for a setting and
// returns it. The mock does not distinguish principal-scoped settings from the
// default, so it shares one store with PutAccountSetting.
func (m *Mock) PutAccountSettingDefault(_ context.Context, name, value string) (*driver.AccountSetting, error) {
	return m.putSetting(name, value)
}

func (m *Mock) putSetting(name, value string) (*driver.AccountSetting, error) {
	if name == "" {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter, "name is required")
	}

	s := &driver.AccountSetting{Name: name, Value: value}
	m.settings.Set(name, s)

	out := *s

	return &out, nil
}

// ListAccountSettings returns all account settings in deterministic (name) order.
func (m *Mock) ListAccountSettings(_ context.Context) ([]driver.AccountSetting, error) {
	all := m.settings.SortedValues()

	out := make([]driver.AccountSetting, 0, len(all))
	for _, s := range all {
		out = append(out, *s)
	}

	return out, nil
}

// DeleteAccountSetting removes an account setting and returns its last value. A
// name that was never set returns a zero-valued setting (AWS is lenient here).
func (m *Mock) DeleteAccountSetting(_ context.Context, name string) (*driver.AccountSetting, error) {
	if name == "" {
		return nil, apiErrf(errors.InvalidArgument, excInvalidParameter, "name is required")
	}

	s, ok := m.settings.Get(name)
	m.settings.Delete(name)

	if !ok {
		return &driver.AccountSetting{Name: name}, nil
	}

	out := *s

	return &out, nil
}
