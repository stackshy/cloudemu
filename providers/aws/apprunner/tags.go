package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// TagResource merges tags onto any App Runner resource identified by its ARN.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags []driver.Tag) error {
	return m.mutateTags(resourceArn, func(existing []driver.Tag) []driver.Tag {
		for _, t := range tags {
			existing = upsertTag(existing, t)
		}

		return existing
	})
}

// UntagResource removes tags by key from any App Runner resource.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	remove := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
		remove[k] = true
	}

	return m.mutateTags(resourceArn, func(existing []driver.Tag) []driver.Tag {
		kept := existing[:0]

		for _, t := range existing {
			if !remove[t.Key] {
				kept = append(kept, t)
			}
		}

		return kept
	})
}

// ListTagsForResource returns a copy of a resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) ([]driver.Tag, error) {
	if s, ok := m.services.Get(resourceArn); ok {
		return copyTags(s.Tags), nil
	}

	if c, ok := m.autoScaling.Get(resourceArn); ok {
		return copyTags(c.Tags), nil
	}

	if c, ok := m.connections.Get(resourceArn); ok {
		return copyTags(c.Tags), nil
	}

	if c, ok := m.vpcConnectors.Get(resourceArn); ok {
		return copyTags(c.Tags), nil
	}

	if c, ok := m.observability.Get(resourceArn); ok {
		return copyTags(c.Tags), nil
	}

	return nil, notFound("resource %q does not exist", resourceArn)
}

// mutateTags applies mutate to the tags of whichever store holds resourceArn.
func (m *Mock) mutateTags(resourceArn string, mutate func([]driver.Tag) []driver.Tag) error {
	if m.services.Update(resourceArn, func(s driver.Service) driver.Service {
		s.Tags = mutate(copyTags(s.Tags))

		return s
	}) {
		return nil
	}

	if m.autoScaling.Update(resourceArn, func(c driver.AutoScalingConfiguration) driver.AutoScalingConfiguration {
		c.Tags = mutate(copyTags(c.Tags))

		return c
	}) {
		return nil
	}

	if m.connections.Update(resourceArn, func(c driver.Connection) driver.Connection {
		c.Tags = mutate(copyTags(c.Tags))

		return c
	}) {
		return nil
	}

	return m.mutateTagsRest(resourceArn, mutate)
}

// mutateTagsRest handles the remaining stores, keeping mutateTags within the
// cyclomatic-complexity budget.
func (m *Mock) mutateTagsRest(resourceArn string, mutate func([]driver.Tag) []driver.Tag) error {
	if m.vpcConnectors.Update(resourceArn, func(c driver.VpcConnector) driver.VpcConnector {
		c.Tags = mutate(copyTags(c.Tags))

		return c
	}) {
		return nil
	}

	if m.observability.Update(resourceArn, func(c driver.ObservabilityConfiguration) driver.ObservabilityConfiguration {
		c.Tags = mutate(copyTags(c.Tags))

		return c
	}) {
		return nil
	}

	return notFound("resource %q does not exist", resourceArn)
}

// upsertTag sets a tag's value, replacing any existing tag with the same key.
func upsertTag(tags []driver.Tag, t driver.Tag) []driver.Tag {
	for i := range tags {
		if tags[i].Key == t.Key {
			tags[i].Value = t.Value

			return tags
		}
	}

	return append(tags, t)
}
