package blobstorage

import (
	"context"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

var _ driver.AccountServiceSettings = (*Mock)(nil)

// settingKey keys one account's settings document. Account names cannot hold
// "/", so the key is unambiguous and a "name/" prefix bounds one account.
func settingKey(account, kind string) string {
	return account + "/" + strings.ToLower(kind)
}

// SetAccountSetting implements driver.AccountServiceSettings. The document
// replaces any earlier one wholesale, as an ARM PUT does.
func (m *Mock) SetAccountSetting(_ context.Context, account, kind string, props []byte) (driver.AccountSetting, error) {
	s := driver.AccountSetting{Properties: slices.Clone(props), LastModified: m.opts.Clock.Now().UTC()}
	m.acctSettings.Set(settingKey(account, kind), s)

	return cloneSetting(s), nil
}

// AccountSetting implements driver.AccountServiceSettings.
func (m *Mock) AccountSetting(_ context.Context, account, kind string) (driver.AccountSetting, bool, error) {
	s, ok := m.acctSettings.Get(settingKey(account, kind))

	return cloneSetting(s), ok, nil
}

// DeleteAccountSetting implements driver.AccountServiceSettings.
func (m *Mock) DeleteAccountSetting(_ context.Context, account, kind string) (bool, error) {
	return m.acctSettings.Delete(settingKey(account, kind)), nil
}

// deleteAccountSettings drops every settings document of one account.
func (m *Mock) deleteAccountSettings(account string) {
	prefix := account + "/"

	for _, k := range m.acctSettings.Keys() {
		if strings.HasPrefix(k, prefix) {
			m.acctSettings.Delete(k)
		}
	}
}

func cloneSetting(s driver.AccountSetting) driver.AccountSetting {
	s.Properties = slices.Clone(s.Properties)

	return s
}
