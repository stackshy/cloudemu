package bedrockagent

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// CreateKnowledgeBase creates a knowledge base in the ACTIVE state.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) CreateKnowledgeBase(_ context.Context, cfg driver.KnowledgeBaseConfig) (*driver.KnowledgeBase, error) {
	switch {
	case cfg.Name == "":
		return nil, errors.New(errors.InvalidArgument, "name is required")
	case cfg.RoleArn == "":
		return nil, errors.New(errors.InvalidArgument, "roleArn is required")
	case len(cfg.KnowledgeBaseConfiguration) == 0:
		return nil, errors.New(errors.InvalidArgument, "knowledgeBaseConfiguration is required")
	}

	id := idgen.GenerateID("KB")
	now := m.now()
	kb := &driver.KnowledgeBase{
		ID:                         id,
		ARN:                        idgen.AWSARN("bedrock", m.opts.Region, m.opts.AccountID, "knowledge-base/"+id),
		Name:                       cfg.Name,
		RoleArn:                    cfg.RoleArn,
		Description:                cfg.Description,
		Status:                     driver.KnowledgeBaseActive,
		KnowledgeBaseConfiguration: copyRaw(cfg.KnowledgeBaseConfiguration),
		StorageConfiguration:       copyRaw(cfg.StorageConfiguration),
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	m.knowledge.Set(id, kb)

	result := cloneKnowledgeBase(kb)

	return &result, nil
}

// GetKnowledgeBase returns a knowledge base by ID.
func (m *Mock) GetKnowledgeBase(_ context.Context, id string) (*driver.KnowledgeBase, error) {
	kb, ok := m.knowledge.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "knowledge base %q not found", id)
	}

	result := cloneKnowledgeBase(kb)

	return &result, nil
}

// ListKnowledgeBases lists all knowledge bases.
func (m *Mock) ListKnowledgeBases(_ context.Context) ([]driver.KnowledgeBase, error) {
	all := m.knowledge.SortedValues()
	out := make([]driver.KnowledgeBase, 0, len(all))

	for _, kb := range all {
		out = append(out, cloneKnowledgeBase(kb))
	}

	return out, nil
}

// UpdateKnowledgeBase updates a knowledge base's mutable fields.
//
//nolint:gocritic // cfg matches the driver interface signature; copied once on entry.
func (m *Mock) UpdateKnowledgeBase(_ context.Context, id string, cfg driver.KnowledgeBaseConfig) (*driver.KnowledgeBase, error) {
	kb, ok := m.knowledge.Get(id)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "knowledge base %q not found", id)
	}

	updated := *kb
	updated.Name = orDefault(cfg.Name, kb.Name)
	updated.RoleArn = orDefault(cfg.RoleArn, kb.RoleArn)
	updated.Description = cfg.Description
	updated.UpdatedAt = m.now()

	if len(cfg.KnowledgeBaseConfiguration) != 0 {
		updated.KnowledgeBaseConfiguration = copyRaw(cfg.KnowledgeBaseConfiguration)
	}

	if len(cfg.StorageConfiguration) != 0 {
		updated.StorageConfiguration = copyRaw(cfg.StorageConfiguration)
	}

	m.knowledge.Set(id, &updated)

	result := cloneKnowledgeBase(&updated)

	return &result, nil
}

// DeleteKnowledgeBase deletes a knowledge base and, cascading like real AWS,
// every data source and ingestion job that belongs to it.
func (m *Mock) DeleteKnowledgeBase(_ context.Context, id string) (string, error) {
	if !m.knowledge.Has(id) {
		return "", errors.Newf(errors.NotFound, "knowledge base %q not found", id)
	}

	m.knowledge.Delete(id)
	m.deleteDataSourcesForKnowledgeBase(id)
	m.deleteJobsForKnowledgeBase(id)

	return statusDeleting, nil
}

// cloneKnowledgeBase returns a value copy whose RawMessage config fields do not
// alias the stored knowledge base, so callers can't mutate internal state.
func cloneKnowledgeBase(kb *driver.KnowledgeBase) driver.KnowledgeBase {
	out := *kb
	out.KnowledgeBaseConfiguration = copyRaw(kb.KnowledgeBaseConfiguration)
	out.StorageConfiguration = copyRaw(kb.StorageConfiguration)

	return out
}
