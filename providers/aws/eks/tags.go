package eks

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// clusterNameFromARN resolves an EKS cluster ARN
// ("arn:aws:eks:<region>:<account>:cluster/<name>") to the bare cluster name.
// A non-ARN value is returned unchanged.
func clusterNameFromARN(arn string) string {
	const marker = ":cluster/"

	if i := strings.LastIndex(arn, marker); i >= 0 {
		return arn[i+len(marker):]
	}

	return arn
}

// TagResource adds or overwrites tags on a cluster identified by ARN (EKS
// TagResource).
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	name := clusterNameFromARN(arn)

	c, ok := m.clusters.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	if c.Tags == nil {
		c.Tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		c.Tags[k] = v
	}

	m.clusters.Set(name, c)

	return nil
}

// UntagResource removes tags by key from a cluster identified by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	name := clusterNameFromARN(arn)

	c, ok := m.clusters.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	for _, k := range keys {
		delete(c.Tags, k)
	}

	m.clusters.Set(name, c)

	return nil
}

// ListResourceTags returns the tags on a cluster identified by ARN.
func (m *Mock) ListResourceTags(_ context.Context, arn string) (map[string]string, error) {
	name := clusterNameFromARN(arn)

	c, ok := m.clusters.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "cluster %q not found", name)
	}

	out := make(map[string]string, len(c.Tags))
	for k, v := range c.Tags {
		out[k] = v
	}

	return out, nil
}
