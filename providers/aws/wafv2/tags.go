package wafv2

import (
	"context"
	"sync"
)

// tagTarget adapts a stored resource to a uniform tag mutation surface: a lock,
// a pointer to its tag map, and its ARN.
type tagTarget struct {
	mu   *sync.RWMutex
	tags *map[string]string
}

// findTagTarget locates the resource identified by arn across every store and
// returns a handle for mutating its tags. The second return is false when no
// resource has that ARN.
func (m *Mock) findTagTarget(arn string) (tagTarget, bool) {
	for _, wd := range m.webACLs.All() {
		wd.mu.RLock()
		match := wd.acl.ARN == arn
		wd.mu.RUnlock()

		if match {
			return tagTarget{mu: &wd.mu, tags: &wd.acl.Tags}, true
		}
	}

	for _, sd := range m.ipSets.All() {
		sd.mu.RLock()
		match := sd.set.ARN == arn
		sd.mu.RUnlock()

		if match {
			return tagTarget{mu: &sd.mu, tags: &sd.set.Tags}, true
		}
	}

	for _, gd := range m.ruleGrps.All() {
		gd.mu.RLock()
		match := gd.grp.ARN == arn
		gd.mu.RUnlock()

		if match {
			return tagTarget{mu: &gd.mu, tags: &gd.grp.Tags}, true
		}
	}

	for _, rd := range m.regexes.All() {
		rd.mu.RLock()
		match := rd.set.ARN == arn
		rd.mu.RUnlock()

		if match {
			return tagTarget{mu: &rd.mu, tags: &rd.set.Tags}, true
		}
	}

	return tagTarget{}, false
}

// TagResource adds or overwrites tags on a WAFv2 resource.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	t, ok := m.findTagTarget(arn)
	if !ok {
		return nonexistent("resource %q not found", arn)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if *t.tags == nil {
		*t.tags = map[string]string{}
	}

	if len(*t.tags)+len(tags) > maxTags {
		return invalidParameter("a resource may have at most %d tags", maxTags)
	}

	for k, v := range tags {
		(*t.tags)[k] = v
	}

	return nil
}

// UntagResource removes tags by key from a WAFv2 resource.
func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	t, ok := m.findTagTarget(arn)
	if !ok {
		return nonexistent("resource %q not found", arn)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, k := range tagKeys {
		delete(*t.tags, k)
	}

	return nil
}

// ListTagsForResource returns a copy of a WAFv2 resource's tags.
func (m *Mock) ListTagsForResource(
	_ context.Context, arn string,
) (resourceARN string, tags map[string]string, err error) {
	t, ok := m.findTagTarget(arn)
	if !ok {
		return "", nil, nonexistent("resource %q not found", arn)
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	return arn, copyTags(*t.tags), nil
}
