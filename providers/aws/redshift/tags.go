package redshift

import (
	"context"
	"strings"
	"unicode/utf8"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Tags for every Redshift resource live in one ARN-keyed store (tagsByARN).
// Creates put their tags there, CreateTags/DeleteTags change it, and every
// read path fills the returned Tags from it. Stored resource rows keep no tag
// copy, so a read can never return stale tags.

const (
	maxTagsPerResource = 50
	maxTagKeyLen       = 128
	maxTagValueLen     = 256
	arnParts           = 6
	arnScheme          = "arn"
)

// CreateTags tags an existing Redshift resource by ARN (ResourceName).
func (m *Mock) CreateTags(_ context.Context, resourceName string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireTaggableLocked(resourceName); err != nil {
		return err
	}

	if err := validateTags(m.tagsByARN[resourceName], tags); err != nil {
		return err
	}

	m.setTagsLocked(resourceName, tags)

	return nil
}

// DeleteTags removes tags by key from an existing Redshift resource by ARN.
func (m *Mock) DeleteTags(_ context.Context, resourceName string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireTaggableLocked(resourceName); err != nil {
		return err
	}

	for _, k := range keys {
		delete(m.tagsByARN[resourceName], k)
	}

	if len(m.tagsByARN[resourceName]) == 0 {
		delete(m.tagsByARN, resourceName)
	}

	return nil
}

// DescribeTags returns the tags on an existing Redshift resource by ARN. An
// empty name (a resource-type listing) is not modeled and returns no tags.
func (m *Mock) DescribeTags(_ context.Context, resourceName string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if resourceName == "" {
		return map[string]string{}, nil
	}

	if err := m.requireTaggableLocked(resourceName); err != nil {
		return nil, err
	}

	out := make(map[string]string, len(m.tagsByARN[resourceName]))
	for k, v := range m.tagsByARN[resourceName] {
		out[k] = v
	}

	return out, nil
}

// requireTaggableLocked checks that arn names a Redshift resource that exists
// in this account and region. The caller holds m.mu.
func (m *Mock) requireTaggableLocked(arn string) error {
	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) != arnParts || parts[0] != arnScheme || parts[2] != "redshift" {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid Redshift resource ARN %q", arn)
	}

	kind, id, _ := strings.Cut(parts[5], ":")
	if stored, ok := m.taggableARNLocked(kind, id); !ok || stored != arn {
		return cerrors.Newf(cerrors.NotFound, "taggable resource %s not found", arn)
	}

	return nil
}

// taggableARNLocked returns the ARN of the stored resource of the given ARN
// resource type and id, and whether it exists. The caller holds m.mu.
func (m *Mock) taggableARNLocked(kind, id string) (string, bool) {
	switch kind {
	case "cluster":
		c, ok := m.clusters.Get(id)
		return c.ARN, ok
	case "snapshot":
		s, ok := m.clusterSnapshots.Get(id)
		return s.ARN, ok
	case "parametergroup":
		return m.parameterGroupARN(id), m.parameterGroups.Has(id)
	case "subnetgroup":
		return m.subnetGroupARN(id), m.subnetGroups.Has(id)
	case "eventsubscription":
		sub, ok := m.eventSubs.Get(id)
		return sub.ARN, ok
	default:
		return "", false
	}
}

// validateTags checks add against the Redshift tag rules: keys of 1 to 128
// characters without the reserved "aws:" prefix, values up to 256 characters,
// and at most 50 tags per resource counting the ones it already has.
func validateTags(existing, add map[string]string) error {
	count := len(existing)

	for k, v := range add {
		if k == "" || utf8.RuneCountInString(k) > maxTagKeyLen || strings.HasPrefix(strings.ToLower(k), "aws:") {
			return cerrors.Newf(cerrors.InvalidArgument,
				"invalid tag key %q: use 1 to %d characters and no aws: prefix", k, maxTagKeyLen)
		}

		if utf8.RuneCountInString(v) > maxTagValueLen {
			return cerrors.Newf(cerrors.InvalidArgument,
				"invalid tag value for key %q: use at most %d characters", k, maxTagValueLen)
		}

		if _, ok := existing[k]; !ok {
			count++
		}
	}

	if count > maxTagsPerResource {
		return cerrors.Newf(cerrors.ResourceExhausted,
			"tag limit exceeded: a resource can have at most %d tags", maxTagsPerResource)
	}

	return nil
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
