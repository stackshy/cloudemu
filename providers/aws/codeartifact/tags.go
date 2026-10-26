package codeartifact

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// tagTarget gives read and write access to the tag map of one taggable resource.
type tagTarget struct {
	resourceType string
	resourceID   string
	get          func() map[string]string
	set          func(map[string]string)
}

// TagResource adds or overwrites tags on a domain, repository or package group.
// A resource holds at most 200 tags; overwriting an existing key does not count
// against the limit.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	if err := validateTags(tags); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.tagTargetLocked(resourceArn)
	if err != nil {
		return err
	}

	merged := copyTags(t.get())
	if merged == nil {
		merged = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		merged[k] = v
	}

	if len(merged) > maxTags {
		return quotaExceeded(t.resourceType, t.resourceID, "A resource can have at most %d tags", maxTags)
	}

	t.set(merged)

	return nil
}

// UntagResource removes tags by key from a domain, repository or package group.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.tagTargetLocked(resourceArn)
	if err != nil {
		return err
	}

	kept := copyTags(t.get())

	for _, k := range tagKeys {
		delete(kept, k)
	}

	t.set(kept)

	return nil
}

// ListTagsForResource returns a copy of a resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.tagTargetLocked(resourceArn)
	if err != nil {
		return nil, err
	}

	return copyTags(t.get()), nil
}

// tagTargetLocked resolves a CodeArtifact resource ARN to its tag map. A
// malformed or non-CodeArtifact ARN is a ValidationException; a well-formed ARN
// naming a missing resource, or one in another account, region or partition, is a
// ResourceNotFoundException. The caller holds mu.
func (m *Mock) tagTargetLocked(arn string) (*tagTarget, error) {
	kind, key := resourceRefFromARN(arn)

	switch kind {
	case kindDomain:
		d, ok := m.domains.Get(key)
		if !ok || d.Arn != arn {
			return nil, notFound(driver.ResourceTypeDomain, key, "Domain %s not found", key)
		}

		return &tagTarget{
			resourceType: driver.ResourceTypeDomain, resourceID: key,
			get: func() map[string]string { return d.Tags },
			set: func(t map[string]string) {
				d.Tags = t
				m.domains.Set(key, d)
			},
		}, nil
	case kindRepository:
		r, ok := m.repos.Get(key)
		if !ok || r.Arn != arn {
			return nil, notFound(driver.ResourceTypeRepository, key, "Repository %s not found", key)
		}

		return &tagTarget{
			resourceType: driver.ResourceTypeRepository, resourceID: key,
			get: func() map[string]string { return r.Tags },
			set: func(t map[string]string) {
				r.Tags = t
				m.repos.Set(key, r)
			},
		}, nil
	case kindPackageGroup:
		g, ok := m.groups.Get(key)
		if !ok || g.Arn != arn {
			return nil, notFound("", key, "Package group %s not found", key)
		}

		return &tagTarget{
			resourceType: "", resourceID: key,
			get: func() map[string]string { return g.Tags },
			set: func(t map[string]string) { g.Tags = t },
		}, nil
	default:
		return nil, validation("invalid resource ARN: %q", arn)
	}
}

// resourceRefFromARN extracts the (kind, store key) pair from a CodeArtifact
// resource ARN. A domain ARN of the form arn:aws:codeartifact:{region}:{acct}:domain/{name}
// yields (domain, name); a repository ARN of the form
// arn:aws:codeartifact:{region}:{acct}:repository/{domain}/{repo} yields
// (repository, "{domain}/{repo}"); a package group ARN of the form
// arn:aws:codeartifact:{region}:{acct}:package-group/{domain}{pattern} yields
// (package-group, "{domain}|{pattern}"). Anything else yields ("", "").
func resourceRefFromARN(arn string) (kind, key string) {
	const arnParts = 6

	if !strings.Contains(arn, ":"+serviceName+":") {
		return "", ""
	}

	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) < arnParts {
		return "", ""
	}

	slash := strings.IndexByte(parts[arnParts-1], '/')
	if slash < 0 {
		return "", ""
	}

	kind, rest := parts[arnParts-1][:slash], parts[arnParts-1][slash+1:]

	switch kind {
	case kindDomain, kindRepository:
		return kind, rest
	case kindPackageGroup:
		i := strings.IndexByte(rest, '/')
		if i <= 0 {
			return "", ""
		}

		return kind, groupKey(rest[:i], rest[i:])
	default:
		return "", ""
	}
}
