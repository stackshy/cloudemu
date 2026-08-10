package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// tagTarget abstracts the taggable resources (recorders, rules, aggregators)
// resolved by ARN. get/set operate under the caller-held write lock.
type tagTarget struct {
	get func() map[string]string
	set func(map[string]string)
	mu  interface {
		Lock()
		Unlock()
	}
}

// resolveTagTarget finds the taggable resource with the given ARN, returning a
// target whose get/set close over that resource's own map and lock.
func (m *Mock) resolveTagTarget(arn string) (*tagTarget, bool) {
	if t, ok := m.recorderTarget(arn); ok {
		return t, true
	}

	if t, ok := m.ruleTarget(arn); ok {
		return t, true
	}

	if t, ok := m.aggregatorTarget(arn); ok {
		return t, true
	}

	return nil, false
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (m *Mock) recorderTarget(arn string) (*tagTarget, bool) {
	for _, k := range m.recorders.Keys() {
		rd, ok := m.recorders.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		match := rd.rec.Arn == arn
		rd.mu.RUnlock()

		if match {
			return &tagTarget{
				get: func() map[string]string { return rd.rec.Tags },
				set: func(t map[string]string) { rd.rec.Tags = t },
				mu:  &rd.mu,
			}, true
		}
	}

	return nil, false
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (m *Mock) ruleTarget(arn string) (*tagTarget, bool) {
	for _, k := range m.rules.Keys() {
		rd, ok := m.rules.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		match := rd.rule.ConfigRuleArn == arn
		rd.mu.RUnlock()

		if match {
			return &tagTarget{
				get: func() map[string]string { return rd.rule.Tags },
				set: func(t map[string]string) { rd.rule.Tags = t },
				mu:  &rd.mu,
			}, true
		}
	}

	return nil, false
}

//nolint:dupl // near-identical per-operation SDK dispatch/CRUD boilerplate; extracting a shared helper would obscure the per-op wire shape.
func (m *Mock) aggregatorTarget(arn string) (*tagTarget, bool) {
	for _, k := range m.aggregators.Keys() {
		ad, ok := m.aggregators.Get(k)
		if !ok {
			continue
		}

		ad.mu.RLock()
		match := ad.agg.Arn == arn
		ad.mu.RUnlock()

		if match {
			return &tagTarget{
				get: func() map[string]string { return ad.agg.Tags },
				set: func(t map[string]string) { ad.agg.Tags = t },
				mu:  &ad.mu,
			}, true
		}
	}

	return nil, false
}

// TagResource adds or overwrites tags on a Config resource, enforcing the tag
// cap. Validation happens before mutation.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	if len(tags) == 0 {
		return validation("Tags must not be empty")
	}

	t, ok := m.resolveTagTarget(arn)
	if !ok {
		return tagged(driver.ExResourceNotFound, notFoundCode, "no taggable resource with ARN %q", arn)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	merged, err := mergeTags(t.get(), tags)
	if err != nil {
		return err
	}

	t.set(merged)

	return nil
}

// UntagResource removes tags by key.
func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	t, ok := m.resolveTagTarget(arn)
	if !ok {
		return tagged(driver.ExResourceNotFound, notFoundCode, "no taggable resource with ARN %q", arn)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	out := copyTags(t.get())
	if out == nil {
		return nil
	}

	for _, k := range tagKeys {
		delete(out, k)
	}

	t.set(out)

	return nil
}

// ListTagsForResource returns a copy of a resource's tags, paginated by key.
func (m *Mock) ListTagsForResource(
	_ context.Context, arn string, page driver.Page,
) (tags map[string]string, nextToken string, err error) {
	t, ok := m.resolveTagTarget(arn)
	if !ok {
		return nil, "", tagged(driver.ExResourceNotFound, notFoundCode, "no taggable resource with ARN %q", arn)
	}

	t.mu.Lock()
	out := copyTags(t.get())
	t.mu.Unlock()

	// Pagination over an unordered map isn't meaningful for tags; Config returns
	// all tags in one page. NextToken is honored for validity but yields all.
	if page.NextToken != "" {
		return nil, "", invalidNextToken(page.NextToken)
	}

	return out, "", nil
}
