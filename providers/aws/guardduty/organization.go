package guardduty

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/guardduty/driver"
)

// AdminStatus values GuardDuty reports for a delegated administrator account.
const adminStatusEnabled = "ENABLED"

// orgConfigData is a detector's organization auto-enable configuration. Feature
// configs are carried verbatim as raw JSON because the emulator does not
// interpret them.
type orgConfigData struct {
	autoEnable        *bool
	autoEnableMembers string
	features          []json.RawMessage
	dataSources       json.RawMessage
}

// copyOrgConfig deep-copies an org config so a reader cannot alias its feature
// slice or data-source block.
func copyOrgConfig(c orgConfigData) orgConfigData {
	out := c

	if c.autoEnable != nil {
		v := *c.autoEnable
		out.autoEnable = &v
	}

	out.features = copyRawSlice(c.features)
	out.dataSources = copyRaw(c.dataSources)

	return out
}

// adminAccountRequest is the Enable/DisableOrganizationAdminAccount body.
type adminAccountRequest struct {
	AdminAccountID string `json:"adminAccountId"`
}

// EnableOrganizationAdminAccount registers a delegated GuardDuty administrator
// account. It is idempotent: re-enabling an existing admin is a no-op success.
func (m *Mock) EnableOrganizationAdminAccount(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	var req adminAccountRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	if req.AdminAccountID == "" {
		return nil, badRequest("adminAccountId is required")
	}

	m.orgAdmins.Set(req.AdminAccountID, true)

	return json.Marshal(map[string]any{})
}

// DisableOrganizationAdminAccount removes a delegated administrator account.
func (m *Mock) DisableOrganizationAdminAccount(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	var req adminAccountRequest
	if err := unmarshalBody(body, &req); err != nil {
		return nil, err
	}

	if req.AdminAccountID == "" {
		return nil, badRequest("adminAccountId is required")
	}

	m.orgAdmins.Delete(req.AdminAccountID)

	return json.Marshal(map[string]any{})
}

// ListOrganizationAdminAccounts lists delegated administrator accounts, sorted.
func (m *Mock) ListOrganizationAdminAccounts(_ context.Context, page driver.Page) (json.RawMessage, error) {
	ids := m.orgAdmins.Keys()
	sort.Strings(ids)

	pageIDs, next, err := paginateIDs(ids, page)
	if err != nil {
		return nil, err
	}

	accounts := make([]map[string]any, 0, len(pageIDs))
	for _, id := range pageIDs {
		accounts = append(accounts, map[string]any{
			"adminAccountId": id,
			"adminStatus":    adminStatusEnabled,
		})
	}

	return json.Marshal(withNextToken(map[string]any{"adminAccounts": accounts}, next))
}

// DescribeOrganizationConfiguration returns a detector's org auto-enable config.
func (m *Mock) DescribeOrganizationConfiguration(_ context.Context, detectorID string, _ driver.Page) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	cfg := copyOrgConfig(dd.orgConfig)
	dd.mu.RUnlock()

	out := map[string]any{"memberAccountLimitReached": false}

	if cfg.autoEnable != nil {
		out["autoEnable"] = *cfg.autoEnable
	}

	if cfg.autoEnableMembers != "" {
		out["autoEnableOrganizationMembers"] = cfg.autoEnableMembers
	}

	if len(cfg.features) > 0 {
		out["features"] = cfg.features
	}

	if cfg.dataSources != nil {
		out["dataSources"] = cfg.dataSources
	}

	return json.Marshal(out)
}

// updateOrgConfigRequest is the UpdateOrganizationConfiguration body.
type updateOrgConfigRequest struct {
	AutoEnable                    *bool             `json:"autoEnable"`
	AutoEnableOrganizationMembers string            `json:"autoEnableOrganizationMembers"`
	Features                      []json.RawMessage `json:"features"`
	DataSources                   json.RawMessage   `json:"dataSources"`
}

// UpdateOrganizationConfiguration patches a detector's org auto-enable config.
// Only supplied fields are applied.
func (m *Mock) UpdateOrganizationConfiguration(_ context.Context, detectorID string, body json.RawMessage) (json.RawMessage, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	var req updateOrgConfigRequest
	if uerr := unmarshalBody(body, &req); uerr != nil {
		return nil, uerr
	}

	dd.mu.Lock()
	if req.AutoEnable != nil {
		v := *req.AutoEnable
		dd.orgConfig.autoEnable = &v
	}

	if req.AutoEnableOrganizationMembers != "" {
		dd.orgConfig.autoEnableMembers = req.AutoEnableOrganizationMembers
	}

	if req.Features != nil {
		dd.orgConfig.features = copyRawSlice(req.Features)
	}

	if req.DataSources != nil {
		dd.orgConfig.dataSources = copyRaw(req.DataSources)
	}
	dd.mu.Unlock()

	return json.Marshal(map[string]any{})
}

// GetOrganizationStatistics returns aggregate active-member/detector counts
// across all detectors in the emulated account.
func (m *Mock) GetOrganizationStatistics(_ context.Context) (json.RawMessage, error) {
	totalDetectors := 0
	totalMembers := 0

	for _, id := range m.detectors.Keys() {
		dd, ok := m.detectors.Get(id)
		if !ok {
			continue
		}

		totalDetectors++

		dd.mu.RLock()
		totalMembers += len(dd.members)
		dd.mu.RUnlock()
	}

	details := map[string]any{
		"organizationDetails": map[string]any{
			"organizationStatistics": map[string]any{
				"totalAccountsCount":   totalMembers,
				"memberAccountsCount":  totalMembers,
				"activeAccountsCount":  totalDetectors,
				"enabledAccountsCount": totalDetectors,
			},
		},
	}

	return json.Marshal(details)
}
