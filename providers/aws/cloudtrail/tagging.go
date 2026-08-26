package cloudtrail

import (
	"context"
	"strings"
)

// resourceARNExists reports whether resourceID is a well-formed CloudTrail
// resource ARN that names an existing trail, event data store, channel, or
// dashboard. A malformed ARN yields a CloudTrailARNInvalidException; a
// well-formed-but-absent ARN yields a ResourceNotFoundException.
func (m *Mock) resourceARNExists(resourceID string) error {
	seg := strings.SplitN(resourceID, ":", arnParts)
	if !strings.HasPrefix(resourceID, arnPrefix+":") || len(seg) != arnParts || seg[2] != serviceName {
		return errCloudTrailARNInvalid(resourceID)
	}

	exists, known := m.resourceExistsByType(resourceID, seg[5])
	if !known {
		return errCloudTrailARNInvalid(resourceID)
	}

	if !exists {
		return errResourceNotFound(resourceID)
	}

	return nil
}

// resourceExistsByType reports whether the ARN whose resource segment is res
// names an existing resource. known is false when res has no recognized
// CloudTrail resource-type prefix (a malformed ARN).
func (m *Mock) resourceExistsByType(arn, res string) (exists, known bool) {
	switch {
	case strings.HasPrefix(res, "eventdatastore/"):
		return m.eds.Has(arn), true
	case strings.HasPrefix(res, "channel/"):
		return m.channels.Has(arn), true
	case strings.HasPrefix(res, "trail/"):
		return m.trailARNIdx.Has(arn), true
	case strings.HasPrefix(res, "dashboard/"):
		return m.dashboards.Has(strings.TrimPrefix(res, "dashboard/")), true
	default:
		return false, false
	}
}

// storeResourceTags overwrites the tag set for a resource ARN. A nil/empty map
// clears the entry.
func (m *Mock) storeResourceTags(resourceID string, tags map[string]string) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if len(tags) == 0 {
		delete(m.tags, resourceID)

		return
	}

	m.tags[resourceID] = copyTags(tags)
}

func (m *Mock) deleteResourceTags(resourceID string) {
	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	delete(m.tags, resourceID)
}

// AddTags adds or overwrites tags on a resource (trail/EDS/channel/dashboard).
func (m *Mock) AddTags(_ context.Context, resourceID string, tags map[string]string) error {
	if resourceID == "" {
		return errInvalidParameter("ResourceId is required")
	}

	if err := m.resourceARNExists(resourceID); err != nil {
		return err
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	if m.tags[resourceID] == nil {
		m.tags[resourceID] = map[string]string{}
	}

	for k, v := range tags {
		m.tags[resourceID][k] = v
	}

	return nil
}

// RemoveTags removes tags by key from a resource.
func (m *Mock) RemoveTags(_ context.Context, resourceID string, tagKeys []string) error {
	if resourceID == "" {
		return errInvalidParameter("ResourceId is required")
	}

	if err := m.resourceARNExists(resourceID); err != nil {
		return err
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	for _, k := range tagKeys {
		delete(m.tags[resourceID], k)
	}

	if len(m.tags[resourceID]) == 0 {
		delete(m.tags, resourceID)
	}

	return nil
}

// ListTags returns a copy of the tags for each requested resource ID. Each ARN
// is validated like AddTags/RemoveTags: a malformed ARN yields
// CloudTrailARNInvalidException and a well-formed-but-absent ARN yields
// ResourceNotFoundException.
func (m *Mock) ListTags(_ context.Context, resourceIDs []string) (map[string]map[string]string, error) {
	for _, id := range resourceIDs {
		if err := m.resourceARNExists(id); err != nil {
			return nil, err
		}
	}

	m.tagsMu.RLock()
	defer m.tagsMu.RUnlock()

	out := make(map[string]map[string]string, len(resourceIDs))
	for _, id := range resourceIDs {
		out[id] = copyTags(m.tags[id])
	}

	return out, nil
}
