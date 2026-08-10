package sfn

import (
	"context"
)

// tagTarget abstracts the tag map access shared by state machines and
// activities so the tag operations don't branch on resource kind.
type tagTarget struct {
	lock   func()
	unlock func()
	get    func() map[string]string
	set    func(map[string]string)
}

// resolveTagTarget locates the taggable resource behind an ARN. Step Functions
// tags apply to state machines and activities.
func (m *Mock) resolveTagTarget(arn string) (tagTarget, error) {
	if validStateMachineARN(arn) {
		sd, ok := m.machines.Get(arn)
		if !ok {
			return tagTarget{}, resourceNotFound(arn)
		}

		return tagTarget{
			lock: sd.mu.Lock, unlock: sd.mu.Unlock,
			get: func() map[string]string { return sd.sm.Tags },
			set: func(t map[string]string) { sd.sm.Tags = t },
		}, nil
	}

	if validActivityARN(arn) {
		ad, ok := m.activities.Get(arn)
		if !ok {
			return tagTarget{}, resourceNotFound(arn)
		}

		return tagTarget{
			lock: ad.mu.Lock, unlock: ad.mu.Unlock,
			get: func() map[string]string { return ad.act.Tags },
			set: func(t map[string]string) { ad.act.Tags = t },
		}, nil
	}

	return tagTarget{}, invalidArn("%q is not a valid taggable Step Functions ARN", arn)
}

func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return err
	}

	target.lock()
	defer target.unlock()

	merged := copyTags(target.get())
	if merged == nil {
		merged = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		merged[k] = v
	}

	if len(merged) > maxTags {
		return tooManyTags("resource %q would exceed the %d-tag limit", arn, maxTags)
	}

	target.set(merged)

	return nil
}

func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return err
	}

	target.lock()
	defer target.unlock()

	current := copyTags(target.get())
	for _, k := range tagKeys {
		delete(current, k)
	}

	target.set(current)

	return nil
}

func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	target, err := m.resolveTagTarget(arn)
	if err != nil {
		return nil, err
	}

	target.lock()
	defer target.unlock()

	return copyTags(target.get()), nil
}
