package timestreamwrite

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// resourceRef identifies the resource an ARN points at: a database, or a table
// under a database.
type resourceRef struct {
	databaseName string
	tableName    string // empty for a database ARN
}

// TagResource adds or overwrites tags on a database or table identified by its
// ARN.
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

// UntagResource removes tags by key from a database or table.
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

// ListTagsForResource returns a copy of a database's or table's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	ref, err := parseResourceARN(resourceARN)
	if err != nil {
		return nil, err
	}

	if ref.tableName != "" {
		t, ok := m.tables.Get(tableKey(ref.databaseName, ref.tableName))
		if !ok {
			return nil, notFound("resource %q does not exist", resourceARN)
		}

		return copyTags(t.Tags), nil
	}

	db, ok := m.databases.Get(ref.databaseName)
	if !ok {
		return nil, notFound("resource %q does not exist", resourceARN)
	}

	return copyTags(db.Tags), nil
}

func (m *Mock) mutateTags(ref resourceRef, arn string, mutate func([]driver.Tag) []driver.Tag) error {
	if ref.tableName != "" {
		ok := m.tables.Update(tableKey(ref.databaseName, ref.tableName), func(t driver.Table) driver.Table {
			t.Tags = mutate(copyTags(t.Tags))

			return t
		})
		if !ok {
			return notFound("resource %q does not exist", arn)
		}

		return nil
	}

	ok := m.databases.Update(ref.databaseName, func(d driver.Database) driver.Database {
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

// parseResourceARN extracts the database name (and table name, if present) from
// a Timestream resource ARN of the form
// arn:aws:timestream:{region}:{acct}:database/{db} or
// arn:aws:timestream:{region}:{acct}:database/{db}/table/{table}.
func parseResourceARN(resourceARN string) (resourceRef, error) {
	const dbMarker = ":database/"

	idx := strings.LastIndex(resourceARN, dbMarker)
	if idx < 0 {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	rest := resourceARN[idx+len(dbMarker):]

	const tableMarker = "/table/"

	if dbName, tableName, found := strings.Cut(rest, tableMarker); found {
		if dbName == "" || tableName == "" {
			return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
		}

		return resourceRef{databaseName: dbName, tableName: tableName}, nil
	}

	if rest == "" {
		return resourceRef{}, validation("invalid resource ARN: %q", resourceARN)
	}

	return resourceRef{databaseName: rest}, nil
}
