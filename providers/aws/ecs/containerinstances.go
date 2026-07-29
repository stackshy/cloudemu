package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// SeedContainerInstance registers a container instance into a cluster and
// returns it. There is no RegisterContainerInstance API in scope, so tests and
// examples use this helper to give Describe/List something to return.
func (m *Mock) SeedContainerInstance(cluster, ec2InstanceID string) *driver.ContainerInstance {
	name := resolveClusterName(cluster)
	ci := &driver.ContainerInstance{
		ARN:            m.arn("container-instance/" + name + "/" + m.hexID()),
		EC2InstanceID:  ec2InstanceID,
		Status:         statusActive,
		AgentConnected: true,
	}
	m.instances.Set(ci.ARN, ci)

	out := *ci

	return &out
}

// ListContainerInstances returns container instances in a cluster.
func (m *Mock) ListContainerInstances(_ context.Context, cluster string) ([]driver.ContainerInstance, error) {
	want := resolveClusterName(cluster)

	all := m.instances.SortedValues()

	out := make([]driver.ContainerInstance, 0, len(all))

	for _, ci := range all {
		if instanceClusterName(ci.ARN) == want {
			out = append(out, *ci)
		}
	}

	return out, nil
}

// DescribeContainerInstances resolves instances by id or ARN; unresolved ids
// become failures.
//
//nolint:dupl // batch resolve-or-fail loop; each Describe binds a distinct resolver and type.
func (m *Mock) DescribeContainerInstances(_ context.Context, _ string, ids []string) (
	[]driver.ContainerInstance, []driver.Failure, error,
) {
	found := make([]driver.ContainerInstance, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if ci, ok := m.resolveInstance(id); ok {
			found = append(found, *ci)
			continue
		}

		failures = append(failures, driver.Failure{ARN: id, Reason: "MISSING"})
	}

	return found, failures, nil
}

// resolveInstance looks up a container instance by full ARN or trailing id.
func (m *Mock) resolveInstance(id string) (*driver.ContainerInstance, bool) {
	if ci, ok := m.instances.Get(id); ok {
		return ci, true
	}

	for _, ci := range m.instances.All() {
		if id != "" && strings.HasSuffix(ci.ARN, "/"+id) {
			return ci, true
		}
	}

	return nil, false
}

// instanceClusterName extracts the cluster name from a container-instance ARN
// (…:container-instance/cluster/id).
func instanceClusterName(arn string) string {
	i := strings.LastIndex(arn, "container-instance/")
	if i < 0 {
		return ""
	}

	rest := arn[i+len("container-instance/"):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}

	return rest
}
