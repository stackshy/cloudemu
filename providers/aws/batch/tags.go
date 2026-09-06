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

// TagResource adds or replaces tags on the resource named by arn.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	return m.mutateTags(arn, func(existing map[string]string) map[string]string {
		return mergeTags(existing, tags)
	})
}

// UntagResource removes the given tag keys from the resource named by arn.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	return m.mutateTags(arn, func(existing map[string]string) map[string]string {
		return removeTags(existing, keys)
	})
}

// ListTagsForResource returns the tags on the resource named by arn.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	switch kindFromARN(arn) {
	case kindComputeEnvironment:
		if ce, ok := m.computeEnvs.Get(nameFromARN(kindComputeEnvironment, arn)); ok {
			return copyTags(ce.Tags), nil
		}
	case kindJobQueue:
		if q, ok := m.jobQueues.Get(nameFromARN(kindJobQueue, arn)); ok {
			return copyTags(q.Tags), nil
		}
	case kindJobDefinition:
		if jd, ok := m.jobDefs.Get(nameFromARN(kindJobDefinition, arn)); ok {
			return copyTags(jd.Tags), nil
		}
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported resource ARN %q", arn)
	}

	return nil, cerrors.Newf(cerrors.NotFound, "resource %q does not exist", arn)
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
	default:
		return cerrors.Newf(cerrors.InvalidArgument, "unsupported resource ARN %q", arn)
	}

	if !ok {
		return cerrors.Newf(cerrors.NotFound, "resource %q does not exist", arn)
	}

	return nil
}
