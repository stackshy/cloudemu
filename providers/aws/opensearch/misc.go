package opensearch

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// StartDomainMaintenance starts a maintenance action, returning its ID.
func (m *Mock) StartDomainMaintenance(_ context.Context, domainName, action, _ string) (string, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return "", err
	}

	if action == "" {
		return "", validation("Action is required")
	}

	return idgen.GenerateID("maintenance-"), nil
}

// GetDomainMaintenanceStatus returns a synthesized completed maintenance status.
func (m *Mock) GetDomainMaintenanceStatus(_ context.Context, domainName, _ string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	now := float64(m.now().Unix())

	return map[string]json.RawMessage{
		"Status":        rawString("COMPLETED"),
		"StatusMessage": rawString("Maintenance action completed."),
		"CreatedAt":     rawFloat(now),
		"UpdatedAt":     rawFloat(now),
	}, nil
}

// ListDomainMaintenances returns an empty synthesized maintenance list.
func (m *Mock) ListDomainMaintenances(_ context.Context, domainName, _, _ string,
	_ driver.Page,
) (maintenances []map[string]json.RawMessage, next string, err error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, "", err
	}

	return []map[string]json.RawMessage{}, "", nil
}

// ListScheduledActions returns an empty synthesized scheduled-action list.
func (m *Mock) ListScheduledActions(_ context.Context, domainName string,
	_ driver.Page,
) (actions []map[string]json.RawMessage, next string, err error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, "", err
	}

	return []map[string]json.RawMessage{}, "", nil
}

// UpdateScheduledAction returns a synthesized updated scheduled action.
func (m *Mock) UpdateScheduledAction(_ context.Context, domainName, actionID, actionType, _ string,
	desiredStartTime int64,
) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	if actionID == "" {
		return nil, validation("ActionID is required")
	}

	return map[string]json.RawMessage{
		"Id":            rawString(actionID),
		"Type":          rawString(actionType),
		"Status":        rawString("PENDING_UPDATE"),
		"ScheduledTime": rawFloat(float64(desiredStartTime)),
		"Mandatory":     json.RawMessage("false"),
		"Cancellable":   json.RawMessage("true"),
	}, nil
}

// indexAck returns a synthesized index acknowledgement for a domain index op.
func (m *Mock) indexAck(domainName, indexName string) (map[string]json.RawMessage, error) {
	if _, err := m.getDomain(domainName); err != nil {
		return nil, err
	}

	if indexName == "" {
		return nil, validation("IndexName is required")
	}

	return map[string]json.RawMessage{
		"acknowledged": json.RawMessage("true"),
		"index":        rawString(indexName),
	}, nil
}

// CreateIndex creates an index on a domain (synthesized acknowledgement).
func (m *Mock) CreateIndex(_ context.Context, domainName, indexName string,
	_ map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	return m.indexAck(domainName, indexName)
}

// DeleteIndex deletes an index on a domain (synthesized acknowledgement).
func (m *Mock) DeleteIndex(_ context.Context, domainName, indexName string) (map[string]json.RawMessage, error) {
	return m.indexAck(domainName, indexName)
}

// GetIndex returns a synthesized index description.
func (m *Mock) GetIndex(_ context.Context, domainName, indexName string) (map[string]json.RawMessage, error) {
	return m.indexAck(domainName, indexName)
}

// UpdateIndex updates an index's settings (synthesized acknowledgement).
func (m *Mock) UpdateIndex(_ context.Context, domainName, indexName string,
	_ map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	return m.indexAck(domainName, indexName)
}

// ListInsights returns an empty synthesized insight list.
func (*Mock) ListInsights(_ context.Context, _ driver.Page) (insights []map[string]json.RawMessage, nextToken string, err error) {
	return []map[string]json.RawMessage{}, "", nil
}

// DescribeInsightDetails returns a synthesized empty insight-details record.
func (*Mock) DescribeInsightDetails(_ context.Context, insightID string) (details map[string]json.RawMessage, err error) {
	if insightID == "" {
		return nil, validation("InsightId is required")
	}

	return map[string]json.RawMessage{
		"InsightId":      rawString(insightID),
		"InsightDetails": json.RawMessage("[]"),
	}, nil
}

// InsightFeedback records feedback for an insight (no-op).
func (*Mock) InsightFeedback(_ context.Context, insightID, _ string) error {
	if insightID == "" {
		return validation("InsightId is required")
	}

	return nil
}

// StartMigration starts a serverless migration, returning its ID.
func (*Mock) StartMigration(_ context.Context, _ map[string]json.RawMessage) (string, error) {
	return idgen.GenerateID("migration-"), nil
}

// GetMigration returns a synthesized completed migration status.
func (*Mock) GetMigration(_ context.Context, migrationID string) (map[string]json.RawMessage, error) {
	if migrationID == "" {
		return nil, validation("MigrationId is required")
	}

	return map[string]json.RawMessage{
		"MigrationId":     rawString(migrationID),
		"MigrationStatus": rawString("COMPLETED"),
	}, nil
}

// ListMigrations returns an empty synthesized migration list.
func (*Mock) ListMigrations(_ context.Context, _ driver.Page) (migrations []string, nextToken string, err error) {
	return []string{}, "", nil
}

// RegisterCapability registers a capability for an application (synthesized).
func (m *Mock) RegisterCapability(_ context.Context, applicationID, capability string,
	payload map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, notFound("Application not found: %s", applicationID)
	}

	if capability == "" {
		return nil, validation("Capability is required")
	}

	out := copyRaw(payload)
	if out == nil {
		out = map[string]json.RawMessage{}
	}

	out["capabilityName"] = rawString(capability)

	return out, nil
}

// DeregisterCapability removes a capability from an application.
func (m *Mock) DeregisterCapability(_ context.Context, applicationID, capability string) error {
	if _, ok := m.apps.Get(applicationID); !ok {
		return notFound("Application not found: %s", applicationID)
	}

	if capability == "" {
		return validation("Capability is required")
	}

	return nil
}

// GetCapability returns a synthesized capability record.
func (m *Mock) GetCapability(_ context.Context, applicationID, capability string) (map[string]json.RawMessage, error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, notFound("Application not found: %s", applicationID)
	}

	if capability == "" {
		return nil, validation("Capability is required")
	}

	return map[string]json.RawMessage{"capabilityName": rawString(capability)}, nil
}

// GetDefaultApplicationSetting returns the stored default application setting.
func (m *Mock) GetDefaultApplicationSetting(_ context.Context) (map[string]json.RawMessage, error) {
	m.defaultAppMu.RLock()
	defer m.defaultAppMu.RUnlock()

	return copyRaw(m.defaultAppSet), nil
}

// PutDefaultApplicationSetting stores the default application setting.
func (m *Mock) PutDefaultApplicationSetting(_ context.Context, setting map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	m.defaultAppMu.Lock()
	defer m.defaultAppMu.Unlock()

	m.defaultAppSet = copyRaw(setting)
	if m.defaultAppSet == nil {
		m.defaultAppSet = map[string]json.RawMessage{}
	}

	return copyRaw(m.defaultAppSet), nil
}

// AttachDataSource attaches a data source to an application (synthesized).
func (m *Mock) AttachDataSource(_ context.Context, applicationID string,
	ds map[string]json.RawMessage,
) (map[string]json.RawMessage, error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, notFound("Application not found: %s", applicationID)
	}

	out := copyRaw(ds)
	if out == nil {
		out = map[string]json.RawMessage{}
	}

	out["dataSourceAttachmentId"] = rawString(idgen.GenerateID("dsa-"))

	return out, nil
}

// DetachDataSource detaches a data source from an application (synthesized).
func (m *Mock) DetachDataSource(_ context.Context, applicationID, dataSourceARN string) (map[string]json.RawMessage, error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, notFound("Application not found: %s", applicationID)
	}

	return map[string]json.RawMessage{"dataSourceArn": rawString(dataSourceARN)}, nil
}

// DescribeDataSourceAttachment returns a synthesized attachment record.
func (m *Mock) DescribeDataSourceAttachment(_ context.Context, applicationID, attachmentID string) (map[string]json.RawMessage, error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, notFound("Application not found: %s", applicationID)
	}

	return map[string]json.RawMessage{
		"dataSourceAttachmentId": rawString(attachmentID),
		"attachmentState":        rawString("ACTIVE"),
	}, nil
}

// ListDataSourceAttachments returns an empty synthesized attachment list.
func (m *Mock) ListDataSourceAttachments(
	_ context.Context, applicationID string, _ driver.Page,
) (attachments []map[string]json.RawMessage, nextToken string, err error) {
	if _, ok := m.apps.Get(applicationID); !ok {
		return nil, "", notFound("Application not found: %s", applicationID)
	}

	return []map[string]json.RawMessage{}, "", nil
}
