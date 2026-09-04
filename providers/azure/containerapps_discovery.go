package azure

import (
	"context"

	"github.com/stackshy/cloudemu/v2/providers/azure/containerapps"
	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
)

// containerAppsDiscovery projects Azure Container Apps resources — managed
// environments and container apps (Microsoft.App) — into the cross-service
// inventory so they surface in Resource Graph / `az resource list`. Container
// Apps are Azure-only with no shared cross-cloud driver, so this rides the
// generic GenericResources projection (like azureMLDiscovery and
// managedIdentityDiscovery) rather than a shared walker. A container app's row
// carries the vCPU/memory and scale.minReplicas a discoverer prices on, so the
// cost signals survive a create -> discover round trip.
type containerAppsDiscovery struct{ m *containerapps.Mock }

func (d containerAppsDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	envs, err := d.m.DiscoverEnvironments(ctx)
	if err != nil {
		return nil, err
	}

	apps, err := d.m.DiscoverApps(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]resourcediscovery.DiscoveredResource, 0, len(envs)+len(apps))

	for i := range envs {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceContainerApps,
			Type:    resourcediscovery.TypeManagedEnvironment,
			ID:      envs[i].Name,
			ARN:     envs[i].ARMID(),
			Region:  envs[i].Location,
			Tags:    envs[i].Tags,
		})
	}

	for i := range apps {
		out = append(out, resourcediscovery.DiscoveredResource{
			Service: resourcediscovery.ServiceContainerApps,
			Type:    resourcediscovery.TypeContainerApp,
			ID:      apps[i].Name,
			ARN:     apps[i].ARMID(),
			Region:  apps[i].Location,
			Tags:    apps[i].Tags,
			Attrs:   resourcediscovery.Attributes{Properties: appCostProps(&apps[i])},
		})
	}

	return out, nil
}

// appCostProps projects the cost-relevant slice of a container app into the ARG
// row's properties bag: the template's containers (each with its
// resources.cpu/.memory) and scale.minReplicas/maxReplicas. Returns nil when the
// app carries no template, so an empty properties block is omitted.
func appCostProps(a *containerapps.ContainerApp) map[string]any {
	tmpl := map[string]any{}

	if containers := containerProps(a.Template.Containers); len(containers) > 0 {
		tmpl["containers"] = containers
	}

	if s := a.Template.Scale; s != nil {
		scale := map[string]any{}
		if s.MinReplicas != nil {
			scale["minReplicas"] = *s.MinReplicas
		}

		if s.MaxReplicas != nil {
			scale["maxReplicas"] = *s.MaxReplicas
		}

		if len(scale) > 0 {
			tmpl["scale"] = scale
		}
	}

	if len(tmpl) == 0 {
		return nil
	}

	return map[string]any{"template": tmpl}
}

func containerProps(containers []containerapps.Container) []any {
	out := make([]any, 0, len(containers))

	for i := range containers {
		c := containers[i]
		entry := map[string]any{}

		if c.Name != "" {
			entry["name"] = c.Name
		}

		if c.Image != "" {
			entry["image"] = c.Image
		}

		if c.Resources != nil {
			res := map[string]any{}
			if c.Resources.CPU != 0 {
				res["cpu"] = c.Resources.CPU
			}

			if c.Resources.Memory != "" {
				res["memory"] = c.Resources.Memory
			}

			if len(res) > 0 {
				entry["resources"] = res
			}
		}

		out = append(out, entry)
	}

	return out
}
