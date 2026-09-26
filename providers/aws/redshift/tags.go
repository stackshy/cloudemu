package redshift

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Tags for every Redshift resource live in one ARN-keyed store (tagsByARN).
// Creates put their tags there, CreateTags/DeleteTags change it, and every
// read path fills the returned Tags from it. Stored resource rows keep no tag
// copy, so a read can never return stale tags.

// CreateTags tags a Redshift resource by ARN (ResourceName).
func (m *Mock) CreateTags(_ context.Context, resourceName string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.setTagsLocked(resourceName, tags)

	return nil
}

// DeleteTags removes tags by key from a Redshift resource by ARN.
func (m *Mock) DeleteTags(_ context.Context, resourceName string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, k := range keys {
		delete(m.tagsByARN[resourceName], k)
	}

	if len(m.tagsByARN[resourceName]) == 0 {
		delete(m.tagsByARN, resourceName)
	}

	return nil
}

// DescribeTags returns the tags on a Redshift resource by ARN.
func (m *Mock) DescribeTags(_ context.Context, resourceName string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]string, len(m.tagsByARN[resourceName]))
	for k, v := range m.tagsByARN[resourceName] {
		out[k] = v
	}

	return out, nil
}

// setTagsLocked adds tags to the ARN-keyed tag store. The caller holds m.mu.
func (m *Mock) setTagsLocked(arn string, tags map[string]string) {
	if len(tags) == 0 {
		return
	}

	if m.tagsByARN == nil {
		m.tagsByARN = map[string]map[string]string{}
	}

	if m.tagsByARN[arn] == nil {
		m.tagsByARN[arn] = map[string]string{}
	}

	for k, v := range tags {
		m.tagsByARN[arn][k] = v
	}
}

// replaceTagsLocked sets the tags of arn to exactly tags. The caller holds m.mu.
func (m *Mock) replaceTagsLocked(arn string, tags map[string]string) {
	delete(m.tagsByARN, arn)
	m.setTagsLocked(arn, tags)
}

// tagsLocked returns a copy of the tags of arn, or nil when it has none. The
// caller holds m.mu (read or write).
func (m *Mock) tagsLocked(arn string) map[string]string {
	return copyTags(m.tagsByARN[arn])
}

// tags is tagsLocked for callers that do not hold m.mu.
func (m *Mock) tags(arn string) map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.tagsLocked(arn)
}

func (m *Mock) parameterGroupARN(name string) string {
	return idgen.AWSARN("redshift", m.opts.Region, m.opts.AccountID, "parametergroup:"+name)
}

func (m *Mock) subnetGroupARN(name string) string {
	return idgen.AWSARN("redshift", m.opts.Region, m.opts.AccountID, "subnetgroup:"+name)
}
