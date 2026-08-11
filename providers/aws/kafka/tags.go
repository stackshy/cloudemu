package kafka

import (
	"context"
	"strings"
)

// MSK tags four resource kinds: clusters, configurations, VPC connections, and
// replicators. Tag ops resolve the ResourceArn to its owning store by the ARN's
// resource-kind segment and read/merge/remove that resource's Tags map. An ARN
// that resolves to no known resource is a NotFoundException, matching real MSK.

// tagTarget reads and atomically updates the Tags of the resource an ARN names.
type tagTarget struct {
	// read returns a deep copy of the resource's current tags.
	read func() (map[string]string, error)
	// update applies mutate to the resource's tags under a single lock hold
	// (atomic read-modify-write, so concurrent tag ops cannot lose writes).
	update func(mutate func(map[string]string) map[string]string) error
}

// arnResourceKind returns the resource-kind segment of an MSK ARN
// (arn:aws:kafka:region:account:<kind>/...), or "" if the ARN is malformed.
func arnResourceKind(arn string) string {
	const arnParts = 6

	parts := strings.SplitN(arn, ":", arnParts)
	if len(parts) < arnParts {
		return ""
	}

	kind, _, _ := strings.Cut(parts[5], "/")

	return kind
}

// resolveTagTarget maps a ResourceArn to a tagTarget for its owning resource, or
// NotFoundException when the ARN names no known, taggable MSK resource.
func (m *Mock) resolveTagTarget(arn string) (tagTarget, error) {
	switch arnResourceKind(arn) {
	case "cluster":
		return m.clusterTagTarget(arn), nil
	case "configuration":
		return m.configTagTarget(arn), nil
	case "vpc-connection":
		return m.vpcTagTarget(arn), nil
	case "replicator":
		return m.replicatorTagTarget(arn), nil
	default:
		return tagTarget{}, notFound("resource not found: %s", arn)
	}
}

//nolint:dupl // near-identical per-resource tag targets; the resource kind differs.
func (m *Mock) clusterTagTarget(arn string) tagTarget {
	return tagTarget{
		read: func() (map[string]string, error) {
			cd, err := m.getCluster(arn)
			if err != nil {
				return nil, err
			}

			cd.mu.RLock()
			defer cd.mu.RUnlock()

			return copyTags(cd.cluster.Tags), nil
		},
		update: func(mutate func(map[string]string) map[string]string) error {
			cd, err := m.getCluster(arn)
			if err != nil {
				return err
			}

			cd.mu.Lock()
			defer cd.mu.Unlock()

			cd.cluster.Tags = mutate(copyTags(cd.cluster.Tags))

			return nil
		},
	}
}

//nolint:dupl // near-identical per-resource tag targets; the resource kind differs.
func (m *Mock) configTagTarget(arn string) tagTarget {
	return tagTarget{
		read: func() (map[string]string, error) {
			cd, err := m.getConfig(arn)
			if err != nil {
				return nil, err
			}

			cd.mu.RLock()
			defer cd.mu.RUnlock()

			return copyTags(cd.config.Tags), nil
		},
		update: func(mutate func(map[string]string) map[string]string) error {
			cd, err := m.getConfig(arn)
			if err != nil {
				return err
			}

			cd.mu.Lock()
			defer cd.mu.Unlock()

			cd.config.Tags = mutate(copyTags(cd.config.Tags))

			return nil
		},
	}
}

//nolint:dupl // near-identical per-resource tag targets; the resource kind differs.
func (m *Mock) vpcTagTarget(arn string) tagTarget {
	return tagTarget{
		read: func() (map[string]string, error) {
			vd, err := m.getVpcConnection(arn)
			if err != nil {
				return nil, err
			}

			vd.mu.RLock()
			defer vd.mu.RUnlock()

			return copyTags(vd.vpc.Tags), nil
		},
		update: func(mutate func(map[string]string) map[string]string) error {
			vd, err := m.getVpcConnection(arn)
			if err != nil {
				return err
			}

			vd.mu.Lock()
			defer vd.mu.Unlock()

			vd.vpc.Tags = mutate(copyTags(vd.vpc.Tags))

			return nil
		},
	}
}

//nolint:dupl // near-identical per-resource tag targets; the resource kind differs.
func (m *Mock) replicatorTagTarget(arn string) tagTarget {
	return tagTarget{
		read: func() (map[string]string, error) {
			rd, err := m.getReplicator(arn)
			if err != nil {
				return nil, err
			}

			rd.mu.RLock()
			defer rd.mu.RUnlock()

			return copyTags(rd.replicator.Tags), nil
		},
		update: func(mutate func(map[string]string) map[string]string) error {
			rd, err := m.getReplicator(arn)
			if err != nil {
				return err
			}

			rd.mu.Lock()
			defer rd.mu.Unlock()

			rd.replicator.Tags = mutate(copyTags(rd.replicator.Tags))

			return nil
		},
	}
}

// TagResource merges tags into the resource an ARN names, atomically.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	target, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return err
	}

	return target.update(func(current map[string]string) map[string]string {
		if current == nil {
			current = make(map[string]string, len(tags))
		}

		for k, v := range tags {
			current[k] = v
		}

		return current
	})
}

// ListTagsForResource returns a deep copy of the resource's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	target, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return nil, err
	}

	tags, err := target.read()
	if err != nil {
		return nil, err
	}

	if tags == nil {
		tags = map[string]string{}
	}

	return tags, nil
}

// UntagResource removes the given keys from the resource an ARN names, atomically.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, keys []string) error {
	target, err := m.resolveTagTarget(resourceARN)
	if err != nil {
		return err
	}

	return target.update(func(current map[string]string) map[string]string {
		for _, k := range keys {
			delete(current, k)
		}

		return current
	})
}
