package opensearch

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

const fullProgress = 100.0

// serviceSoftwareStatus returns a synthesized software-options block reporting
// the given deployment state.
func (*Mock) serviceSoftwareStatus(state string) map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"ServiceSoftwareOptions": json.RawMessage(`{
			"CurrentVersion": "OpenSearch_2.11_R20240101",
			"NewVersion": "",
			"UpdateAvailable": false,
			"Cancellable": false,
			"UpdateStatus": "` + state + `",
			"Description": "There is no software update available for this domain.",
			"OptionalDeployment": true
		}`),
	}
}

// StartServiceSoftwareUpdate marks a domain's software update as in progress.
func (m *Mock) StartServiceSoftwareUpdate(_ context.Context, name string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return m.serviceSoftwareStatus("PENDING_UPDATE"), nil
}

// CancelServiceSoftwareUpdate cancels a pending software update.
func (m *Mock) CancelServiceSoftwareUpdate(_ context.Context, name string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return m.serviceSoftwareStatus("COMPLETED"), nil
}

// RollbackServiceSoftwareUpdate rolls back a pending software update.
func (m *Mock) RollbackServiceSoftwareUpdate(_ context.Context, name string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return m.serviceSoftwareStatus("COMPLETED"), nil
}

// UpgradeDomain upgrades a domain to a target engine version. When perform is
// true the domain's stored version is updated; a dry run (perform=false) leaves
// it unchanged. Returns an upgrade ID.
func (m *Mock) UpgradeDomain(_ context.Context, name, targetVersion string, perform bool, _ map[string]string) (string, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return "", err
	}

	if targetVersion == "" {
		return "", validation("TargetVersion is required")
	}

	if perform {
		dd.mu.Lock()
		dd.status.EngineVersion = targetVersion
		dd.config.EngineVersion = targetVersion
		dd.mu.Unlock()
	}

	return idgen.GenerateID("upgrade-"), nil
}

// GetUpgradeStatus returns a synthesized succeeded upgrade status.
func (m *Mock) GetUpgradeStatus(_ context.Context, name string) (*driver.UpgradeStep, error) {
	if _, err := m.getDomain(name); err != nil {
		return nil, err
	}

	return &driver.UpgradeStep{
		UpgradeStep:     "UPGRADE",
		StepStatus:      "SUCCEEDED",
		ProgressPercent: fullProgress,
	}, nil
}

// GetUpgradeHistory returns a synthesized upgrade history for the domain.
func (m *Mock) GetUpgradeHistory(_ context.Context, name string, _ driver.Page) ([]driver.UpgradeHistory, string, error) {
	dd, err := m.getDomain(name)
	if err != nil {
		return nil, "", err
	}

	dd.mu.RLock()
	version := dd.status.EngineVersion
	created := dd.status.CreatedAt
	dd.mu.RUnlock()

	return []driver.UpgradeHistory{{
		UpgradeName:   "Upgrade to " + version,
		StartTime:     created,
		UpgradeStatus: "SUCCEEDED",
		StepsList: []driver.UpgradeStep{
			{UpgradeStep: "PRE_UPGRADE_CHECK", StepStatus: "SUCCEEDED", ProgressPercent: fullProgress},
			{UpgradeStep: "SNAPSHOT", StepStatus: "SUCCEEDED", ProgressPercent: fullProgress},
			{UpgradeStep: "UPGRADE", StepStatus: "SUCCEEDED", ProgressPercent: fullProgress},
		},
	}}, "", nil
}
