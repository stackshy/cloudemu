package eventbridgescheduler

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/eventbridgescheduler/driver"
)

// TagResource adds or overwrites tags on a schedule group.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	name, err := groupNameFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateGroupTags(name, func(t map[string]string) {
		for k, v := range tags {
			t[k] = v
		}
	})
}

// UntagResource removes tags by key from a schedule group.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	name, err := groupNameFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateGroupTags(name, func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	})
}

// ListTagsForResource returns a copy of a schedule group's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	name, err := groupNameFromARN(resourceArn)
	if err != nil {
		return nil, err
	}

	g, ok := m.groups.Get(name)
	if !ok {
		if name == driver.DefaultGroupName {
			return map[string]string{}, nil
		}

		return nil, notFound("schedule group %q does not exist", name)
	}

	return copyTags(g.Tags), nil
}

// arnFields is the number of colon-separated fields in a Scheduler ARN
// (arn:aws:scheduler:{region}:{acct}:schedule-group/{name}); the sixth is the
// resource.
const arnFields = 6

// groupResourcePrefix is the resource-type segment of a schedule-group ARN.
const groupResourcePrefix = "schedule-group/"

// groupNameFromARN extracts the group name from a schedule-group ARN. Tags are
// scoped to schedule groups, so any other Scheduler ARN (a schedule) is
// rejected.
func groupNameFromARN(resourceArn string) (string, error) {
	if !strings.Contains(resourceArn, arnMarker) {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	parts := strings.SplitN(resourceArn, ":", arnFields)
	if len(parts) < arnFields {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	resource := parts[arnFields-1]
	if !strings.HasPrefix(resource, groupResourcePrefix) {
		return "", validation("tags are supported only on schedule groups: %q", resourceArn)
	}

	name := strings.TrimPrefix(resource, groupResourcePrefix)
	if name == "" {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	return name, nil
}

// updateGroupTags mutates a group's tags in place. The default group is
// materialized on first tag so its tags persist, matching the real service where
// the default group is taggable.
func (m *Mock) updateGroupTags(name string, mutate func(map[string]string)) error {
	if !m.groups.Has(name) {
		if name != driver.DefaultGroupName {
			return notFound("schedule group %q does not exist", name)
		}

		m.groups.Set(name, *m.defaultGroup())
	}

	m.groups.Update(name, func(g driver.ScheduleGroup) driver.ScheduleGroup {
		if g.Tags == nil {
			g.Tags = map[string]string{}
		}

		mutate(g.Tags)

		return g
	})

	return nil
}
