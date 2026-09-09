package eventbridgescheduler

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// CreateScheduleGroup creates a schedule group directly in the ACTIVE state with
// a stable arn and creation timestamp. The always-present default group cannot
// be recreated.
func (m *Mock) CreateScheduleGroup(_ context.Context, name string, tags map[string]string) (*driver.ScheduleGroup, error) {
	if name == "" {
		return nil, validation("schedule group name is required")
	}

	if name == driver.DefaultGroupName {
		return nil, conflict("schedule group %q already exists", name)
	}

	if m.groups.Has(name) {
		return nil, conflict("schedule group %q already exists", name)
	}

	now := m.now()
	group := driver.ScheduleGroup{
		Name:                 name,
		Arn:                  m.groupARN(name),
		State:                driver.GroupActive,
		CreationDate:         now,
		LastModificationDate: now,
		Tags:                 copyTags(tags),
	}
	m.groups.Set(name, group)

	out := copyGroup(&group)

	return &out, nil
}

// GetScheduleGroup returns a copy of the group. The default group is synthesized
// as ACTIVE when it was never explicitly created, matching the real service.
func (m *Mock) GetScheduleGroup(_ context.Context, name string) (*driver.ScheduleGroup, error) {
	name = resolveGroup(name)

	g, ok := m.groups.Get(name)
	if !ok {
		if name == driver.DefaultGroupName {
			return m.defaultGroup(), nil
		}

		return nil, notFound("schedule group %q does not exist", name)
	}

	out := copyGroup(&g)

	return &out, nil
}

// DeleteScheduleGroup removes a group and cascades to the schedules it contains,
// matching the real service where deleting a group deletes its schedules. The
// default group cannot be deleted.
func (m *Mock) DeleteScheduleGroup(_ context.Context, name string) error {
	if name == driver.DefaultGroupName {
		return validation("the default schedule group cannot be deleted")
	}

	if !m.groups.Has(name) {
		return notFound("schedule group %q does not exist", name)
	}

	prefix := name + "/"
	for _, key := range m.schedules.Keys() {
		if strings.HasPrefix(key, prefix) {
			m.schedules.Delete(key)
		}
	}

	m.groups.Delete(name)

	return nil
}

// ListScheduleGroups returns a deterministic page of group summaries ordered by
// name, filtered by name prefix. The synthesized default group is included so a
// caller that never created a group still sees it.
func (m *Mock) ListScheduleGroups(_ context.Context, f driver.GroupFilter) (groups []driver.ScheduleGroup, nextToken string, err error) {
	var matched []driver.ScheduleGroup

	if !m.groups.Has(driver.DefaultGroupName) && hasPrefix(driver.DefaultGroupName, f.NamePrefix) {
		matched = append(matched, *m.defaultGroup())
	}

	for _, g := range m.groups.SortedValues() {
		if hasPrefix(g.Name, f.NamePrefix) {
			matched = append(matched, g)
		}
	}

	start, end, next := paginate(len(matched), f.Page)

	out := make([]driver.ScheduleGroup, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyGroup(&matched[i]))
	}

	return out, next, nil
}

// defaultGroup synthesizes the always-present default group. Its timestamps are
// pinned once at construction (defaultGroupTime) so repeated reads of the
// unmaterialized default group are byte-stable; the group is not stored until a
// schedule or an explicit action materializes state.
func (m *Mock) defaultGroup() *driver.ScheduleGroup {
	return &driver.ScheduleGroup{
		Name:                 driver.DefaultGroupName,
		Arn:                  m.groupARN(driver.DefaultGroupName),
		State:                driver.GroupActive,
		CreationDate:         m.defaultGroupTime,
		LastModificationDate: m.defaultGroupTime,
	}
}
