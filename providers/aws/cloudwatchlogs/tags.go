package cloudwatchlogs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
)

// TagLogGroup adds or overwrites tags on a log group (CloudWatch Logs
// TagResource / TagLogGroup).
func (m *Mock) TagLogGroup(_ context.Context, name string, tags map[string]string) error {
	g, ok := m.groups.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	if g.info.Tags == nil {
		g.info.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		g.info.Tags[k] = v
	}

	return nil
}

// UntagLogGroup removes tags by key from a log group.
func (m *Mock) UntagLogGroup(_ context.Context, name string, keys []string) error {
	g, ok := m.groups.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	for _, k := range keys {
		delete(g.info.Tags, k)
	}

	return nil
}

// ListLogGroupTags returns a log group's tags.
func (m *Mock) ListLogGroupTags(_ context.Context, name string) (map[string]string, error) {
	g, ok := m.groups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "log group %q not found", name)
	}

	out := make(map[string]string, len(g.info.Tags))
	for k, v := range g.info.Tags {
		out[k] = v
	}

	return out, nil
}
