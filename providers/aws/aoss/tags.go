package aoss

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/aoss/driver"
)

// TagResource adds or overwrites tags on a collection identified by its ARN.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags []driver.Tag) error {
	id, err := collectionIDFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateCollectionTags(id, resourceArn, func(existing []driver.Tag) []driver.Tag {
		for _, t := range tags {
			existing = upsertTag(existing, t)
		}

		return existing
	})
}

// UntagResource removes tags by key from a collection.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	id, err := collectionIDFromARN(resourceArn)
	if err != nil {
		return err
	}

	remove := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
		remove[k] = true
	}

	return m.updateCollectionTags(id, resourceArn, func(existing []driver.Tag) []driver.Tag {
		kept := existing[:0]

		for _, t := range existing {
			if !remove[t.Key] {
				kept = append(kept, t)
			}
		}

		return kept
	})
}

// ListTagsForResource returns a copy of a collection's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) ([]driver.Tag, error) {
	id, err := collectionIDFromARN(resourceArn)
	if err != nil {
		return nil, err
	}

	c, ok := m.collections.Get(id)
	if !ok {
		return nil, notFound("collection with arn %q not found", resourceArn)
	}

	return copyTags(c.Tags), nil
}

func (m *Mock) updateCollectionTags(id, arn string, mutate func([]driver.Tag) []driver.Tag) error {
	ok := m.collections.Update(id, func(c driver.Collection) driver.Collection {
		c.Tags = mutate(copyTags(c.Tags))

		return c
	})
	if !ok {
		return notFound("collection with arn %q not found", arn)
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

// collectionIDFromARN extracts the collection id from an aoss collection ARN of
// the form arn:aws:aoss:{region}:{acct}:collection/{id}.
func collectionIDFromARN(resourceArn string) (string, error) {
	const marker = ":collection/"

	idx := strings.LastIndex(resourceArn, marker)
	if idx < 0 {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	id := resourceArn[idx+len(marker):]
	if id == "" {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	return id, nil
}
