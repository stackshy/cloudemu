package acm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/acm/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// acmSnapshot is the full serialized state of the AWS ACM mock. The certs store
// holds an unexported *certData whose stored certificate lives in an unexported
// field (invisible to json.Marshal), so it is promoted to an exported form keyed
// by certificate ARN. The account-level configuration (cfgMu-guarded) is
// captured beside it. The per-cert settle window (a read-time PENDING_VALIDATION
// overlay) and the wired opts are intentionally not serialized — a restored
// certificate reports its stored (final) state.
type acmSnapshot struct {
	Certs     map[string]*certSnapshot    `json:"certs,omitempty"`
	AccountFg driver.AccountConfiguration `json:"accountFg"`
}

// certSnapshot is the exported form of certData; only the stored certificate is
// durable state, the settle overlay and mutex are not.
type certSnapshot struct {
	Cert driver.Certificate `json:"cert"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// ACM holds certificate material, not bulk object bodies, and it is always kept.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := acmSnapshot{}

	if m.certs.Len() > 0 {
		snap.Certs = make(map[string]*certSnapshot, m.certs.Len())

		for arn, cd := range m.certs.All() {
			cd.mu.RLock()
			snap.Certs[arn] = &certSnapshot{Cert: cd.cert}
			cd.mu.RUnlock()
		}
	}

	m.cfgMu.RLock()
	snap.AccountFg = m.accountFg
	m.cfgMu.RUnlock()

	return json.Marshal(snap)
}

// Restore rebuilds the mock's state under the original identities: every
// certificate ARN is preserved, so an ARN a client holds still resolves.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap acmSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("acm: parse snapshot: %w", err)
	}

	for arn, cs := range snap.Certs {
		m.certs.Set(arn, &certData{cert: cs.Cert})
	}

	m.cfgMu.Lock()
	m.accountFg = snap.AccountFg
	m.cfgMu.Unlock()

	return nil
}
