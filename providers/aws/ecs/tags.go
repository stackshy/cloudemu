package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// maxUserTags is the ECS per-resource tag limit. Tags whose key starts with
// the reserved aws: prefix do not count against it.
const maxUserTags = 50

// reservedTagPrefix is the case-insensitive prefix ECS reserves for AWS use on
// both tag keys and values.
const reservedTagPrefix = "aws:"

// recordTags records a resource's creation-time tags under its ARN so that
// ListTagsForResource can return them. A resource with no tags is still
// recorded (as an empty slice) so its ARN is recognized as tag-managed.
func (m *Mock) recordTags(arn string, tags []driver.Tag) {
	m.tags.Set(arn, copyTags(tags))
}

// TagResource merges tags onto a resource, replacing the value of any key that
// already exists and appending new keys, mirroring AWS's upsert semantics. The
// ARN must name an existing taggable resource (a short-format service ARN, a
// predefined Fargate capacity provider, or an unknown resource is an
// InvalidParameterException), no key or value may carry the reserved aws:
// prefix, and the resource may not end up with more than 50 user tags. The
// read-modify-write runs atomically under the store lock (SetIfAbsent seeds the
// entry, then Update mutates it in place) so two concurrent tag writes on the
// same ARN can't lose one another's changes.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags []driver.Tag) error {
	key, err := m.tagKey(resourceARN, true)
	if err != nil {
		return err
	}

	if err := validateTagSet(tags); err != nil {
		return err
	}

	overLimit := false

	m.tags.SetIfAbsent(key, nil)
	m.tags.Update(key, func(existing []driver.Tag) []driver.Tag {
		merged := mergeTags(existing, tags)
		if userTagCount(merged) > maxUserTags {
			overLimit = true
			return existing
		}

		return merged
	})

	if overLimit {
		return tooManyTags()
	}

	return nil
}

// UntagResource removes the given tag keys from a resource. The ARN is
// resolved exactly as TagResource resolves it, and reserved aws: keys cannot be
// removed. The read-modify-write runs atomically under the store lock so it
// can't race a concurrent TagResource on the same ARN.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	key, err := m.tagKey(resourceARN, true)
	if err != nil {
		return err
	}

	drop := make(map[string]bool, len(tagKeys))

	for _, k := range tagKeys {
		if hasReservedPrefix(k) {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"Tag keys with the %q prefix are reserved for AWS use and can't be removed.", reservedTagPrefix)
		}

		drop[k] = true
	}

	m.tags.Update(key, func(existing []driver.Tag) []driver.Tag {
		kept := make([]driver.Tag, 0, len(existing))

		for _, t := range existing {
			if !drop[t.Key] {
				kept = append(kept, t)
			}
		}

		return kept
	})

	return nil
}

// ListTagsForResource returns a resource's tags. The ARN is resolved like
// TagResource resolves it, except that the predefined Fargate capacity
// providers are readable (they carry no tags).
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) ([]driver.Tag, error) {
	key, err := m.tagKey(resourceARN, false)
	if err != nil {
		return nil, err
	}

	tags, _ := m.tags.Get(key)

	return copyTags(tags), nil
}

// liveTags returns a resource's current tags. The tag store keyed by ARN is
// the single authority: every create path seeds it via recordTags and
// TagResource/UntagResource mutate only it, so the entity's own Tags field is a
// create-time snapshot that goes stale after the first tag write. Describe
// paths read through here so they agree with ListTagsForResource. fallback is
// used only when the ARN was never recorded (e.g. a snapshot taken before the
// resource's tags were tracked).
func (m *Mock) liveTags(arn string, fallback []driver.Tag) []driver.Tag {
	if tags, ok := m.tags.Get(arn); ok {
		return copyTags(tags)
	}

	return copyTags(fallback)
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

// validateTagSet enforces the per-request tag rules: at most 50 tags, and no
// key or value carrying the reserved aws: prefix (in any letter case).
func validateTagSet(tags []driver.Tag) error {
	if len(tags) > maxUserTags {
		return tooManyTags()
	}

	for _, t := range tags {
		if hasReservedPrefix(t.Key) || hasReservedPrefix(t.Value) {
			return apiErrf(errors.InvalidArgument, excInvalidParameter,
				"Tag keys and values can't start with %q; the prefix is reserved for AWS use.", reservedTagPrefix)
		}
	}

	return nil
}

func tooManyTags() error {
	return apiErrf(errors.InvalidArgument, excInvalidParameter,
		"A resource can have at most %d tags.", maxUserTags)
}

func hasReservedPrefix(s string) bool {
	return len(s) >= len(reservedTagPrefix) && strings.EqualFold(s[:len(reservedTagPrefix)], reservedTagPrefix)
}

// userTagCount counts the tags that count against the per-resource limit
// (reserved aws: keys are exempt).
func userTagCount(tags []driver.Tag) int {
	n := 0

	for _, t := range tags {
		if !hasReservedPrefix(t.Key) {
			n++
		}
	}

	return n
}

// tagKey resolves a resource ARN to the ARN its tags are stored under: the
// resource's own stored ARN, so a lookup by any accepted spelling reads and
// writes the same entry that Describe* reads. forWrite additionally rejects
// the predefined Fargate capacity providers, which cannot be tagged.
func (m *Mock) tagKey(resourceARN string, forWrite bool) (string, error) {
	resourceType, rest, ok := splitECSARN(resourceARN)
	if !ok {
		return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The ARN %q is not a valid Amazon ECS resource ARN.", resourceARN)
	}

	if resourceType == arnTypeCapacityProvider && isBuiltinCapacityProvider(rest) && forWrite {
		return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The predefined %s capacity provider can't be tagged.", rest)
	}

	if resourceType == arnTypeService && !strings.Contains(rest, "/") {
		return "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"The service ARN %q uses the short ARN format. Migrate the service to the long ARN format "+
				"(service/cluster-name/service-name) to tag it.", resourceARN)
	}

	if key, found := m.storedARN(resourceARN, resourceType, rest); found {
		return key, nil
	}

	return "", apiErrf(errors.NotFound, excInvalidParameter,
		"The specified resource %q does not exist.", resourceARN)
}

// ECS ARN pieces: the service namespace and the resource types TagResource
// accepts.
const (
	arnServiceECS           = "ecs"
	arnTypeCluster          = "cluster"
	arnTypeService          = "service"
	arnTypeTaskDefinition   = "task-definition"
	arnTypeTask             = "task"
	arnTypeContainerInst    = "container-instance"
	arnTypeCapacityProvider = "capacity-provider"
)

// storedARN returns the stored ARN of the resource an ECS ARN names.
func (m *Mock) storedARN(resourceARN, resourceType, rest string) (string, bool) {
	resolvers := map[string]func() (string, bool){
		arnTypeCluster: func() (string, bool) {
			if c, ok := m.clusters.Get(rest); ok {
				return c.ARN, true
			}

			// The implicit default cluster exists even when never created.
			return resourceARN, rest == defaultCluster
		},
		arnTypeService: func() (string, bool) {
			cluster, name, _ := strings.Cut(rest, "/")
			s, ok := m.resolveService(cluster, name)

			return arnOf(s, ok, func(s *driver.Service) string { return s.ARN })
		},
		arnTypeTaskDefinition: func() (string, bool) {
			td, ok := m.resolveTaskDef(resourceARN)

			return arnOf(td, ok, func(td *driver.TaskDefinition) string { return td.ARN })
		},
		arnTypeTask: func() (string, bool) {
			t, ok := m.resolveTask(resourceARN)

			return arnOf(t, ok, func(t *driver.Task) string { return t.ARN })
		},
		arnTypeContainerInst: func() (string, bool) {
			ci, ok := m.resolveInstance(resourceARN)

			return arnOf(ci, ok, func(ci *driver.ContainerInstance) string { return ci.ARN })
		},
		arnTypeCapacityProvider: func() (string, bool) {
			if isBuiltinCapacityProvider(rest) {
				return resourceARN, true
			}

			cp, ok := m.capacityProviders.Get(rest)

			return arnOf(cp, ok && cp.Status == statusActive, func(cp *driver.CapacityProvider) string { return cp.ARN })
		},
	}

	resolve, known := resolvers[resourceType]
	if !known {
		return "", false
	}

	return resolve()
}

// arnOf returns arn(v) when ok, so each resolver reads as one lookup.
func arnOf[T any](v T, ok bool, arn func(T) string) (string, bool) {
	if !ok {
		return "", false
	}

	return arn(v), true
}

// splitECSARN splits arn:<partition>:ecs:<region>:<account>:<type>/<rest> into
// its resource type and the remainder after the first slash.
func splitECSARN(arn string) (resourceType, rest string, ok bool) {
	const fields, serviceField, resourceField = 6, 2, 5

	parts := strings.SplitN(arn, ":", fields)
	if len(parts) != fields || parts[0] != "arn" || parts[serviceField] != arnServiceECS {
		return "", "", false
	}

	resourceType, rest, ok = strings.Cut(parts[resourceField], "/")
	if !ok || rest == "" {
		return "", "", false
	}

	return resourceType, rest, true
}
