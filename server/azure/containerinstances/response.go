package containerinstances

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

// toGroupJSON converts a driver ContainerGroup into its ARM resource shape. The
// id carries the scope the group was created in, falling back to the request
// path's scope for groups created through the portable API.
func toGroupJSON(rp *azurearm.ResourcePath, g *driver.ContainerGroup) containerGroupJSON {
	sub := g.Scope.Subscription
	if sub == "" {
		sub = rp.Subscription
	}

	rg := g.Scope.ResourceGroup
	if rg == "" {
		rg = rp.ResourceGroup
	}

	return containerGroupJSON{
		ID:       azurearm.BuildResourceID(sub, rg, providerName, typeContainerGroups, g.Name),
		Name:     g.Name,
		Type:     containerGroupResourceType,
		Location: g.Location,
		Tags:     g.Tags,
		Properties: &containerGroupProperties{
			OSType:            g.OSType,
			RestartPolicy:     g.RestartPolicy,
			ProvisioningState: g.ProvisioningState,
			Containers:        toContainerJSONs(g.Containers),
			InstanceView:      &groupInstanceView{State: g.State, Events: []any{}},
		},
	}
}

// toContainerJSONs maps the group's containers (with their observed state) onto
// the ARM container entries.
func toContainerJSONs(in []driver.ContainerInstance) []containerJSON {
	out := make([]containerJSON, 0, len(in))

	for i := range in {
		c := &in[i]
		out = append(out, containerJSON{
			Name: c.Name,
			Properties: &containerPropsJSON{
				Image:                c.Image,
				Command:              c.Command,
				EnvironmentVariables: toEnvVarJSONs(c.Env),
				Resources:            resourcesJSON(c),
				InstanceView: &containerInstanceView{
					CurrentState: toStateJSON(&c.Current),
				},
			},
		})
	}

	return out
}

// resourcesJSON builds the ARM resource requirements from a container's
// requested CPU/memory, omitting the block when neither is set.
func resourcesJSON(c *driver.ContainerInstance) *resourceRequirements {
	if c.CPU == 0 && c.MemoryInGB == 0 {
		return nil
	}

	return &resourceRequirements{
		Requests: &resourceRequests{CPU: c.CPU, MemoryInGB: c.MemoryInGB},
	}
}

// toEnvVarJSONs maps driver env vars onto ARM env entries.
func toEnvVarJSONs(in []driver.EnvVar) []envVarJSON {
	if len(in) == 0 {
		return nil
	}

	out := make([]envVarJSON, 0, len(in))
	for _, e := range in {
		out = append(out, envVarJSON{Name: e.Name, Value: e.Value})
	}

	return out
}

// toStateJSON maps a driver ContainerState onto ARM's currentState. A terminated
// container carries its exit code and finish time; a running one carries only
// its start time.
func toStateJSON(s *driver.ContainerState) *containerStateJSON {
	out := &containerStateJSON{
		State:        s.State,
		DetailStatus: s.DetailStatus,
	}

	if !s.StartTime.IsZero() {
		out.StartTime = s.StartTime.UTC().Format(timeFormat)
	}

	if s.HasExitCode {
		code := s.ExitCode
		out.ExitCode = &code
	}

	if !s.FinishTime.IsZero() {
		out.FinishTime = s.FinishTime.UTC().Format(timeFormat)
	}

	return out
}
