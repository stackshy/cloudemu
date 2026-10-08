package kendra

import (
	"context"
	"encoding/json"
	"strings"

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

// Documented request limits shared by the create calls.
const (
	maxClientTokenLen = 100
	maxDescriptionLen = 1000
	maxTags           = 200
)

// validateCommon checks the ClientToken, Description and Tags limits every
// create call shares.
func validateCommon(token, description string, tags []driver.Tag) error {
	if token != "" && len(token) > maxClientTokenLen {
		return validation("ClientToken must have length between 1 and %d", maxClientTokenLen)
	}

	if len(description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	if len(tags) > maxTags {
		return validation("Tags must have at most %d items", maxTags)
	}

	return nil
}

// CreateIndex provisions an index with stable computed fields (id, status,
// createdAt). Real Kendra takes ~30 minutes to activate an index, so the index
// is ACTIVE at once by default (what lets an IaC create waiter complete); under
// async settling it reports CREATING for a short window first. Edition defaults
// to ENTERPRISE_EDITION and UserContextPolicy to ATTRIBUTE_FILTER, matching the
// real API. A repeated ClientToken returns the index the first call created.
func (m *Mock) CreateIndex(_ context.Context, in *driver.CreateIndexInput) (*driver.Index, error) {
	edition, userContext, err := validateCreateIndex(in)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.indexByToken(in.ClientToken); ok {
		out := m.viewIndex(&existing)

		return &out, nil
	}

	id := newUUID()
	now := m.now()

	idx := driver.Index{
		ID:                                id,
		ClientToken:                       in.ClientToken,
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
	m.beginSettle(id, driver.IndexStatusCreating)

	out := m.viewIndex(&idx)

	return &out, nil
}

// indexByToken returns the index a previous create made with the same client
// token. An empty token never matches, and a token whose index was deleted
// starts a new create, as the first call's resource no longer exists.
func (m *Mock) indexByToken(token string) (driver.Index, bool) {
	if token == "" {
		return driver.Index{}, false
	}

	stored := m.indexes.SortedValues()
	for i := range stored {
		if stored[i].ClientToken == token {
			return stored[i], true
		}
	}

	return driver.Index{}, false
}

// viewIndex returns an alias-free copy of an index with its status overlaid by
// any settle window.
func (m *Mock) viewIndex(i *driver.Index) driver.Index {
	out := copyIndex(i)
	out.Status = m.settleStatus(i.ID, i.Status)

	return out
}

// getIndex returns the stored index, or the exception for a malformed or unknown
// IndexId.
func (m *Mock) getIndex(id string) (driver.Index, error) {
	if err := validateIndexID(id); err != nil {
		return driver.Index{}, err
	}

	idx, ok := m.indexes.Get(id)
	if !ok {
		return driver.Index{}, notFound("index with id %q does not exist", id)
	}

	return idx, nil
}

// requireActiveIndex returns the index when it is ACTIVE, a ConflictException
// when it is still settling, and the not-found / validation error otherwise.
func (m *Mock) requireActiveIndex(id string) error {
	idx, err := m.getIndex(id)
	if err != nil {
		return err
	}

	if status := m.settleStatus(id, idx.Status); status != driver.IndexStatusActive {
		return conflict("index %q is %s; try again when it is ACTIVE", id, status)
	}

	return nil
}

// DescribeIndex returns the index by id, with its status and statistics, or a
// ResourceNotFoundException.
func (m *Mock) DescribeIndex(_ context.Context, id string) (*driver.Index, error) {
	idx, err := m.getIndex(id)
	if err != nil {
		return nil, err
	}

	out := m.viewIndex(&idx)
	out.Statistics = m.indexStatistics(id)

	return &out, nil
}

// indexStatistics counts the text documents the index holds and their size.
func (m *Mock) indexStatistics(indexID string) *driver.IndexStatistics {
	stats := &driver.IndexStatistics{}

	docs := m.documentsOf(indexID)
	for i := range docs {
		stats.IndexedTextDocuments++
		stats.IndexedTextBytes += int64(len(docs[i].Text))
	}

	return stats
}

// validateCreateIndex applies CreateIndex's input rules and returns the edition and
// user context policy to store, with their defaults filled in.
func validateCreateIndex(in *driver.CreateIndexInput) (edition, userContext string, err error) {
	if err := validateName(in.Name, maxIndexNameLen); err != nil {
		return "", "", err
	}

	if in.RoleArn == "" {
		return "", "", validation("RoleArn is required")
	}

	if err := validateRoleArn(in.RoleArn); err != nil {
		return "", "", err
	}

	edition = in.Edition
	if edition == "" {
		edition = driver.EditionEnterprise
	}

	if !validEditions[edition] {
		return "", "", validation("invalid Edition: %q", in.Edition)
	}

	userContext = in.UserContextPolicy
	if userContext == "" {
		userContext = driver.UserContextAttributeFilter
	}

	if userContext != driver.UserContextAttributeFilter && userContext != driver.UserContextUserToken {
		return "", "", validation("invalid UserContextPolicy: %q", in.UserContextPolicy)
	}

	return edition, userContext, validateCommon(in.ClientToken, in.Description, in.Tags)
}

// UpdateIndex applies the supplied fields, leaving omitted parameters unchanged.
// The computed id, status and createdAt are preserved; updatedAt is bumped. An
// index that is not ACTIVE cannot be updated (ConflictException).
func (m *Mock) UpdateIndex(_ context.Context, in *driver.UpdateIndexInput) error {
	if err := validateUpdateIndex(in); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.ID); err != nil {
		return err
	}

	m.indexes.Update(in.ID, func(i driver.Index) driver.Index {
		applyIndexUpdate(&i, in)
		i.UpdatedAt = m.now()

		return i
	})

	m.beginSettle(in.ID, driver.IndexStatusUpdating)

	return nil
}

// validateUpdateIndex applies UpdateIndex's input rules.
func validateUpdateIndex(in *driver.UpdateIndexInput) error {
	if in.Name != nil {
		if err := validateName(*in.Name, maxIndexNameLen); err != nil {
			return err
		}
	}

	if in.RoleArn != nil {
		if err := validateRoleArn(*in.RoleArn); err != nil {
			return err
		}
	}

	if in.UserContextPolicy != nil && *in.UserContextPolicy != driver.UserContextAttributeFilter &&
		*in.UserContextPolicy != driver.UserContextUserToken {
		return validation("invalid UserContextPolicy: %q", *in.UserContextPolicy)
	}

	if in.Description != nil && len(*in.Description) > maxDescriptionLen {
		return validation("Description must have length between 0 and %d", maxDescriptionLen)
	}

	return nil
}

// applyIndexUpdate overlays the supplied members of an update onto an index.
func applyIndexUpdate(i *driver.Index, in *driver.UpdateIndexInput) {
	setIfSet(&i.Name, in.Name)
	setIfSet(&i.RoleArn, in.RoleArn)
	setIfSet(&i.Description, in.Description)
	setIfSet(&i.UserContextPolicy, in.UserContextPolicy)
	setRawIfSet(&i.CapacityUnits, in.CapacityUnits)
	setRawIfSet(&i.DocumentMetadataConfigurations, in.DocumentMetadataConfigurationUpdates)
	setRawIfSet(&i.UserGroupResolutionConfiguration, in.UserGroupResolutionConfiguration)
	setRawIfSet(&i.UserTokenConfigurations, in.UserTokenConfigurations)
}

// setIfSet assigns *src to dst when src is non-nil.
func setIfSet(dst, src *string) {
	if src != nil {
		*dst = *src
	}
}

// setRawIfSet copies a raw JSON member when it was supplied.
func setRawIfSet(dst *json.RawMessage, src json.RawMessage) {
	if src != nil {
		*dst = copyRaw(src)
	}
}

// DeleteIndex removes an index and cascades to everything it owns: data sources
// and their sync jobs, documents, FAQs, thesauri, block lists, experiences,
// access control configurations, featured results sets, principal mappings and
// the suggestions configuration. An index that is not ACTIVE cannot be deleted
// (ConflictException).
func (m *Mock) DeleteIndex(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(id); err != nil {
		return err
	}

	m.indexes.Delete(id)
	m.settling.Clear(id)
	m.cascadeIndex(id)

	return nil
}

// cascadeIndex deletes every resource that belongs to an index. The caller holds
// m.mu.
func (m *Mock) cascadeIndex(id string) {
	prefix := id + "/"

	for _, key := range m.dataSources.Keys() {
		if strings.HasPrefix(key, prefix) {
			m.dataSources.Delete(key)
			m.settling.Clear(key)
		}
	}

	drop := m.settling.Clear

	deleteWithPrefix(m.documents, prefix, func(k string) { m.settling.Clear(docSettleKey(k)) })
	deleteWithPrefix(m.syncJobs, prefix, drop)
	deleteWithPrefix(m.faqs, prefix, drop)
	deleteWithPrefix(m.thesauri, prefix, drop)
	deleteWithPrefix(m.blockLists, prefix, drop)
	deleteWithPrefix(m.experiences, prefix, drop)
	deleteWithPrefix(m.accessControls, prefix, drop)
	deleteWithPrefix(m.featured, prefix, drop)
	deleteWithPrefix(m.mappings, prefix, drop)
	m.suggestions.Delete(id)
	m.settling.Clear(suggestionsKey(id))
	m.queryLog.clear(id)
}

// ListIndices returns a deterministic page of indexes ordered by id.
func (m *Mock) ListIndices(_ context.Context, page driver.Page) (indexes []driver.Index, nextToken string, err error) {
	stored := m.indexes.SortedValues()

	start, end, next, err := m.paginate("indices", len(stored), page, maxPageSize)
	if err != nil {
		return nil, "", err
	}

	out := make([]driver.Index, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, m.viewIndex(&stored[i]))
	}

	return out, next, nil
}
