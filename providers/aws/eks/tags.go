package eks

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// arnKind identifies which EKS store an ARN points at.
type arnKind int

const (
	arnCluster arnKind = iota
	arnNodegroup
	arnFargate
	arnAddon
)

// arnParts is the number of colon-separated fields in a full EKS ARN
// (arn:aws:eks:<region>:<account>:<resource-path>); the resource path is the
// sixth field and itself may contain slashes.
const arnParts = 6

// childSegs is the minimum slash-separated segment count for a child-resource
// ARN path (<kind>/<cluster>/<name>[/<uuid>]); clusterSegs is the minimum for a
// cluster ARN path (cluster/<name>).
const (
	childSegs   = 3
	clusterSegs = 2
)

// parseResourceRef resolves an EKS resource ARN to the store it lives in and
// the key that store is indexed by. Real EKS tags every resource type
// (cluster, nodegroup, Fargate profile, add-on); the resource path segment
// selects the kind:
//
//	cluster/<name>
//	nodegroup/<cluster>/<name>[/<uuid>]
//	fargateprofile/<cluster>/<name>[/<uuid>]
//	addon/<cluster>/<name>[/<uuid>]
//
// A bare, non-ARN value is treated as a cluster name so direct programmatic
// callers keep working.
func parseResourceRef(arn string) (kind arnKind, key string) {
	path := arn
	if fields := strings.SplitN(arn, ":", arnParts); len(fields) == arnParts {
		path = fields[arnParts-1]
	}

	segs := strings.Split(path, "/")

	switch segs[0] {
	case "nodegroup":
		if len(segs) >= childSegs {
			return arnNodegroup, nodegroupKey(segs[1], segs[2])
		}
	case "fargateprofile":
		if len(segs) >= childSegs {
			return arnFargate, fargateKey(segs[1], segs[2])
		}
	case "addon":
		if len(segs) >= childSegs {
			return arnAddon, addonKey(segs[1], segs[2])
		}
	case "cluster":
		if len(segs) >= clusterSegs {
			return arnCluster, segs[1]
		}
	}

	// Fallback: the value is not a recognizable EKS resource path, so treat it
	// as a bare cluster name.
	return arnCluster, path
}

// resourceTags returns a copy of the tags on the resource the ARN identifies
// and a setter that writes an updated tag map back. NotFound is returned when
// the resource does not exist. Callers hold m.mu.
func (m *Mock) resourceTags(arn string) (tags map[string]string, set func(map[string]string), err error) {
	kind, key := parseResourceRef(arn)

	//nolint:exhaustive // arnCluster (and any fallback) is handled by the default branch.
	switch kind {
	case arnNodegroup:
		ng, ok := m.nodegroups.Get(key)
		if !ok {
			return nil, nil, cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
		}

		return ng.Tags, func(t map[string]string) { ng.Tags = t; m.nodegroups.Set(key, ng) }, nil
	case arnFargate:
		fp, ok := m.fargateProfiles.Get(key)
		if !ok {
			return nil, nil, cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
		}

		return fp.Tags, func(t map[string]string) { fp.Tags = t; m.fargateProfiles.Set(key, fp) }, nil
	case arnAddon:
		ad, ok := m.addons.Get(key)
		if !ok {
			return nil, nil, cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
		}

		return ad.Tags, func(t map[string]string) { ad.Tags = t; m.addons.Set(key, ad) }, nil
	default: // arnCluster
		c, ok := m.clusters.Get(key)
		if !ok {
			return nil, nil, cerrors.Newf(cerrors.NotFound, "resource %q not found", arn)
		}

		return c.Tags, func(t map[string]string) { c.Tags = t; m.clusters.Set(key, c) }, nil
	}
}

// TagResource adds or overwrites tags on any EKS resource identified by ARN
// (cluster, nodegroup, Fargate profile, or add-on).
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, set, err := m.resourceTags(arn)
	if err != nil {
		return err
	}

	merged := copyTags(cur)
	if merged == nil {
		merged = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		merged[k] = v
	}

	set(merged)

	return nil
}

// UntagResource removes tags by key from any EKS resource identified by ARN.
func (m *Mock) UntagResource(_ context.Context, arn string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, set, err := m.resourceTags(arn)
	if err != nil {
		return err
	}

	merged := copyTags(cur)
	for _, k := range keys {
		delete(merged, k)
	}

	set(merged)

	return nil
}

// ListResourceTags returns the tags on any EKS resource identified by ARN.
func (m *Mock) ListResourceTags(_ context.Context, arn string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cur, _, err := m.resourceTags(arn)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, len(cur))
	for k, v := range cur {
		out[k] = v
	}

	return out, nil
}
