package backup

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
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

// maxListTagsResults is the upper bound of ListTags maxResults (the service
// model allows 1 to 1000).
const maxListTagsResults = 1000

// ListTags returns one page of the tags of a vault or plan. Keys are ordered so
// a NextToken always resumes where the previous page stopped. A zero MaxResults
// means the caller sent none.
func (m *Mock) ListTags(_ context.Context, resourceArn string, page driver.Page) (tags map[string]string, nextToken string, err error) {
	if page.MaxResults < 0 || page.MaxResults > maxListTagsResults {
		return nil, "", invalidParam("maxResults must be between 1 and %d", maxListTagsResults)
	}

	kind, key, err := m.resolveTaggable(resourceArn)
	if err != nil {
		return nil, "", err
	}

	var stored map[string]string

	if kind == kindVault {
		v, _ := m.vaults.Get(key)
		stored = v.Tags
	} else {
		p, _ := m.plans.Get(key)
		stored = p.Tags
	}

	keys := make([]string, 0, len(stored))
	for k := range stored {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	start, end, nextToken, err := paginate(len(keys), page)
	if err != nil {
		return nil, "", err
	}

	if start == end {
		return nil, nextToken, nil
	}

	tags = make(map[string]string, end-start)
	for _, k := range keys[start:end] {
		tags[k] = stored[k]
	}

	return tags, nextToken, nil
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
