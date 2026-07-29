package ecs

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// serviceKey builds the store key scoping a service name to its cluster.
func serviceKey(cluster, name string) string {
	return cluster + "/" + name
}

// CreateService creates a service. Running count is set to the desired count to
// model a synchronous, fully-converged deployment.
//
//nolint:gocritic // in is passed by value to satisfy the driver.ECS interface; the copy is cheap for a mock.
func (m *Mock) CreateService(_ context.Context, in driver.CreateServiceInput) (*driver.Service, error) {
	if in.ServiceName == "" {
		return nil, errors.New(errors.InvalidArgument, "serviceName is required")
	}

	cluster := resolveClusterName(in.Cluster)
	key := serviceKey(cluster, in.ServiceName)

	if m.services.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "service %q already exists in cluster %q", in.ServiceName, cluster)
	}

	sched := in.SchedulingStrategy
	if sched == "" {
		sched = "REPLICA"
	}

	svc := &driver.Service{
		ARN:                m.arn("service/" + cluster + "/" + in.ServiceName),
		Name:               in.ServiceName,
		ClusterARN:         m.arn("cluster/" + cluster),
		TaskDefinition:     in.TaskDefinition,
		DesiredCount:       in.DesiredCount,
		RunningCount:       in.DesiredCount,
		PendingCount:       0,
		Status:             statusActive,
		LaunchType:         in.LaunchType,
		SchedulingStrategy: sched,
		CreatedAt:          m.now(),
		Tags:               copyTags(in.Tags),
	}
	m.services.Set(key, svc)

	out := *svc

	return &out, nil
}

// UpdateService updates mutable fields of a service.
func (m *Mock) UpdateService(_ context.Context, in driver.UpdateServiceInput) (*driver.Service, error) {
	cluster := resolveClusterName(in.Cluster)

	svc, ok := m.resolveService(cluster, in.Service)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "service %q not found", in.Service)
	}

	if in.TaskDefinition != "" {
		svc.TaskDefinition = in.TaskDefinition
	}

	if in.DesiredCount != nil {
		svc.DesiredCount = *in.DesiredCount
		svc.RunningCount = *in.DesiredCount
	}

	out := *svc

	return &out, nil
}

// ListServices returns services in a cluster in deterministic order.
func (m *Mock) ListServices(_ context.Context, cluster string) ([]driver.Service, error) {
	want := resolveClusterName(cluster)

	all := m.services.SortedValues()

	out := make([]driver.Service, 0, len(all))

	for _, s := range all {
		if clusterNameFromARN(s.ClusterARN) == want {
			out = append(out, *s)
		}
	}

	return out, nil
}

// DescribeServices resolves services by name or ARN; unresolved ids become failures.
func (m *Mock) DescribeServices(_ context.Context, cluster string, ids []string) ([]driver.Service, []driver.Failure, error) {
	want := resolveClusterName(cluster)

	found := make([]driver.Service, 0, len(ids))
	failures := make([]driver.Failure, 0, len(ids))

	for _, id := range ids {
		if s, ok := m.resolveService(want, id); ok {
			found = append(found, *s)
			continue
		}

		failures = append(failures, driver.Failure{ARN: m.arn("service/" + want + "/" + serviceNameOf(id)), Reason: "MISSING"})
	}

	return found, failures, nil
}

// DeleteService marks a service INACTIVE and returns it.
func (m *Mock) DeleteService(_ context.Context, cluster, service string) (*driver.Service, error) {
	want := resolveClusterName(cluster)

	svc, ok := m.resolveService(want, service)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "service %q not found", service)
	}

	svc.Status = statusInactive

	out := *svc

	return &out, nil
}

// resolveService looks up a service by name or ARN within a cluster.
func (m *Mock) resolveService(cluster, id string) (*driver.Service, bool) {
	name := serviceNameOf(id)

	return m.services.Get(serviceKey(cluster, name))
}

// serviceNameOf returns the bare service name from a name or service ARN
// (…:service/cluster/name).
func serviceNameOf(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}

	return id
}
