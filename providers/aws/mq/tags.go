package mq

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// CreateTags adds or overwrites tags on a broker or configuration.
func (m *Mock) CreateTags(_ context.Context, resourceArn string, tags map[string]string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) {
		for k, v := range tags {
			t[k] = v
		}
	})
}

// DeleteTags removes tags by key from a broker or configuration.
func (m *Mock) DeleteTags(_ context.Context, resourceArn string, tagKeys []string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	})
}

// ListTags returns a copy of a broker's or configuration's tags.
func (m *Mock) ListTags(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, id := resourceRefFromARN(resourceArn)

	switch kind {
	case kindBroker:
		b, ok := m.brokers.Get(id)
		if !ok {
			return nil, notFound("Broker %s not found", id)
		}

		return copyTags(b.Tags), nil
	case kindConfiguration:
		c, ok := m.configs.Get(id)
		if !ok {
			return nil, notFound("Configuration %s not found", id)
		}

		return copyTags(c.Tags), nil
	default:
		return nil, badRequest("invalid resource ARN: %q", resourceArn)
	}
}

// mutateTags applies mutate to the tag map of the broker or configuration the
// ARN identifies, under the store lock.
func (m *Mock) mutateTags(resourceArn string, mutate func(map[string]string)) error {
	kind, id := resourceRefFromARN(resourceArn)

	switch kind {
	case kindBroker:
		ok := m.brokers.Update(id, func(b driver.Broker) driver.Broker {
			b.Tags = ensureTags(b.Tags)
			mutate(b.Tags)

			return b
		})
		if !ok {
			return notFound("Broker %s not found", id)
		}

		return nil
	case kindConfiguration:
		ok := m.configs.Update(id, func(c driver.Configuration) driver.Configuration {
			c.Tags = ensureTags(c.Tags)
			mutate(c.Tags)

			return c
		})
		if !ok {
			return notFound("Configuration %s not found", id)
		}

		return nil
	default:
		return badRequest("invalid resource ARN: %q", resourceArn)
	}
}

// ensureTags returns an alias-free, non-nil copy of a tag map ready for mutation.
func ensureTags(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}

	return copyTags(in)
}
