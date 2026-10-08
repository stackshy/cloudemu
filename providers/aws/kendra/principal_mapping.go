package kendra

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// Documented principal mapping limits.
const (
	maxGroupIDLen      = 1024
	maxOrderingID      = 32535158400000
	maxMappingSummary  = 10
	maxGroupsPageLimit = 10
)

// principalMapping is the stored state of one group's user mapping: its members
// and the history of PUT / DELETE actions by ordering id.
type principalMapping struct {
	IndexID      string
	DataSourceID string
	GroupID      string
	GroupMembers json.RawMessage
	Summaries    []driver.OrderingSummary
}

func mappingKey(indexID, dataSourceID, groupID string) string {
	return indexID + "/" + dataSourceID + "/" + groupID
}

func (m *Mock) checkMappingTarget(indexID, dataSourceID, groupID string) error {
	if err := validateIndexID(indexID); err != nil {
		return err
	}

	if len(groupID) < 1 || len(groupID) > maxGroupIDLen {
		return validation("GroupId must have length between 1 and %d", maxGroupIDLen)
	}

	if dataSourceID != "" {
		if _, err := m.getDataSource(indexID, dataSourceID); err != nil {
			return err
		}

		return nil
	}

	_, err := m.getIndex(indexID)

	return err
}

func orderingOf(requested *int64, now int64) (int64, error) {
	if requested == nil {
		return now, nil
	}

	if *requested < 0 || *requested > maxOrderingID {
		return 0, validation("OrderingId must be between 0 and %d", int64(maxOrderingID))
	}

	return *requested, nil
}

// recordAction adds or replaces the summary for an ordering id, newest first, and
// trims the history to the ten the API reports.
func recordAction(p *principalMapping, s *driver.OrderingSummary) {
	for i := range p.Summaries {
		if p.Summaries[i].OrderingID == s.OrderingID {
			p.Summaries[i] = *s

			return
		}
	}

	p.Summaries = append(p.Summaries, *s)

	sort.Slice(p.Summaries, func(i, j int) bool { return p.Summaries[i].OrderingID > p.Summaries[j].OrderingID })

	if len(p.Summaries) > maxMappingSummary {
		p.Summaries = p.Summaries[:maxMappingSummary]
	}
}

// PutPrincipalMapping maps a group to its members. The action is recorded under
// its ordering id (default: the receive time in Unix milliseconds) and applies
// at once, reporting SUCCEEDED. An action whose ordering id is lower than the
// group's latest one is recorded but does not replace the members, so a late
// older action cannot override a newer one.
func (m *Mock) PutPrincipalMapping(_ context.Context, in *driver.PutPrincipalMappingInput) error {
	if err := m.checkMappingTarget(in.IndexID, in.DataSourceID, in.GroupID); err != nil {
		return err
	}

	if len(in.GroupMembers) == 0 {
		return validation("GroupMembers is required")
	}

	if in.RoleArn != "" {
		if err := validateRoleArn(in.RoleArn); err != nil {
			return err
		}
	}

	now := m.now()

	ordering, err := orderingOf(in.OrderingID, now.UnixMilli())
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return err
	}

	key := mappingKey(in.IndexID, in.DataSourceID, in.GroupID)
	p, _ := m.mappings.Get(key)

	if p.GroupID == "" {
		p = principalMapping{IndexID: in.IndexID, DataSourceID: in.DataSourceID, GroupID: in.GroupID}
	}

	if len(p.Summaries) == 0 || ordering >= p.Summaries[0].OrderingID {
		p.GroupMembers = copyRaw(in.GroupMembers)
	}

	recordAction(&p, &driver.OrderingSummary{
		OrderingID: ordering, Status: driver.MappingSucceeded, ReceivedAt: now, LastUpdatedAt: now,
	})
	m.mappings.Set(key, p)

	return nil
}

// DeletePrincipalMapping removes a group's mapping. The action is recorded under
// its ordering id and reports DELETED; a delete older than the group's latest
// action is recorded but does not remove the members.
func (m *Mock) DeletePrincipalMapping(_ context.Context, in *driver.DeletePrincipalMappingInput) error {
	if err := m.checkMappingTarget(in.IndexID, in.DataSourceID, in.GroupID); err != nil {
		return err
	}

	now := m.now()

	ordering, err := orderingOf(in.OrderingID, now.UnixMilli())
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireActiveIndex(in.IndexID); err != nil {
		return err
	}

	key := mappingKey(in.IndexID, in.DataSourceID, in.GroupID)

	p, ok := m.mappings.Get(key)
	if !ok {
		return nil
	}

	if ordering >= p.Summaries[0].OrderingID {
		p.GroupMembers = nil
	}

	recordAction(&p, &driver.OrderingSummary{
		OrderingID: ordering, Status: driver.MappingDeleted, ReceivedAt: now, LastUpdatedAt: now,
	})
	m.mappings.Set(key, p)

	return nil
}

// DescribePrincipalMapping reports the processing history (up to ten ordering
// ids, newest first) of a group's mapping actions.
func (m *Mock) DescribePrincipalMapping(
	_ context.Context, indexID, dataSourceID, groupID string,
) (*driver.PrincipalMappingDescription, error) {
	if err := m.checkMappingTarget(indexID, dataSourceID, groupID); err != nil {
		return nil, err
	}

	out := &driver.PrincipalMappingDescription{
		IndexID: indexID, DataSourceID: dataSourceID, GroupID: groupID, Summaries: []driver.OrderingSummary{},
	}

	if p, ok := m.mappings.Get(mappingKey(indexID, dataSourceID, groupID)); ok {
		out.Summaries = append(out.Summaries, p.Summaries...)
	}

	return out, nil
}

// ListGroupsOlderThanOrderingID lists the groups whose latest mapping action has
// an ordering id lower than the given one and is a PUT (a deleted mapping is not
// listed), ordered by group id.
func (m *Mock) ListGroupsOlderThanOrderingID(
	_ context.Context, in *driver.ListGroupsInput,
) (groups []driver.GroupSummary, nextToken string, err error) {
	if cerr := m.checkListGroups(in); cerr != nil {
		return nil, "", cerr
	}

	matched := []driver.GroupSummary{}

	for _, p := range m.mappings.SortedValues() {
		if p.IndexID != in.IndexID || p.DataSourceID != in.DataSourceID || len(p.Summaries) == 0 {
			continue
		}

		latest := p.Summaries[0]
		if latest.Status == driver.MappingDeleted || latest.OrderingID >= in.OrderingID {
			continue
		}

		matched = append(matched, driver.GroupSummary{GroupID: p.GroupID, OrderingID: latest.OrderingID})
	}

	start, end, next, err := m.paginate("groups/"+in.IndexID+"/"+in.DataSourceID, len(matched), in.Page, maxGroupsPageLimit)
	if err != nil {
		return nil, "", err
	}

	return matched[start:end], next, nil
}

// checkListGroups validates the index, optional data source and ordering id.
func (m *Mock) checkListGroups(in *driver.ListGroupsInput) error {
	if err := validateIndexID(in.IndexID); err != nil {
		return err
	}

	if in.OrderingID < 0 || in.OrderingID > maxOrderingID {
		return validation("OrderingId must be between 0 and %d", int64(maxOrderingID))
	}

	if in.DataSourceID != "" {
		_, err := m.getDataSource(in.IndexID, in.DataSourceID)

		return err
	}

	_, err := m.getIndex(in.IndexID)

	return err
}
