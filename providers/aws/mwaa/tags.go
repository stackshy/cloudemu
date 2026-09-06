package mwaa

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/mwaa/driver"
)

// TagResource adds or overwrites tags on an environment.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	name, err := environmentNameFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateEnvTags(name, resourceArn, func(t map[string]string) {
		for k, v := range tags {
			t[k] = v
		}
	})
}

// UntagResource removes tags by key from an environment.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	name, err := environmentNameFromARN(resourceArn)
	if err != nil {
		return err
	}

	return m.updateEnvTags(name, resourceArn, func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	})
}

// ListTagsForResource returns a copy of an environment's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	name, err := environmentNameFromARN(resourceArn)
	if err != nil {
		return nil, err
	}

	e, ok := m.envs.Get(name)
	if !ok {
		return nil, notFound(name)
	}

	return copyTags(e.Tags), nil
}

// environmentNameFromARN extracts and validates the environment name from an
// MWAA resource ARN.
func environmentNameFromARN(resourceArn string) (string, error) {
	kind, name := resourceNameFromARN(resourceArn)
	if kind != kindEnvironment || name == "" {
		return "", validation("invalid resource ARN: %q", resourceArn)
	}

	return name, nil
}

func (m *Mock) updateEnvTags(name, arn string, mutate func(map[string]string)) error {
	ok := m.envs.Update(name, func(e driver.Environment) driver.Environment {
		if e.Tags == nil {
			e.Tags = map[string]string{}
		}

		mutate(e.Tags)

		return e
	})
	if !ok {
		return notFound(arn)
	}

	return nil
}
