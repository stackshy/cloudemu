package appflow

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// CreateFlow provisions a new flow with a stable flowArn, an Active status, and
// create/update timestamps. The configuration blocks are carried verbatim so a
// DescribeFlow reflects exactly what the caller sent. A flow name already in use
// yields a ConflictException.
func (m *Mock) CreateFlow(_ context.Context, in *driver.CreateFlowInput) (*driver.Flow, error) {
	if in.FlowName == "" {
		return nil, validation("flowName is required")
	}

	if m.flows.Has(in.FlowName) {
		return nil, conflict("flow %s already exists", in.FlowName)
	}

	src, dst, trigger := summarizeConnectorTypes(in.Extra)
	now := m.now()

	flow := driver.Flow{
		FlowName:                 in.FlowName,
		FlowArn:                  m.flowARN(in.FlowName),
		Description:              in.Description,
		KmsArn:                   in.KmsArn,
		FlowStatus:               driver.FlowStatusActive,
		SourceConnectorType:      src,
		DestinationConnectorType: dst,
		TriggerType:              trigger,
		CreatedAt:                now,
		LastUpdatedAt:            now,
		CreatedBy:                m.createdBy(),
		LastUpdatedBy:            m.createdBy(),
		Tags:                     copyTags(in.Tags),
		Extra:                    copyExtra(in.Extra),
	}

	m.flows.Set(in.FlowName, flow)

	out := copyFlow(&flow)

	return &out, nil
}

// DescribeFlow returns a copy of the flow. The stored flowArn, flowStatus,
// createdAt, and createdBy are returned unchanged so repeated reads never drift.
func (m *Mock) DescribeFlow(_ context.Context, flowName string) (*driver.Flow, error) {
	f, ok := m.flows.Get(flowName)
	if !ok {
		return nil, notFound("flow %s not found", flowName)
	}

	out := copyFlow(&f)

	return &out, nil
}

// UpdateFlow replaces the mutable configuration of a flow while preserving the
// computed flowArn, flowStatus, createdAt, createdBy, kmsArn, and tags. It bumps
// lastUpdatedAt and refreshes the derived connector/trigger summary.
func (m *Mock) UpdateFlow(_ context.Context, in *driver.UpdateFlowInput) (*driver.Flow, error) {
	var updated driver.Flow

	ok := m.flows.Update(in.FlowName, func(f driver.Flow) driver.Flow {
		src, dst, trigger := summarizeConnectorTypes(in.Extra)

		f.Description = in.Description
		f.Extra = copyExtra(in.Extra)
		f.SourceConnectorType = src
		f.DestinationConnectorType = dst
		f.TriggerType = trigger
		f.LastUpdatedAt = m.now()
		f.LastUpdatedBy = m.createdBy()

		updated = f

		return f
	})
	if !ok {
		return nil, notFound("flow %s not found", in.FlowName)
	}

	out := copyFlow(&updated)

	return &out, nil
}

// DeleteFlow removes a flow. forceDelete is accepted for wire compatibility; the
// emulator deletes unconditionally.
func (m *Mock) DeleteFlow(_ context.Context, flowName string, _ bool) error {
	if !m.flows.Delete(flowName) {
		return notFound("flow %s not found", flowName)
	}

	return nil
}

// ListFlows returns a deterministic, deep-copied page of the flows.
func (m *Mock) ListFlows(_ context.Context, page driver.Page) ([]driver.Flow, string, error) {
	stored := m.flows.SortedValues()

	all := make([]driver.Flow, 0, len(stored))
	for i := range stored {
		all = append(all, copyFlow(&stored[i]))
	}

	start, end, next := paginate(len(all), page)

	return all[start:end], next, nil
}

// StartFlow moves a flow to Active and returns a fresh execution id. No data is
// transferred: this is a control-plane-only surface.
func (m *Mock) StartFlow(_ context.Context, flowName string) (*driver.Flow, string, error) {
	var started driver.Flow

	ok := m.flows.Update(flowName, func(f driver.Flow) driver.Flow {
		f.FlowStatus = driver.FlowStatusActive
		started = f

		return f
	})
	if !ok {
		return nil, "", notFound("flow %s not found", flowName)
	}

	out := copyFlow(&started)

	return &out, idgen.GenerateID(""), nil
}

// StopFlow suspends a flow. Only OnDemand flows cannot be stopped in real
// AppFlow; the emulator suspends any flow so the status transition round-trips.
func (m *Mock) StopFlow(_ context.Context, flowName string) (*driver.Flow, error) {
	var stopped driver.Flow

	ok := m.flows.Update(flowName, func(f driver.Flow) driver.Flow {
		f.FlowStatus = driver.FlowStatusSuspended
		stopped = f

		return f
	})
	if !ok {
		return nil, notFound("flow %s not found", flowName)
	}

	out := copyFlow(&stopped)

	return &out, nil
}
