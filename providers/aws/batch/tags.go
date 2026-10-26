package batch

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/batch/driver"
)

// kindFromARN reports which Batch resource kind an ARN names.
func kindFromARN(arn string) string {
	switch {
	case strings.Contains(arn, ":"+kindComputeEnvironment+"/"):
		return kindComputeEnvironment
	case strings.Contains(arn, ":"+kindJobQueue+"/"):
		return kindJobQueue
	case strings.Contains(arn, ":"+kindJobDefinition+"/"):
		return kindJobDefinition
	case strings.Contains(arn, ":"+kindSchedulingPolicy+"/"):
		return kindSchedulingPolicy
	case strings.Contains(arn, ":"+kindJob+"/"):
		return kindJob
	default:
		return ""
	}
}

func mergeTags(existing, add map[string]string) map[string]string {
	out := copyTags(existing)
	if out == nil {
		out = make(map[string]string, len(add))
	}

	for k, v := range add {
		out[k] = v
	}

	return out
}

func removeTags(existing map[string]string, keys []string) map[string]string {
	out := copyTags(existing)
	for _, k := range keys {
		delete(out, k)
	}

	return out
}

// Tag limits from the Batch TagResource reference: at most 50 tags, keys of
// 1-128 characters, values of at most 256 characters. The "aws:" key prefix is
// reserved for AWS (see "Tagging AWS resources").
const (
	maxTags        = 50
	maxTagKeyLen   = 128
	maxTagValueLen = 256
	reservedPrefix = "aws:"
)

// validateTags checks one tag map against the TagResource limits.
func validateTags(tags map[string]string) error {
	if len(tags) > maxTags {
		return cerrors.Newf(cerrors.InvalidArgument, "a resource can have at most %d tags, got %d", maxTags, len(tags))
	}

	for k, v := range tags {
		switch {
		case k == "" || len(k) > maxTagKeyLen:
			return cerrors.Newf(cerrors.InvalidArgument, "tag key %q must be 1 to %d characters", k, maxTagKeyLen)
		case len(v) > maxTagValueLen:
			return cerrors.Newf(cerrors.InvalidArgument, "tag value for key %q must be at most %d characters", k, maxTagValueLen)
		case strings.HasPrefix(strings.ToLower(k), reservedPrefix):
			return cerrors.Newf(cerrors.InvalidArgument, "tag key %q uses the reserved prefix %q", k, reservedPrefix)
		}
	}

	return nil
}

// TagResource adds or replaces tags on the resource named by arn. Child jobs of
// an array job can't be tagged.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	if len(tags) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "tags is required")
	}

	if err := validateTags(tags); err != nil {
		return err
	}

	var limitErr error

	err := m.mutateTags(arn, func(existing map[string]string) map[string]string {
		merged := mergeTags(existing, tags)
		if len(merged) > maxTags {
			limitErr = cerrors.Newf(cerrors.InvalidArgument, "a resource can have at most %d tags", maxTags)

			return existing
		}

		return merged
	})
	if err != nil {
		return err
	}

	return limitErr
}

// UntagResource removes the given tag keys from the resource named by arn.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	return m.mutateTags(arn, func(existing map[string]string) map[string]string {
		return removeTags(existing, keys)
	})
}

// ListTagsForResource returns the tags on the resource named by arn.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	kind := kindFromARN(arn)
	if kind == "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported resource ARN %q", arn)
	}

	tags, ok := m.tagsOf(kind, nameFromARN(kind, arn))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %q does not exist", arn)
	}

	return copyTags(tags), nil
}

// tagsOf returns the stored tags of the named resource of the given kind.
func (m *Mock) tagsOf(kind, name string) (map[string]string, bool) {
	switch kind {
	case kindComputeEnvironment:
		ce, ok := m.computeEnvs.Get(name)

		return ce.Tags, ok
	case kindJobQueue:
		q, ok := m.jobQueues.Get(name)

		return q.Tags, ok
	case kindJobDefinition:
		jd, ok := m.jobDefs.Get(name)

		return jd.Tags, ok
	case kindSchedulingPolicy:
		p, ok := m.schedPolicies.Get(name)

		return p.Tags, ok
	case kindJob:
		r, ok := m.jobs.Get(name)

		return r.Job.Tags, ok
	default:
		return nil, false
	}
}

// mutateTags applies fn to the tag map of the resource named by arn.
func (m *Mock) mutateTags(arn string, fn func(map[string]string) map[string]string) error {
	var ok bool

	switch kindFromARN(arn) {
	case kindComputeEnvironment:
		ok = m.computeEnvs.Update(nameFromARN(kindComputeEnvironment, arn),
			func(ce driver.ComputeEnvironment) driver.ComputeEnvironment {
				next := cloneComputeEnvironment(ce)
				next.Tags = fn(next.Tags)

				return next
			})
	case kindJobQueue:
		ok = m.jobQueues.Update(nameFromARN(kindJobQueue, arn),
			func(q driver.JobQueue) driver.JobQueue {
				next := cloneJobQueue(q)
				next.Tags = fn(next.Tags)

				return next
			})
	case kindJobDefinition:
		ok = m.jobDefs.Update(nameFromARN(kindJobDefinition, arn),
			func(jd driver.JobDefinition) driver.JobDefinition {
				next := cloneJobDefinition(jd)
				next.Tags = fn(next.Tags)

				return next
			})
	case kindSchedulingPolicy:
		ok = m.schedPolicies.Update(nameFromARN(kindSchedulingPolicy, arn),
			func(p driver.SchedulingPolicy) driver.SchedulingPolicy {
				next := cloneSchedulingPolicy(p)
				next.Tags = fn(next.Tags)

				return next
			})
	case kindJob:
		// jobsMu: publishJobEvents rewrites job records under it.
		m.jobsMu.Lock()
		ok = m.jobs.Update(nameFromARN(kindJob, arn), func(r jobRecord) jobRecord {
			next := cloneJobRecord(r)
			next.Job.Tags = fn(next.Job.Tags)

			return next
		})
		m.jobsMu.Unlock()
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported resource ARN %q", arn)
	}

	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q does not exist", arn)
	}

	return nil
}
