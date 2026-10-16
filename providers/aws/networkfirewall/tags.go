package networkfirewall

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// TagResource adds tags to any taggable resource (firewall, policy, rule group,
// TLS inspection configuration, VPC endpoint association) by ARN. Tagging does
// not rotate the resource's update token.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateARN("ResourceArn", arn); err != nil {
		return err
	}

	t := m.taggableByARN(arn)
	if t == nil {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
	}

	if len(t)+len(tags) > maxTags {
		return invalid("at most %d tags are allowed", maxTags)
	}

	for k, v := range tags {
		t[k] = v
	}

	return nil
}

// UntagResource removes tags from a resource by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateARN("ResourceArn", arn); err != nil {
		return err
	}

	t := m.taggableByARN(arn)
	if t == nil {
		return cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
	}

	for _, k := range keys {
		delete(t, k)
	}

	return nil
}

// ListTagsForResource returns a copy of the tags on the resource with the given ARN.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := validateARN("ResourceArn", arn); err != nil {
		return nil, err
	}

	t := m.taggableByARN(arn)
	if t == nil {
		return nil, cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
	}

	return copyTags(t), nil
}

// taggableByARN returns the mutable Tags map of the resource with the given ARN,
// creating it if nil, or nil when no resource matches.
func (m *Mock) taggableByARN(arn string) map[string]string {
	for _, f := range m.firewalls.SortedValues() {
		if f.ARN == arn {
			if f.Tags == nil {
				f.Tags = map[string]string{}
			}

			return f.Tags
		}
	}

	for _, p := range m.policies.SortedValues() {
		if p.ARN == arn {
			if p.Tags == nil {
				p.Tags = map[string]string{}
			}

			return p.Tags
		}
	}

	for _, rg := range m.ruleGroups.SortedValues() {
		if rg.ARN == arn {
			if rg.Tags == nil {
				rg.Tags = map[string]string{}
			}

			return rg.Tags
		}
	}

	return m.taggableExtra(arn)
}

// taggableExtra covers the resource families added after the original three.
func (m *Mock) taggableExtra(arn string) map[string]string {
	for _, t := range m.tlsConfigs.SortedValues() {
		if t.ARN == arn {
			if t.Tags == nil {
				t.Tags = map[string]string{}
			}

			return t.Tags
		}
	}

	if a, ok := m.associations.Get(arn); ok {
		if a.Tags == nil {
			a.Tags = map[string]string{}
		}

		return a.Tags
	}

	return nil
}
