package resourcediscovery

import (
	"context"
	"sort"
)

// SearchByTag returns every resource carrying tag key. If value is non-empty,
// the tag's value must also match exactly.
func (e *Engine) SearchByTag(ctx context.Context, key, value string) ([]Resource, error) {
	return e.List(ctx, Query{Tags: map[string]string{key: value}})
}

// GetTagKeys returns the deduplicated, sorted set of tag keys present on
// any resource across the engine's drivers.
func (e *Engine) GetTagKeys(ctx context.Context) ([]string, error) {
	all, err := e.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	set := make(map[string]struct{})

	for i := range all {
		for k := range all[i].Tags {
			set[k] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}

	sort.Strings(out)

	return out, nil
}

// GetTagValues returns the deduplicated, sorted set of values seen for the
// given tag key across every resource.
func (e *Engine) GetTagValues(ctx context.Context, key string) ([]string, error) {
	all, err := e.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	set := make(map[string]struct{})

	for i := range all {
		if v, ok := all[i].Tags[key]; ok {
			set[v] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}

	sort.Strings(out)

	return out, nil
}
