package sesv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// GetAccount returns the account-level SES attributes.
func (m *Mock) GetAccount(_ context.Context) (*driver.Account, error) {
	m.acctMu.RLock()
	defer m.acctMu.RUnlock()

	out := m.account
	out.SuppressedReasons = append([]string(nil), m.account.SuppressedReasons...)

	return &out, nil
}

// PutAccountSendingAttributes toggles account-wide sending.
func (m *Mock) PutAccountSendingAttributes(_ context.Context, sendingEnabled bool) error {
	m.acctMu.Lock()
	defer m.acctMu.Unlock()

	m.account.SendingEnabled = sendingEnabled

	return nil
}

// PutAccountSuppressionAttributes sets the account-wide suppressed reasons.
func (m *Mock) PutAccountSuppressionAttributes(_ context.Context, suppressedReasons []string) error {
	m.acctMu.Lock()
	defer m.acctMu.Unlock()

	m.account.SuppressedReasons = append([]string(nil), suppressedReasons...)

	return nil
}
