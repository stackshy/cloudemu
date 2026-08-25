package ec2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// tagsOf resolves a non-instance EC2 resource ID (by prefix) to its mutable tag
// map. Returns false when the ID is unknown or the resource does not exist.
// Instances are handled separately by mutateInstanceTags because their tag map
// is guarded by the per-instance instanceData.mu, not m.mu.
func (m *Mock) tagsOf(id string) (map[string]string, bool) {
	switch {
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

// mutateInstanceTags runs fn against an instance's tag map under that instance's
// own lock, so CreateTags/DeleteTags never race a concurrent DescribeInstances
// read of the same map. Returns false when the instance does not exist.
func (m *Mock) mutateInstanceTags(id string, fn func(map[string]string)) bool {
	inst, ok := m.instances.Get(id)
	if !ok {
		return false
	}

	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.Tags == nil {
		inst.Tags = map[string]string{}
	}

	fn(inst.Tags)

	return true
}

// TagResource applies tags to an EC2 instance, volume, snapshot, or image by
// ID. This backs the EC2 CreateTags API for compute resources (VPC-family IDs
// are handled by the networking provider). Returns NotFound for an unknown ID.
func (m *Mock) TagResource(_ context.Context, id string, tags map[string]string) error {
	apply := func(dst map[string]string) {
		for k, v := range tags {
			dst[k] = v
		}
	}

	if strings.HasPrefix(id, "i-") {
		if !m.mutateInstanceTags(id, apply) {
			return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
		}

		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dst, ok := m.tagsOf(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
	}

	apply(dst)

	return nil
}

// UntagResource removes tags by key from an EC2 resource. An empty key list
// clears all tags, matching EC2 DeleteTags semantics.
func (m *Mock) UntagResource(_ context.Context, id string, keys []string) error {
	remove := func(dst map[string]string) {
		if len(keys) == 0 {
			for k := range dst {
				delete(dst, k)
			}

			return
		}

		for _, k := range keys {
			delete(dst, k)
		}
	}

	if strings.HasPrefix(id, "i-") {
		if !m.mutateInstanceTags(id, remove) {
			return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
		}

		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dst, ok := m.tagsOf(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", id)
	}

	remove(dst)

	return nil
}
