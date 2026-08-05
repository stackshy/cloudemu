package ec2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// tagsOf resolves an EC2 resource ID (by prefix) to its mutable tag map.
// Returns false when the ID is unknown or the resource does not exist.
func (m *Mock) tagsOf(id string) (map[string]string, bool) {
	switch {
	case strings.HasPrefix(id, "i-"):
		if d, ok := m.instances.Get(id); ok {
			if d.Tags == nil {
				d.Tags = map[string]string{}
			}

			return d.Tags, true
		}
	case strings.HasPrefix(id, "vol-"):
		if d, ok := m.volumes.Get(id); ok {
			if d.Tags == nil {
				d.Tags = map[string]string{}
			}

			return d.Tags, true
		}
	case strings.HasPrefix(id, "snap-"):
		if d, ok := m.snapshots.Get(id); ok {
			if d.Tags == nil {
				d.Tags = map[string]string{}
			}

			return d.Tags, true
		}
	case strings.HasPrefix(id, "ami-"):
		if d, ok := m.images.Get(id); ok {
			if d.Tags == nil {
				d.Tags = map[string]string{}
			}

			return d.Tags, true
		}
	}

	return nil, false
}

// TagResource applies tags to an EC2 instance, volume, snapshot, or image by
// ID. This backs the EC2 CreateTags API for compute resources (VPC-family IDs
// are handled by the networking provider). Returns NotFound for an unknown ID.
func (m *Mock) TagResource(_ context.Context, id string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dst, ok := m.tagsOf(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
	}

	for k, v := range tags {
		dst[k] = v
	}

	return nil
}

// UntagResource removes tags by key from an EC2 resource. An empty key list
// clears all tags, matching EC2 DeleteTags semantics.
func (m *Mock) UntagResource(_ context.Context, id string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	dst, ok := m.tagsOf(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
	}

	if len(keys) == 0 {
		for k := range dst {
			delete(dst, k)
		}

		return nil
	}

	for _, k := range keys {
		delete(dst, k)
	}

	return nil
}
