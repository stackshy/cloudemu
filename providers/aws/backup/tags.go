package backup

import (
	"context"
)

// resolveTaggable validates that a tag operation's ARN names an existing backup
// vault or plan, returning the resource kind and its store key.
func (m *Mock) resolveTaggable(arn string) (kind, key string, err error) {
	kind, key = resourceRefFromARN(arn)

	switch kind {
	case kindVault:
		if !m.vaults.Has(key) {
			return "", "", notFound("backup vault %s not found", key)
		}
	case kindPlan:
		if !m.plans.Has(key) {
			return "", "", notFound("backup plan %s not found", key)
		}
	default:
		return "", "", invalidParam("invalid AWS Backup resource ARN: %s", arn)
	}

	return kind, key, nil
}

// TagResource merges tags onto a vault or plan.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	kind, key, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, key, func(existing map[string]string) map[string]string {
		if existing == nil {
			existing = map[string]string{}
		}

		for k, v := range tags {
			existing[k] = v
		}

		return existing
	})

	return nil
}

// UntagResource removes the given tag keys from a vault or plan.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	kind, key, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, key, func(existing map[string]string) map[string]string {
		for _, k := range tagKeys {
			delete(existing, k)
		}

		return existing
	})

	return nil
}

// ListTags returns the tags of a vault or plan.
func (m *Mock) ListTags(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, key, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return nil, err
	}

	if kind == kindVault {
		v, _ := m.vaults.Get(key)

		return copyTags(v.Tags), nil
	}

	p, _ := m.plans.Get(key)

	return copyTags(p.Tags), nil
}

// mutateTags applies fn to the tag map of the identified resource and stores the
// result.
func (m *Mock) mutateTags(kind, key string, fn func(map[string]string) map[string]string) {
	if kind == kindVault {
		v, _ := m.vaults.Get(key)
		v.Tags = fn(copyTags(v.Tags))
		m.vaults.Set(key, v)

		return
	}

	p, _ := m.plans.Get(key)
	p.Tags = fn(copyTags(p.Tags))
	m.plans.Set(key, p)
}
