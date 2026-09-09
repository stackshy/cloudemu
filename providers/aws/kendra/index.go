package kendra

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// defaultCapacityUnits is the capacity reported for an index that never set one:
// zero query and storage units, meaning the index uses its default capacity.
// Stored once at create, it never drifts an IaC plan.
//
//nolint:gochecknoglobals // static default document
var defaultCapacityUnits = json.RawMessage(`{"QueryCapacityUnits":0,"StorageCapacityUnits":0}`)

// validEditions is the set of index editions the API accepts.
//
//nolint:gochecknoglobals // static validation set
var validEditions = map[string]bool{
	driver.EditionDeveloper:       true,
	driver.EditionEnterprise:      true,
	driver.EditionGenAIEnterprise: true,
}

// CreateIndex provisions an index directly in the ACTIVE state with stable
// computed fields (id, status, createdAt). Real Kendra takes ~30 minutes to
// activate an index, so returning ACTIVE synchronously is what lets an IaC
// create waiter complete. Edition defaults to ENTERPRISE_EDITION and
// UserContextPolicy to ATTRIBUTE_FILTER, matching the real API.
func (m *Mock) CreateIndex(_ context.Context, in *driver.CreateIndexInput) (*driver.Index, error) {
	if in.Name == "" {
		return nil, validation("Name is required")
	}

	if in.RoleArn == "" {
		return nil, validation("RoleArn is required")
	}

	edition := in.Edition
	if edition == "" {
		edition = driver.EditionEnterprise
	}

	if !validEditions[edition] {
		return nil, validation("invalid Edition: %q", in.Edition)
	}

	userContext := in.UserContextPolicy
	if userContext == "" {
		userContext = driver.UserContextAttributeFilter
	}

	id := newIndexID()
	now := m.now()

	idx := driver.Index{
		ID:                                id,
		Name:                              in.Name,
		Edition:                           edition,
		RoleArn:                           in.RoleArn,
		Description:                       in.Description,
		Status:                            driver.IndexStatusActive,
		UserContextPolicy:                 userContext,
		ServerSideEncryptionConfiguration: copyRaw(in.ServerSideEncryptionConfiguration),
		CapacityUnits:                     copyRaw(defaultCapacityUnits),
		UserGroupResolutionConfiguration:  copyRaw(in.UserGroupResolutionConfiguration),
		UserTokenConfigurations:           copyRaw(in.UserTokenConfigurations),
		CreatedAt:                         now,
		UpdatedAt:                         now,
		Tags:                              copyTags(in.Tags),
	}

	m.indexes.Set(id, idx)

	out := copyIndex(&idx)

	return &out, nil
}

// DescribeIndex returns the index by id, or a ResourceNotFoundException.
func (m *Mock) DescribeIndex(_ context.Context, id string) (*driver.Index, error) {
	idx, ok := m.indexes.Get(id)
	if !ok {
		return nil, notFound("index with id %q does not exist", id)
	}

	out := copyIndex(&idx)

	return &out, nil
}

// UpdateIndex applies the supplied fields, leaving omitted parameters unchanged.
// The computed id, status and createdAt are preserved; updatedAt is bumped.
func (m *Mock) UpdateIndex(_ context.Context, in *driver.UpdateIndexInput) error {
	ok := m.indexes.Update(in.ID, func(i driver.Index) driver.Index {
		if in.Name != nil {
			i.Name = *in.Name
		}

		if in.RoleArn != nil {
			i.RoleArn = *in.RoleArn
		}

		if in.Description != nil {
			i.Description = *in.Description
		}

		if in.UserContextPolicy != nil {
			i.UserContextPolicy = *in.UserContextPolicy
		}

		if in.CapacityUnits != nil {
			i.CapacityUnits = copyRaw(in.CapacityUnits)
		}

		if in.DocumentMetadataConfigurationUpdates != nil {
			i.DocumentMetadataConfigurations = copyRaw(in.DocumentMetadataConfigurationUpdates)
		}

		if in.UserGroupResolutionConfiguration != nil {
			i.UserGroupResolutionConfiguration = copyRaw(in.UserGroupResolutionConfiguration)
		}

		if in.UserTokenConfigurations != nil {
			i.UserTokenConfigurations = copyRaw(in.UserTokenConfigurations)
		}

		i.UpdatedAt = m.now()

		return i
	})
	if !ok {
		return notFound("index with id %q does not exist", in.ID)
	}

	return nil
}

// DeleteIndex removes an index and cascades to its data sources: real Kendra
// deletes an index's data source connectors along with the index, so a client
// that lists data sources after the index is gone sees none.
func (m *Mock) DeleteIndex(_ context.Context, id string) error {
	if _, ok := m.indexes.Get(id); !ok {
		return notFound("index with id %q does not exist", id)
	}

	m.indexes.Delete(id)

	children := m.dataSources.SortedValues()
	for i := range children {
		if children[i].IndexID == id {
			m.dataSources.Delete(dataSourceKey(id, children[i].ID))
		}
	}

	return nil
}

// ListIndices returns a deterministic page of indexes ordered by id.
func (m *Mock) ListIndices(_ context.Context, page driver.Page) (indexes []driver.Index, nextToken string, err error) {
	stored := m.indexes.SortedValues()

	start, end, next := paginate(len(stored), page)

	out := make([]driver.Index, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyIndex(&stored[i]))
	}

	return out, next, nil
}
