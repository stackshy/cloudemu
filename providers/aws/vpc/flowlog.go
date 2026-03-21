package vpc

import (
	"context"
	"fmt"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
	"github.com/stackshy/cloudemu/networking/driver"
)

// Flow log status constants.
const (
	FlowLogStatusActive = "ACTIVE"
)

// Flow log traffic type constants.
const (
	TrafficTypeAll    = "ALL"
	TrafficTypeAccept = "ACCEPT"
	TrafficTypeReject = "REJECT"
)

// Mock flow log record constants.
const (
	mockSourcePort = 443
	mockDestPort   = 52000
	mockPackets    = 10
	mockBytes      = 1500
)

type flowLogData struct {
	ID           string
	ResourceID   string
	ResourceType string
	TrafficType  string
	Status       string
	CreatedAt    string
	Tags         map[string]string
}

// CreateFlowLog creates a flow log for a VPC or subnet resource.
func (m *Mock) CreateFlowLog(_ context.Context, cfg driver.FlowLogConfig) (*driver.FlowLog, error) {
	if cfg.ResourceID == "" {
		return nil, errors.New(errors.InvalidArgument, "resource ID is required")
	}

	if err := m.validateFlowLogResource(cfg.ResourceID, cfg.ResourceType); err != nil {
		return nil, err
	}

	trafficType := cfg.TrafficType
	if trafficType == "" {
		trafficType = TrafficTypeAll
	}

	id := idgen.GenerateID("fl-")
	fl := &flowLogData{
		ID:           id,
		ResourceID:   cfg.ResourceID,
		ResourceType: cfg.ResourceType,
		TrafficType:  trafficType,
		Status:       FlowLogStatusActive,
		CreatedAt:    m.opts.Clock.Now().Format(timeFormat),
		Tags:         copyTags(cfg.Tags),
	}
	m.flowLogs.Set(id, fl)

	info := toFlowLogInfo(fl)

	return &info, nil
}

// validateFlowLogResource checks that the target resource exists.
func (m *Mock) validateFlowLogResource(resourceID, resourceType string) error {
	switch resourceType {
	case "VPC":
		if !m.vpcs.Has(resourceID) {
			return errors.Newf(errors.NotFound, "vpc %q not found", resourceID)
		}
	case "Subnet":
		if !m.subnets.Has(resourceID) {
			return errors.Newf(errors.NotFound, "subnet %q not found", resourceID)
		}
	default:
		return errors.Newf(errors.InvalidArgument, "unsupported resource type %q", resourceType)
	}

	return nil
}

// DeleteFlowLog deletes the flow log with the given ID.
func (m *Mock) DeleteFlowLog(_ context.Context, id string) error {
	if !m.flowLogs.Delete(id) {
		return errors.Newf(errors.NotFound, "flow log %q not found", id)
	}

	return nil
}

// DescribeFlowLogs returns flow logs matching the given IDs, or all if empty.
func (m *Mock) DescribeFlowLogs(_ context.Context, ids []string) ([]driver.FlowLog, error) {
	return describeResources(m.flowLogs, ids, toFlowLogInfo), nil
}

// GetFlowLogRecords generates mock flow log records for the given flow log.
func (m *Mock) GetFlowLogRecords(
	_ context.Context, flowLogID string, limit int,
) ([]driver.FlowLogRecord, error) {
	fl, ok := m.flowLogs.Get(flowLogID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow log %q not found", flowLogID)
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
			Protocol:   "tcp",
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
