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
	if err := validateFlow(cfg); err != nil {
		return nil, err
	}

	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	id := newID(m.flows)
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
	m.putTags(flow.ARN, cfg.Tags)

	result := cloneFlow(flow)

	return &result, nil
}

// GetFlow returns a flow by identifier.
func (m *Mock) GetFlow(_ context.Context, id string) (*driver.Flow, error) {
	flow, ok := m.flows.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	result := cloneFlow(flow)

	return &result, nil
}

// ListFlows lists one page of flows.
func (m *Mock) ListFlows(_ context.Context, page driver.Page) ([]driver.Flow, string, error) {
	all := m.flows.SortedValues()
	out := make([]driver.Flow, 0, len(all))

	for _, f := range all {
		out = append(out, cloneFlow(f))
	}

	return paginate(out, page)
}

// UpdateFlow updates a flow's mutable fields, resetting it to NotPrepared.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateFlow(_ context.Context, id string, cfg driver.FlowConfig) (*driver.Flow, error) {
	if err := validateFlow(cfg); err != nil {
		return nil, err
	}

	flow, ok := m.flows.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	updated := *flow
	updated.Name = cfg.Name
	updated.ExecutionRoleArn = cfg.ExecutionRoleArn
	updated.Description = cfg.Description
	updated.Status = driver.FlowNotPrepared
	updated.UpdatedAt = m.now()

	if len(cfg.Definition) != 0 {
		updated.Definition = copyRaw(cfg.Definition)
	}

	m.flows.Set(id, &updated)

	result := cloneFlow(&updated)

	return &result, nil
}

// DeleteFlow deletes a flow and its tags and returns its identifier.
func (m *Mock) DeleteFlow(_ context.Context, id string) (string, error) {
	flow, ok := m.flows.Get(id)
	if !ok {
		return "", errors.Newf(errors.NotFound, "flow %q not found", id)
	}

	m.flows.Delete(id)
	m.dropTags(flow.ARN)

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

	result := cloneFlow(&updated)

	return &result, nil
}

// validateFlow checks the members CreateFlow and UpdateFlow require.
//
//nolint:gocritic // cfg matches the driver interface signature.
func validateFlow(cfg driver.FlowConfig) error {
	var v violations

	v.required("name", cfg.Name == "")
	v.required("executionRoleArn", cfg.ExecutionRoleArn == "")

	return v.err()
}

// cloneFlow returns a value copy whose Definition does not alias the stored
// flow, so callers can't mutate internal state via the result.
func cloneFlow(f *driver.Flow) driver.Flow {
	out := *f
	out.Definition = copyRaw(f.Definition)

	return out
}
