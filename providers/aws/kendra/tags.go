package kendra

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kendra/driver"
)

// resourceRef identifies the resource an ARN points at: an index, or a data
// source under an index.
type resourceRef struct {
	indexID      string
	dataSourceID string // empty for an index ARN
}

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

// parseResourceARN extracts the index id (and data source id, if present) from a
// Kendra resource ARN of the form
// arn:aws:kendra:{region}:{acct}:index/{indexId} or
// arn:aws:kendra:{region}:{acct}:index/{indexId}/data-source/{dataSourceId}.
func parseResourceARN(resourceARN string) (resourceRef, error) {
	const indexMarker = ":index/"

	idx := strings.LastIndex(resourceARN, indexMarker)
	if idx < 0 {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	rest := resourceARN[idx+len(indexMarker):]

	const dsMarker = "/data-source/"

	if indexID, dsID, found := strings.Cut(rest, dsMarker); found {
		if indexID == "" || dsID == "" {
			return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
		}

		return resourceRef{indexID: indexID, dataSourceID: dsID}, nil
	}

	if rest == "" {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	return resourceRef{indexID: rest}, nil
}
