package ecs

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

// The clone helpers deep-copy every slice field out of a stored record so that
// callers can never mutate internal state through a returned value, and so that
// a reader iterating a clone never races a concurrent copy-on-write Set. They
// mirror the bedrock package's clone-on-read discipline.

// cloneCluster deep-copies a cluster's Tags, Settings, capacity-provider, and
// raw configuration fields.
func cloneCluster(c *driver.Cluster) driver.Cluster {
	out := *c
	out.Tags = copyTags(c.Tags)
	out.Settings = append([]driver.Setting(nil), c.Settings...)
	out.Configuration = cloneRaw(c.Configuration)
	out.CapacityProviders = append([]string(nil), c.CapacityProviders...)
	out.DefaultCapacityProviderStrategy =
		append([]driver.CapacityProviderStrategyItem(nil), c.DefaultCapacityProviderStrategy...)

	return out
}

// cloneTaskDef deep-copies a task definition's ContainerDefinitions (including
// their nested slices), RequiresCompatibilities, Tags, and every task-level
// slice/pointer field (volumes, placement constraints, inference accelerators,
// ephemeral storage, runtime platform, and proxy configuration).
func cloneTaskDef(td *driver.TaskDefinition) driver.TaskDefinition {
	out := *td
	out.ContainerDefinitions = cloneContainerDefs(td.ContainerDefinitions)
	out.RequiresCompatibilities = append([]string(nil), td.RequiresCompatibilities...)
	out.Volumes = cloneVolumes(td.Volumes)
	out.PlacementConstraints = append([]driver.PlacementConstraint(nil), td.PlacementConstraints...)
	out.InferenceAccelerators = append([]driver.InferenceAccelerator(nil), td.InferenceAccelerators...)
	out.EphemeralStorage = cloneEphemeralStorage(td.EphemeralStorage)
	out.RuntimePlatform = cloneRuntimePlatform(td.RuntimePlatform)
	out.ProxyConfiguration = cloneProxyConfiguration(td.ProxyConfiguration)
	out.Tags = copyTags(td.Tags)

	return out
}

// cloneContainerDefs deep-copies a slice of container definitions and every
// nested slice/map/pointer/raw-JSON field so a returned clone never aliases
// stored state.
func cloneContainerDefs(in []driver.ContainerDefinition) []driver.ContainerDefinition {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.ContainerDefinition, len(in))

	for i := range in {
		cd := in[i]
		cd.PortMappings = append([]driver.PortMapping(nil), cd.PortMappings...)
		cd.Command = append([]string(nil), cd.Command...)
		cd.EntryPoint = append([]string(nil), cd.EntryPoint...)
		cd.Environment = append([]driver.KeyValue(nil), cd.Environment...)
		cd.Secrets = append([]driver.Secret(nil), cd.Secrets...)
		cd.EnvironmentFiles = append([]driver.EnvironmentFile(nil), cd.EnvironmentFiles...)
		cd.DependsOn = append([]driver.ContainerDependency(nil), cd.DependsOn...)
		cd.MountPoints = append([]driver.MountPoint(nil), cd.MountPoints...)
		cd.VolumesFrom = append([]driver.VolumeFrom(nil), cd.VolumesFrom...)
		cd.Ulimits = append([]driver.Ulimit(nil), cd.Ulimits...)
		cd.ResourceRequirements = append([]driver.ResourceRequirement(nil), cd.ResourceRequirements...)
		cd.HealthCheck = cloneHealthCheck(cd.HealthCheck)
		cd.LogConfiguration = cloneLogConfiguration(cd.LogConfiguration)
		cd.LinuxParameters = cloneRaw(cd.LinuxParameters)
		cd.FirelensConfiguration = cloneRaw(cd.FirelensConfiguration)
		out[i] = cd
	}

	return out
}

// cloneHealthCheck returns a fresh copy of a health check, including its Command
// slice, or nil.
func cloneHealthCheck(in *driver.HealthCheck) *driver.HealthCheck {
	if in == nil {
		return nil
	}

	out := *in
	out.Command = append([]string(nil), in.Command...)

	return &out
}

// cloneLogConfiguration returns a fresh copy of a log configuration, including
// its Options map and SecretOptions slice, or nil.
func cloneLogConfiguration(in *driver.LogConfiguration) *driver.LogConfiguration {
	if in == nil {
		return nil
	}

	out := *in
	out.Options = copyStringMap(in.Options)
	out.SecretOptions = append([]driver.Secret(nil), in.SecretOptions...)

	return &out
}

// cloneVolumes deep-copies a slice of volumes, including each volume's Host
// pointer and raw-JSON configuration fields.
func cloneVolumes(in []driver.Volume) []driver.Volume {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Volume, len(in))

	for i := range in {
		v := in[i]

		if in[i].Host != nil {
			host := *in[i].Host
			v.Host = &host
		}

		v.EFSVolumeConfiguration = cloneRaw(in[i].EFSVolumeConfiguration)
		v.DockerVolumeConfiguration = cloneRaw(in[i].DockerVolumeConfiguration)
		out[i] = v
	}

	return out
}

// cloneEphemeralStorage returns a fresh copy of an ephemeral-storage config, or nil.
func cloneEphemeralStorage(in *driver.EphemeralStorage) *driver.EphemeralStorage {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// cloneRuntimePlatform returns a fresh copy of a runtime platform, or nil.
func cloneRuntimePlatform(in *driver.RuntimePlatform) *driver.RuntimePlatform {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

// cloneProxyConfiguration returns a fresh copy of a proxy configuration,
// including its Properties slice, or nil.
func cloneProxyConfiguration(in *driver.ProxyConfiguration) *driver.ProxyConfiguration {
	if in == nil {
		return nil
	}

	out := *in
	out.Properties = append([]driver.KeyValue(nil), in.Properties...)

	return &out
}

// cloneRaw returns a fresh copy of a raw-JSON passthrough field, or nil.
func cloneRaw(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}

	return append(json.RawMessage(nil), in...)
}

// copyStringMap returns a fresh copy of a string map, or nil.
func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

// cloneTask deep-copies a task's Containers (including each container's
// NetworkBindings), Attachments (including each attachment's Details), and
// Tags slices.
func cloneTask(t *driver.Task) driver.Task {
	out := *t
	out.Containers = cloneContainers(t.Containers)
	out.Attachments = cloneAttachments(t.Attachments)
	out.Tags = copyTags(t.Tags)

	return out
}

// cloneContainers deep-copies a slice of containers and each container's
// NetworkBindings slice.
func cloneContainers(in []driver.Container) []driver.Container {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Container, len(in))

	for i := range in {
		c := in[i]
		c.NetworkBindings = append([]driver.NetworkBinding(nil), in[i].NetworkBindings...)
		out[i] = c
	}

	return out
}

// cloneAttachments deep-copies a slice of attachments and each attachment's
// Details slice.
func cloneAttachments(in []driver.Attachment) []driver.Attachment {
	if len(in) == 0 {
		return nil
	}

	out := make([]driver.Attachment, len(in))

	for i := range in {
		a := in[i]
		a.Details = append([]driver.KeyValue(nil), in[i].Details...)
		out[i] = a
	}

	return out
}

// cloneService deep-copies a service's slice and pointer fields (deployments,
// events, load balancers, service registries, capacity-provider strategy, the
// deployment/network configurations, and the health-check grace period) so
// callers can never mutate stored state and copy-on-write appends never alias.
func cloneService(s *driver.Service) driver.Service {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.Deployments = append([]driver.Deployment(nil), s.Deployments...)
	out.Events = append([]driver.ServiceEvent(nil), s.Events...)
	out.LoadBalancers = append([]driver.LoadBalancer(nil), s.LoadBalancers...)
	out.ServiceRegistries = append([]driver.ServiceRegistry(nil), s.ServiceRegistries...)
	out.CapacityProviderStrategy = append([]driver.CapacityProviderStrategyItem(nil), s.CapacityProviderStrategy...)
	out.DeploymentConfiguration = cloneDeploymentConfig(s.DeploymentConfiguration)
	out.NetworkConfiguration = cloneNetworkConfig(s.NetworkConfiguration)
	out.HealthCheckGracePeriodSeconds = cloneIntPtr(s.HealthCheckGracePeriodSeconds)

	return out
}

// cloneDeploymentConfig deep-copies a deployment configuration, including its
// pointer fields and nested circuit breaker.
func cloneDeploymentConfig(in *driver.DeploymentConfiguration) *driver.DeploymentConfiguration {
	if in == nil {
		return nil
	}

	out := *in
	out.MaximumPercent = cloneIntPtr(in.MaximumPercent)
	out.MinimumHealthyPercent = cloneIntPtr(in.MinimumHealthyPercent)

	if in.DeploymentCircuitBreaker != nil {
		cb := *in.DeploymentCircuitBreaker
		out.DeploymentCircuitBreaker = &cb
	}

	return &out
}

// cloneNetworkConfig deep-copies a network configuration, including its awsvpc
// configuration and that configuration's slices.
func cloneNetworkConfig(in *driver.NetworkConfiguration) *driver.NetworkConfiguration {
	if in == nil || in.AwsVpcConfiguration == nil {
		return in
	}

	vpc := *in.AwsVpcConfiguration
	vpc.Subnets = append([]string(nil), in.AwsVpcConfiguration.Subnets...)
	vpc.SecurityGroups = append([]string(nil), in.AwsVpcConfiguration.SecurityGroups...)

	return &driver.NetworkConfiguration{AwsVpcConfiguration: &vpc}
}

// cloneIntPtr returns a fresh copy of an *int, or nil.
func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}
