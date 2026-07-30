package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// recordTags records a resource's creation-time tags under its ARN so that
// ListTagsForResource can return them. A resource with no tags is still
// recorded (as an empty slice) so its ARN is recognized as tag-managed.
func (m *Mock) recordTags(arn string, tags []driver.Tag) {
	m.tags.Set(arn, copyTags(tags))
}

// TagResource merges tags onto a resource, replacing the value of any key that
// already exists and appending new keys, mirroring AWS's upsert semantics.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags []driver.Tag) error {
	if resourceARN == "" {
		return apiErrf(errors.InvalidArgument, excInvalidParameter, "resourceArn is required")
	}

	existing, _ := m.tags.Get(resourceARN)
	merged := mergeTags(existing, tags)
	m.tags.Set(resourceARN, merged)

	return nil
}

// UntagResource removes the given tag keys from a resource.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	if resourceARN == "" {
		return apiErrf(errors.InvalidArgument, excInvalidParameter, "resourceArn is required")
	}

	existing, ok := m.tags.Get(resourceARN)
	if !ok {
		return nil
	}

	drop := make(map[string]bool, len(tagKeys))
	for _, k := range tagKeys {
		drop[k] = true
	}

	kept := make([]driver.Tag, 0, len(existing))

	for _, t := range existing {
		if !drop[t.Key] {
			kept = append(kept, t)
		}
	}

	m.tags.Set(resourceARN, kept)

	return nil
}

// ListTagsForResource returns a resource's tags. An ARN that is neither
// tag-managed nor a resolvable ECS resource surfaces a NotFound error.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	if tags, ok := m.tags.Get(resourceARN); ok {
		return copyTags(tags), nil
	}

	if m.resourceExists(resourceARN) {
		return nil, nil
	}

	return nil, apiErrf(errors.NotFound, excClient, "resource %q not found", resourceARN)
}

// mergeTags upserts add into base: existing keys are overwritten in place and
// new keys are appended, preserving order for determinism.
func mergeTags(base, add []driver.Tag) []driver.Tag {
	out := copyTags(base)

	for _, t := range add {
		replaced := false

		for i := range out {
			if out[i].Key == t.Key {
				out[i].Value = t.Value
				replaced = true

				break
			}
		}

		if !replaced {
			out = append(out, t)
		}
	}

	return out
}

// resourceExists reports whether an ARN resolves to a live cluster, service,
// task definition, task, or container instance.
func (m *Mock) resourceExists(arn string) bool {
	switch {
	case strings.Contains(arn, "cluster/"):
		return m.clusterExists(resolveClusterName(arn))
	case strings.Contains(arn, "task-definition/"):
		_, ok := m.resolveTaskDef(arn)
		return ok
	case strings.Contains(arn, "container-instance/"):
		_, ok := m.resolveInstance(arn)
		return ok
	case strings.Contains(arn, "task/"):
		_, ok := m.resolveTask(arn)
		return ok
	case strings.Contains(arn, "service/"):
		for _, s := range m.services.All() {
			if s.ARN == arn {
				return true
			}
		}

		return false
	default:
		return false
	}
}
