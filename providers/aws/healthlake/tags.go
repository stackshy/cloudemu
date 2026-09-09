package healthlake

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// TagResource adds or overwrites tags on a data store identified by its ARN.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags []driver.Tag) error {
	id, err := datastoreIDFromARN(resourceARN)
	if err != nil {
		return err
	}

	return m.mutateTags(id, resourceARN, func(existing []driver.Tag) []driver.Tag {
		for _, t := range tags {
			existing = upsertTag(existing, t)
		}

		return existing
	})
}

// UntagResource removes tags by key from a data store.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	id, err := datastoreIDFromARN(resourceARN)
	if err != nil {
		return err
	}

	remove := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
		remove[k] = true
	}

	return m.mutateTags(id, resourceARN, func(existing []driver.Tag) []driver.Tag {
		kept := existing[:0]

		for _, t := range existing {
			if !remove[t.Key] {
				kept = append(kept, t)
			}
		}

		return kept
	})
}

// ListTagsForResource returns a copy of a data store's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	id, err := datastoreIDFromARN(resourceARN)
	if err != nil {
		return nil, err
	}

	ds, ok := m.datastores.Get(id)
	if !ok {
		return nil, notFound("resource %q does not exist", resourceARN)
	}

	return copyTags(ds.Tags), nil
}

func (m *Mock) mutateTags(id, arn string, mutate func([]driver.Tag) []driver.Tag) error {
	ok := m.datastores.Update(id, func(d driver.Datastore) driver.Datastore {
		d.Tags = mutate(copyTags(d.Tags))

		return d
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

// datastoreIDFromARN extracts the data-store id from a HealthLake resource ARN
// of the form arn:aws:healthlake:{region}:{acct}:datastore/fhir/{id}.
func datastoreIDFromARN(resourceARN string) (string, error) {
	const marker = ":datastore/fhir/"

	idx := strings.LastIndex(resourceARN, marker)
	if idx < 0 {
		return "", validation("invalid resource ARN: %q", resourceARN)
	}

	id := resourceARN[idx+len(marker):]
	if id == "" {
		return "", validation("invalid resource ARN: %q", resourceARN)
	}

	return id, nil
}
