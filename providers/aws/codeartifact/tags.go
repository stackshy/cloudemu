package codeartifact

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// TagResource adds or overwrites tags on a domain or repository.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) {
		for k, v := range tags {
			t[k] = v
		}
	})
}

// UntagResource removes tags by key from a domain or repository.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	return m.mutateTags(resourceArn, func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	})
}

// ListTagsForResource returns a copy of a domain's or repository's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, key := resourceRefFromARN(resourceArn)

	switch kind {
	case kindDomain:
		d, ok := m.domains.Get(key)
		if !ok {
			return nil, notFound("Domain %s not found", key)
		}

		return copyTags(d.Tags), nil
	case kindRepository:
		r, ok := m.repos.Get(key)
		if !ok {
			return nil, notFound("Repository %s not found", key)
		}

		return copyTags(r.Tags), nil
	default:
		return nil, validation("invalid resource ARN: %q", resourceArn)
	}
}

// mutateTags applies mutate to the tag map of the domain or repository the ARN
// identifies, under the store lock.
func (m *Mock) mutateTags(resourceArn string, mutate func(map[string]string)) error {
	kind, key := resourceRefFromARN(resourceArn)

	switch kind {
	case kindDomain:
		ok := m.domains.Update(key, func(d driver.Domain) driver.Domain {
			d.Tags = ensureTags(d.Tags)
			mutate(d.Tags)

			return d
		})
		if !ok {
			return notFound("Domain %s not found", key)
		}

		return nil
	case kindRepository:
		ok := m.repos.Update(key, func(r driver.Repository) driver.Repository {
			r.Tags = ensureTags(r.Tags)
			mutate(r.Tags)

			return r
		})
		if !ok {
			return notFound("Repository %s not found", key)
		}

		return nil
	default:
		return validation("invalid resource ARN: %q", resourceArn)
	}
}

// ensureTags returns an alias-free, non-nil copy of a tag map ready for mutation.
func ensureTags(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}

	return copyTags(in)
}
