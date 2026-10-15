package aws

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/resourcediscovery"
	sagemakerdriver "github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// sagemakerDiscovery projects the durable, inventory-relevant SageMaker
// resources (models, endpoints, notebook instances, endpoint configs, pipelines,
// feature groups, model package groups, clusters, projects, monitoring
// schedules, images, workteams) into the cross-service inventory. Transient jobs (training/processing/etc.) are intentionally
// excluded. Real inventory APIs surface the standing resources, not job runs.
type sagemakerDiscovery struct{ m smMock }

// smMock is the subset of the SageMaker mock discovery reads. ListTags reads
// the authoritative ARN-keyed tag store (the target of AddTags / the Resource
// Groups Tagging API), which is seeded with create-time tags too, so discovery
// reflects tags applied through either path rather than the stale struct copy.
type smMock interface {
	ListModels(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Model], error)
	ListEndpoints(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Endpoint], error)
	ListNotebookInstances(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.NotebookInstance], error)   //nolint:lll // SDK-shaped literal
	ListEndpointConfigs(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.EndpointConfig], error)       //nolint:lll // SDK-shaped literal
	ListModelPackageGroups(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.ModelPackageGroup], error) //nolint:lll // SDK-shaped literal
	ListPipelines(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Pipeline], error)
	ListFeatureGroups(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.FeatureGroup], error)
	ListClusters(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Cluster], error)
	ListProjects(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Project], error)
	ListMonitoringSchedules(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.MonitoringSchedule], error) //nolint:lll // SDK-shaped literal
	ListImages(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Image], error)
	ListWorkteams(ctx context.Context, opts sagemakerdriver.ListOptions) (sagemakerdriver.ListPage[sagemakerdriver.Workteam], error)
	ListTags(ctx context.Context, resourceARN string) ([]sagemakerdriver.Tag, error)
}

func smTags(tags []sagemakerdriver.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

// smItem is one discoverable resource before its live tags are read.
type smItem struct{ typ, id, arn string }

// smItems lists one resource family and projects each item to an smItem.
func smItems[T any](page sagemakerdriver.ListPage[T], err error, project func(*T) smItem) ([]smItem, error) {
	if err != nil {
		return nil, err
	}

	out := make([]smItem, 0, len(page.Items))
	for i := range page.Items {
		out = append(out, project(&page.Items[i]))
	}

	return out, nil
}

// DiscoverResources projects the standing, taggable resources. Transient runs
// (jobs, executions, trials, lineage entities) are deliberately absent.
//
//nolint:funlen // static table
func (a sagemakerDiscovery) DiscoverResources(
	ctx context.Context,
) ([]resourcediscovery.DiscoveredResource, error) {
	all := sagemakerdriver.ListOptions{}
	sources := []func() ([]smItem, error){
		func() ([]smItem, error) {
			p, err := a.m.ListModels(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Model) smItem {
				return smItem{resourcediscovery.TypeModel, r.ModelName, r.ModelARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListEndpoints(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Endpoint) smItem {
				return smItem{resourcediscovery.TypeEndpoint, r.EndpointName, r.EndpointARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListNotebookInstances(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.NotebookInstance) smItem {
				return smItem{resourcediscovery.TypeNotebookInstance, r.Name, r.ARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListEndpointConfigs(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.EndpointConfig) smItem {
				return smItem{resourcediscovery.TypeEndpointConfig, r.ConfigName, r.ConfigARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListModelPackageGroups(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.ModelPackageGroup) smItem {
				return smItem{resourcediscovery.TypeModelPackageGroup, r.GroupName, r.GroupARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListPipelines(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Pipeline) smItem {
				return smItem{resourcediscovery.TypePipeline, r.PipelineName, r.PipelineARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListFeatureGroups(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.FeatureGroup) smItem {
				return smItem{resourcediscovery.TypeFeatureGroup, r.GroupName, r.GroupARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListClusters(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Cluster) smItem {
				return smItem{resourcediscovery.TypeMLCluster, r.ClusterName, r.ClusterARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListProjects(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Project) smItem {
				return smItem{resourcediscovery.TypeMLProject, r.Name, r.ARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListMonitoringSchedules(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.MonitoringSchedule) smItem {
				return smItem{resourcediscovery.TypeMonitoringSched, r.Name, r.ARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListImages(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Image) smItem {
				return smItem{resourcediscovery.TypeMLImage, r.ImageName, r.ImageARN}
			})
		},
		func() ([]smItem, error) {
			p, err := a.m.ListWorkteams(ctx, all)

			return smItems(p, err, func(r *sagemakerdriver.Workteam) smItem {
				return smItem{resourcediscovery.TypeWorkteam, r.Name, r.ARN}
			})
		},
	}

	var out []resourcediscovery.DiscoveredResource

	for _, source := range sources {
		items, err := source()
		if err != nil {
			return nil, err
		}

		for _, it := range items {
			tags, err := a.liveTags(ctx, it.arn)
			if err != nil {
				return nil, err
			}

			out = append(out, resourcediscovery.DiscoveredResource{
				Service: resourcediscovery.ServiceSageMaker, Type: it.typ, ID: it.id, ARN: it.arn, Tags: tags,
			})
		}
	}

	return out, nil
}

// liveTags reads a resource's current tags from the ARN-keyed tag store.
func (a sagemakerDiscovery) liveTags(ctx context.Context, arn string) (map[string]string, error) {
	tags, err := a.m.ListTags(ctx, arn)
	if err != nil {
		return nil, err
	}

	return smTags(tags), nil
}
