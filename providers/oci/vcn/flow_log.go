package vcn

import (
	"context"
	"fmt"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Flow log statuses and traffic types.
const (
	FlowLogStatusActive = "ACTIVE"
	TrafficTypeAll      = "ALL"
	TrafficTypeAccept   = "ACCEPT"
	TrafficTypeReject   = "REJECT"
)

// Resource kinds a flow log can be enabled on.
const (
	resourceVCN    = "VPC"
	resourceSubnet = "Subnet"
)

// Shape of the synthetic records GetFlowLogRecords hands back.
const (
	defaultFlowLogRecordLimit = 10
	mockSourcePort            = 443
	mockDestPort              = 52000
	mockPackets               = 10
	mockBytes                 = 1500
)

// VCN flow logs are an OCI Logging concept rather than a Core Networking one,
// so the mock implements them for portable callers and the wire layer leaves
// them to the logging service.
type flowLogData struct {
	ID           string
	ResourceID   string
	ResourceType string
	TrafficType  string
	Status       string
	CreatedAt    string
	Tags         map[string]string
}

// CreateFlowLog enables flow logs on a VCN or subnet.
func (m *Mock) CreateFlowLog(_ context.Context, cfg driver.FlowLogConfig) (*driver.FlowLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cfg.ResourceID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "resource OCID is required")
	}

	if err := m.validateFlowLogResource(cfg.ResourceID, cfg.ResourceType); err != nil {
		return nil, err
	}

	trafficType := cfg.TrafficType
	if trafficType == "" {
		trafficType = TrafficTypeAll
	}

	id := m.newOCID(typeLog)
	fl := &flowLogData{
		ID:           id,
		ResourceID:   cfg.ResourceID,
		ResourceType: cfg.ResourceType,
		TrafficType:  trafficType,
		Status:       FlowLogStatusActive,
		CreatedAt:    m.now(),
		Tags:         copyTags(cfg.Tags),
	}

	m.flowLogs.Set(id, fl)
	m.record(id)

	info := toFlowLogInfo(fl)

	return &info, nil
}

// validateFlowLogResource checks that the target resource exists.
func (m *Mock) validateFlowLogResource(resourceID, resourceType string) error {
	switch resourceType {
	case resourceVCN:
		if !m.vcns.Has(resourceID) {
			return cerrors.Newf(cerrors.NotFound, "VCN %q not found", resourceID)
		}
	case resourceSubnet:
		if !m.subnets.Has(resourceID) {
			return cerrors.Newf(cerrors.NotFound, "subnet %q not found", resourceID)
		}
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported resource type %q", resourceType)
	}

	return nil
}

// DeleteFlowLog disables a flow log.
func (m *Mock) DeleteFlowLog(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.flowLogs.Delete(id) {
		return cerrors.Newf(cerrors.NotFound, "flow log %q not found", id)
	}

	m.forget(id)

	return nil
}

// DescribeFlowLogs returns flow logs matching the given OCIDs, or all if empty.
func (m *Mock) DescribeFlowLogs(_ context.Context, ids []string) ([]driver.FlowLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.flowLogs, ids, toFlowLogInfo), nil
}

// GetFlowLogRecords returns synthetic records for a flow log.
func (m *Mock) GetFlowLogRecords(_ context.Context, flowLogID string, limit int) ([]driver.FlowLogRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	fl, ok := m.flowLogs.Get(flowLogID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "flow log %q not found", flowLogID)
	}

	return generateMockRecords(fl, limit), nil
}

// generateMockRecords creates simulated flow log records.
func generateMockRecords(fl *flowLogData, limit int) []driver.FlowLogRecord {
	if limit <= 0 {
		limit = defaultFlowLogRecordLimit
	}

	records := make([]driver.FlowLogRecord, 0, limit)

	for i := range limit {
		action := TrafficTypeAccept
		if fl.TrafficType == TrafficTypeReject || (fl.TrafficType == TrafficTypeAll && i%2 == 1) {
			action = TrafficTypeReject
		}

		records = append(records, driver.FlowLogRecord{
			Timestamp:  fl.CreatedAt,
			SourceIP:   fmt.Sprintf("10.0.0.%d", i+1),
			DestIP:     fmt.Sprintf("10.0.1.%d", i+1),
			SourcePort: mockSourcePort,
			DestPort:   mockDestPort + i,
			Protocol:   protocolTCP,
			Packets:    mockPackets,
			Bytes:      mockBytes,
			Action:     action,
			FlowLogID:  fl.ID,
		})
	}

	return records
}

func toFlowLogInfo(fl *flowLogData) driver.FlowLog {
	return driver.FlowLog{
		ID:           fl.ID,
		ResourceID:   fl.ResourceID,
		ResourceType: fl.ResourceType,
		TrafficType:  fl.TrafficType,
		Status:       fl.Status,
		CreatedAt:    fl.CreatedAt,
		Tags:         copyTags(fl.Tags),
	}
}
