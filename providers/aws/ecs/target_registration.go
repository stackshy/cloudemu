package ecs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// TargetRegistrar is the local interface the ECS mock consumes to register and
// deregister a service's RUNNING tasks against the ELBv2 target groups
// configured on its loadBalancers[]. The AWS provider factory wires the
// concrete ELBv2 mock in, mirroring the ManagedInstanceLauncher shape.
type TargetRegistrar interface {
	RegisterTargets(ctx context.Context, targetGroupARN string, targets []lbdriver.Target) error
	DeregisterTargets(ctx context.Context, targetGroupARN string, targets []lbdriver.Target) error
}

// SetTargetRegistrar wires the ELBv2 mock a service's loadBalancers[] register
// running tasks against. Safe to leave unset — services with loadBalancers
// then converge without ever touching a target group.
func (m *Mock) SetTargetRegistrar(r TargetRegistrar) {
	m.registrar = r
}

// registerTaskTargets registers a just-launched RUNNING task against every
// target group configured on the service's loadBalancers[], matching each
// entry's containerName/containerPort to where the task actually landed.
func (m *Mock) registerTaskTargets(ctx context.Context, svc *driver.Service, td *driver.TaskDefinition, task *driver.Task) {
	if m.registrar == nil || len(svc.LoadBalancers) == 0 {
		return
	}

	for i := range svc.LoadBalancers {
		lb := &svc.LoadBalancers[i]

		target, ok := m.resolveTarget(td, task, lb.ContainerName, lb.ContainerPort)
		if !ok {
			continue
		}

		_ = m.registrar.RegisterTargets(ctx, lb.TargetGroupARN, []lbdriver.Target{target})
	}
}

// deregisterTaskTargets removes a task the service scheduler is about to stop
// from every target group configured on the service's loadBalancers[].
func (m *Mock) deregisterTaskTargets(ctx context.Context, svc *driver.Service, task *driver.Task) {
	if m.registrar == nil || len(svc.LoadBalancers) == 0 {
		return
	}

	td, ok := m.resolveTaskDef(task.TaskDefinitionARN)
	if !ok {
		return
	}

	for i := range svc.LoadBalancers {
		lb := &svc.LoadBalancers[i]

		target, ok := m.resolveTarget(td, task, lb.ContainerName, lb.ContainerPort)
		if !ok {
			continue
		}

		_ = m.registrar.DeregisterTargets(ctx, lb.TargetGroupARN, []lbdriver.Target{target})
	}
}

// resolveTarget computes the target ELBv2 registers for one loadBalancers[]
// entry, matching real ECS service-load-balancing semantics: an awsvpc task
// (Fargate, or EC2 with awsvpc trunking) is addressed directly at its ENI
// private IP and the container port; a bridge/host EC2 task is addressed at
// its container instance's EC2 id and the host port the container port was
// bound to. false is returned when the task carries no resolvable placement
// yet (e.g. PENDING with no reserved capacity).
func (m *Mock) resolveTarget(
	td *driver.TaskDefinition, task *driver.Task, containerName string, containerPort int,
) (lbdriver.Target, bool) {
	if td.NetworkMode == networkModeAwsvpc {
		ip := eniPrivateIP(task.Attachments)
		if ip == "" {
			return lbdriver.Target{}, false
		}

		return lbdriver.Target{ID: ip, Port: containerPort}, true
	}

	if task.ContainerInstanceARN == "" {
		return lbdriver.Target{}, false
	}

	ci, ok := m.instances.Get(task.ContainerInstanceARN)
	if !ok || ci.EC2InstanceID == "" {
		return lbdriver.Target{}, false
	}

	hostPort := hostPortFor(task.Containers, containerName, containerPort)
	if hostPort == 0 {
		return lbdriver.Target{}, false
	}

	return lbdriver.Target{ID: ci.EC2InstanceID, Port: hostPort}, true
}

// eniPrivateIP extracts the privateIPv4Address detail from a task's
// ElasticNetworkInterface attachment, or "" when none is present.
func eniPrivateIP(attachments []driver.Attachment) string {
	for i := range attachments {
		if attachments[i].Type != "ElasticNetworkInterface" {
			continue
		}

		for _, kv := range attachments[i].Details {
			if kv.Name == "privateIPv4Address" {
				return kv.Value
			}
		}
	}

	return ""
}

// hostPortFor looks up the host port bound to a container's containerPort from
// the task's resolved network bindings, or 0 when none was bound (the named
// container is not in the task, or that port was never mapped).
func hostPortFor(containers []driver.Container, containerName string, containerPort int) int {
	for i := range containers {
		if containers[i].Name != containerName {
			continue
		}

		for _, nb := range containers[i].NetworkBindings {
			if nb.ContainerPort == containerPort {
				return nb.HostPort
			}
		}
	}

	return 0
}
