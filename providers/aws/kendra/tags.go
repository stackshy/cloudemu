package kendra

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// resourceRef identifies the resource an ARN points at: an index, a data source
// under an index, or another taggable child of an index (FAQ, thesaurus, query
// suggestions block list, featured results set).
type resourceRef struct {
	indexID      string
	dataSourceID string // set for a data source ARN
	kind         string // "faq", "thesaurus", "query-suggestions-block-list" or "featured-results-set"
	childID      string
}

// taggableKinds are the ARN path segments of the taggable index children.
//
//nolint:gochecknoglobals // static lookup set
var taggableKinds = []string{kindFaq, kindThesaurus, kindBlockList, kindFeatured}

// ARN path segments of the taggable index children.
const (
	kindFaq       = "faq"
	kindThesaurus = "thesaurus"
	kindBlockList = "query-suggestions-block-list"
	kindFeatured  = "featured-results-set"
)

// TagResource adds or overwrites tags on an index or data source identified by
// its ARN.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags []driver.Tag) error {
	ref, err := parseResourceARN(resourceARN)
	if err != nil {
		return err
	}

	return m.mutateTags(ref, resourceARN, func(existing []driver.Tag) []driver.Tag {
		for _, t := range tags {
			existing = upsertTag(existing, t)
		}

		return existing
	})
}

// UntagResource removes tags by key from an index or data source.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	ref, err := parseResourceARN(resourceARN)
	if err != nil {
		return err
	}

	remove := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
		remove[k] = true
	}

	return m.mutateTags(ref, resourceARN, func(existing []driver.Tag) []driver.Tag {
		kept := existing[:0]

		for _, t := range existing {
			if !remove[t.Key] {
				kept = append(kept, t)
			}
		}

		return kept
	})
}

// ListTagsForResource returns a copy of an index's or data source's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	ref, err := parseResourceARN(resourceARN)
	if err != nil {
		return nil, err
	}

	if ref.kind != "" {
		tags, ok := m.childTags(ref)
		if !ok {
			return nil, notFound("resource %q does not exist", resourceARN)
		}

		return tags, nil
	}

	if ref.dataSourceID != "" {
		ds, ok := m.dataSources.Get(dataSourceKey(ref.indexID, ref.dataSourceID))
		if !ok {
			return nil, notFound("resource %q does not exist", resourceARN)
		}

		return copyTags(ds.Tags), nil
	}

	idx, ok := m.indexes.Get(ref.indexID)
	if !ok {
		return nil, notFound("resource %q does not exist", resourceARN)
	}

	return copyTags(idx.Tags), nil
}

func (m *Mock) mutateTags(ref resourceRef, arn string, mutate func([]driver.Tag) []driver.Tag) error {
	if ref.kind != "" {
		if !m.mutateChildTags(ref, mutate) {
			return notFound("resource %q does not exist", arn)
		}

		return nil
	}

	if ref.dataSourceID != "" {
		ok := m.dataSources.Update(dataSourceKey(ref.indexID, ref.dataSourceID), func(d driver.DataSource) driver.DataSource {
			d.Tags = mutate(copyTags(d.Tags))

			return d
		})
		if !ok {
			return notFound("resource %q does not exist", arn)
		}

		return nil
	}

	ok := m.indexes.Update(ref.indexID, func(i driver.Index) driver.Index {
		i.Tags = mutate(copyTags(i.Tags))

		return i
	})
	if !ok {
		return notFound("resource %q does not exist", arn)
	}

	return nil
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

// childTags returns the tags of a taggable index child.
func (m *Mock) childTags(ref resourceRef) ([]driver.Tag, bool) {
	key := childKey(ref.indexID, ref.childID)

	switch ref.kind {
	case kindFaq:
		if f, ok := m.faqs.Get(key); ok {
			return copyTags(f.Tags), true
		}
	case kindThesaurus:
		if t, ok := m.thesauri.Get(key); ok {
			return copyTags(t.Tags), true
		}
	case kindBlockList:
		if b, ok := m.blockLists.Get(key); ok {
			return copyTags(b.Tags), true
		}
	case kindFeatured:
		if f, ok := m.featured.Get(key); ok {
			return copyTags(f.Tags), true
		}
	}

	return nil, false
}

// mutateChildTags rewrites the tags of a taggable index child and reports
// whether it exists.
func (m *Mock) mutateChildTags(ref resourceRef, mutate func([]driver.Tag) []driver.Tag) bool {
	key := childKey(ref.indexID, ref.childID)

	switch ref.kind {
	case kindFaq:
		return m.faqs.Update(key, func(f driver.Faq) driver.Faq {
			f.Tags = mutate(copyTags(f.Tags))

			return f
		})
	case kindThesaurus:
		return m.thesauri.Update(key, func(t driver.Thesaurus) driver.Thesaurus {
			t.Tags = mutate(copyTags(t.Tags))

			return t
		})
	case kindBlockList:
		return m.blockLists.Update(key, func(b driver.BlockList) driver.BlockList {
			b.Tags = mutate(copyTags(b.Tags))

			return b
		})
	case kindFeatured:
		return m.featured.Update(key, func(f driver.FeaturedResultsSet) driver.FeaturedResultsSet {
			f.Tags = mutate(copyTags(f.Tags))

			return f
		})
	}

	return false
}

// parseResourceARN extracts the resource a Kendra ARN names:
// arn:aws:kendra:{region}:{acct}:index/{indexId}, .../data-source/{id}, or
// .../{faq|thesaurus|query-suggestions-block-list|featured-results-set}/{id}.
func parseResourceARN(resourceARN string) (resourceRef, error) {
	const indexMarker = ":index/"

	idx := strings.LastIndex(resourceARN, indexMarker)
	if idx < 0 {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	rest := resourceARN[idx+len(indexMarker):]

	indexID, tail, hasChild := strings.Cut(rest, "/")
	if indexID == "" {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	if !hasChild {
		return resourceRef{indexID: indexID}, nil
	}

	kind, childID, ok := strings.Cut(tail, "/")
	if !ok || childID == "" {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	if kind == "data-source" {
		return resourceRef{indexID: indexID, dataSourceID: childID}, nil
	}

	for _, k := range taggableKinds {
		if kind == k {
			return resourceRef{indexID: indexID, kind: kind, childID: childID}, nil
		}
	}

	return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
}
