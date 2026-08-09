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

// PutAccountDetails sets production access from the submitted account details.
func (m *Mock) PutAccountDetails(_ context.Context, _, _ string, productionAccess bool) error {
	m.acctMu.Lock()
	defer m.acctMu.Unlock()

	m.account.ProductionAccessEnabled = productionAccess

	return nil
}

// PutAccountVdmAttributes toggles account-wide VDM.
func (m *Mock) PutAccountVdmAttributes(_ context.Context, enabled bool) error {
	m.dashMu.Lock()
	defer m.dashMu.Unlock()

	m.vdmEnabled = enabled

	return nil
}

// PutAccountPricingAttributes is accepted as a no-op; the emulator has no
// pricing tier to configure.
func (*Mock) PutAccountPricingAttributes(_ context.Context) error {
	return nil
}
