package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreateFlow creates a flow in the NotPrepared state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateFlow(_ context.Context, cfg driver.FlowConfig) (*driver.Flow, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "name is required")
	case cfg.ExecutionRoleArn == "":
		return nil, errors.New(errors.InvalidArgument, "executionRoleArn is required")
	}

	id := idgen.GenerateID("FLOW")
	now := m.now()
	flow := &driver.Flow{
		ID:                       id,
		ARN:                      idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "flow/"+id),
		Name:                     cfg.Name,
		ExecutionRoleArn:         cfg.ExecutionRoleArn,
		Description:              cfg.Description,
		Status:                   driver.FlowNotPrepared,
		Version:                  driver.DraftVersion,
		CustomerEncryptionKeyArn: cfg.CustomerEncryptionKeyArn,
		Definition:               copyRaw(cfg.Definition),
		CreatedAt:                now,
		UpdatedAt:                now,
	}
	m.flows.Set(id, flow)

	result := *flow

	return &result, nil
}

// GetFlow returns a flow by identifier.
func (m *Mock) GetFlow(_ context.Context, id string) (*driver.Flow, error) {
	flow, ok := m.flows.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	result := *flow

	return &result, nil
}

// ListFlows lists all flows.
func (m *Mock) ListFlows(_ context.Context) ([]driver.Flow, error) {
	all := m.flows.SortedValues()
	out := make([]driver.Flow, 0, len(all))

	for _, f := range all {
		out = append(out, *f)
	}

	return out, nil
}

// UpdateFlow updates a flow's mutable fields, resetting it to NotPrepared.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateFlow(_ context.Context, id string, cfg driver.FlowConfig) (*driver.Flow, error) {
	flow, ok := m.flows.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	updated := *flow
	updated.Name = orDefault(cfg.Name, flow.Name)
	updated.ExecutionRoleArn = orDefault(cfg.ExecutionRoleArn, flow.ExecutionRoleArn)
	updated.Description = cfg.Description
	updated.Status = driver.FlowNotPrepared
	updated.UpdatedAt = m.now()

	if len(cfg.Definition) != 0 {
		updated.Definition = copyRaw(cfg.Definition)
	}

	m.flows.Set(id, &updated)

	result := updated

	return &result, nil
}

// DeleteFlow deletes a flow and returns its identifier.
func (m *Mock) DeleteFlow(_ context.Context, id string) (string, error) {
	if !m.flows.Has(id) {
		return "", errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	m.flows.Delete(id)

	return id, nil
}

// PrepareFlow prepares a flow, transitioning it to Prepared.
func (m *Mock) PrepareFlow(_ context.Context, id string) (*driver.Flow, error) {
	flow, ok := m.flows.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	updated := *flow
	updated.Status = driver.FlowPrepared
	updated.UpdatedAt = m.now()
	m.flows.Set(id, &updated)

	result := updated

	return &result, nil
}
