package appflow

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// TagResource adds or overwrites tags on a flow or connector profile.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	kind, name := resourceNameFromARN(resourceArn)
	if name == "" {
		return validation("invalid resource ARN: %q", resourceArn)
	}

	switch kind {
	case kindFlow:
		return m.updateFlowTags(name, resourceArn, func(t map[string]string) {
			for k, v := range tags {
				t[k] = v
			}
		})
	case kindConnectorProfile:
		return m.updateProfileTags(name, resourceArn, func(t map[string]string) {
			for k, v := range tags {
				t[k] = v
			}
		})
	default:
		return validation("invalid resource ARN: %q", resourceArn)
	}
}

// UntagResource removes tags by key from a flow or connector profile.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	kind, name := resourceNameFromARN(resourceArn)
	if name == "" {
		return validation("invalid resource ARN: %q", resourceArn)
	}

	remove := func(t map[string]string) {
		for _, k := range tagKeys {
			delete(t, k)
		}
	}

	switch kind {
	case kindFlow:
		return m.updateFlowTags(name, resourceArn, remove)
	case kindConnectorProfile:
		return m.updateProfileTags(name, resourceArn, remove)
	default:
		return validation("invalid resource ARN: %q", resourceArn)
	}
}

// ListTagsForResource returns a copy of a flow's or connector profile's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, name := resourceNameFromARN(resourceArn)

	switch kind {
	case kindFlow:
		f, ok := m.flows.Get(name)
		if !ok {
			return nil, notFound("flow %s not found", name)
		}

		return copyTags(f.Tags), nil
	case kindConnectorProfile:
		p, ok := m.profiles.Get(name)
		if !ok {
			return nil, notFound("connector profile %s not found", name)
		}

		return copyTags(p.Tags), nil
	default:
		return nil, validation("invalid resource ARN: %q", resourceArn)
	}
}

func (m *Mock) updateFlowTags(name, arn string, mutate func(map[string]string)) error {
	ok := m.flows.Update(name, func(f driver.Flow) driver.Flow {
		if f.Tags == nil {
			f.Tags = map[string]string{}
		}

		mutate(f.Tags)

		return f
	})
	if !ok {
		return notFound("flow %s not found", arn)
	}

	return nil
}

func (m *Mock) updateProfileTags(name, arn string, mutate func(map[string]string)) error {
	ok := m.profiles.Update(name, func(p driver.ConnectorProfile) driver.ConnectorProfile {
		if p.Tags == nil {
			p.Tags = map[string]string{}
		}

		mutate(p.Tags)

		return p
	})
	if !ok {
		return notFound("connector profile %s not found", arn)
	}

	return nil
}
