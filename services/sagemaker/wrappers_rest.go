package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Cluster ---

func (s *SageMaker) CreateCluster(ctx context.Context, cfg driver.ClusterSpec) (*driver.Cluster, error) {
	return cast[*driver.Cluster](s.do(ctx, "CreateCluster", cfg, func() (any, error) { return s.drv.CreateCluster(ctx, cfg) }))
}

func (s *SageMaker) DescribeCluster(ctx context.Context, name string) (*driver.Cluster, error) {
	return cast[*driver.Cluster](s.do(ctx, "DescribeCluster", name, func() (any, error) { return s.drv.DescribeCluster(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListClusters(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Cluster], error) {
	return cast[driver.ListPage[driver.Cluster]](s.do(ctx, "ListClusters", opts, func() (any, error) {
		return s.drv.ListClusters(ctx, opts)
	}))
}

func (s *SageMaker) UpdateCluster(ctx context.Context, update driver.ClusterUpdate) (*driver.Cluster, error) {
	return cast[*driver.Cluster](s.do(ctx, "UpdateCluster", update, func() (any, error) { return s.drv.UpdateCluster(ctx, update) }))
}

func (s *SageMaker) DeleteCluster(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteCluster", name, func() (any, error) { return nil, s.drv.DeleteCluster(ctx, name) })

	return err
}

func (s *SageMaker) ListClusterNodes(ctx context.Context, clusterName string) ([]driver.ClusterNode, error) {
	return cast[[]driver.ClusterNode](s.do(ctx, "ListClusterNodes", clusterName, func() (any, error) {
		return s.drv.ListClusterNodes(ctx, clusterName)
	}))
}

func (s *SageMaker) DescribeClusterNode(ctx context.Context, clusterName, nodeID string) (*driver.ClusterNode, error) {
	return cast[*driver.ClusterNode](s.do(ctx, "DescribeClusterNode", nodeID, func() (any, error) {
		return s.drv.DescribeClusterNode(ctx, clusterName, nodeID)
	}))
}

// --- Feature Store ---

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateFeatureGroup(ctx context.Context, cfg driver.FeatureGroupSpec) (*driver.FeatureGroup, error) {
	return cast[*driver.FeatureGroup](s.do(ctx, "CreateFeatureGroup", cfg, func() (any, error) { return s.drv.CreateFeatureGroup(ctx, cfg) }))
}

func (s *SageMaker) DescribeFeatureGroup(ctx context.Context, name string) (*driver.FeatureGroup, error) {
	return cast[*driver.FeatureGroup](s.do(ctx, "DescribeFeatureGroup", name, func() (any, error) {
		return s.drv.DescribeFeatureGroup(ctx, name)
	}))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListFeatureGroups(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.FeatureGroup], error) {
	return cast[driver.ListPage[driver.FeatureGroup]](s.do(ctx, "ListFeatureGroups", opts, func() (any, error) {
		return s.drv.ListFeatureGroups(ctx, opts)
	}))
}

func (s *SageMaker) DeleteFeatureGroup(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteFeatureGroup", name, func() (any, error) { return nil, s.drv.DeleteFeatureGroup(ctx, name) })

	return err
}

func (s *SageMaker) PutRecord(ctx context.Context, groupName string, record []driver.FeatureValue) error {
	_, err := s.do(ctx, "PutRecord", groupName, func() (any, error) { return nil, s.drv.PutRecord(ctx, groupName, record) })

	return err
}

func (s *SageMaker) GetRecord(ctx context.Context, groupName, recordID string) ([]driver.FeatureValue, error) {
	return cast[[]driver.FeatureValue](s.do(ctx, "GetRecord", recordID, func() (any, error) {
		return s.drv.GetRecord(ctx, groupName, recordID)
	}))
}

func (s *SageMaker) DeleteRecord(ctx context.Context, groupName, recordID string) error {
	_, err := s.do(ctx, "DeleteRecord", recordID, func() (any, error) { return nil, s.drv.DeleteRecord(ctx, groupName, recordID) })

	return err
}

// --- Pipelines / experiments / trials ---

func (s *SageMaker) CreatePipeline(ctx context.Context, cfg driver.PipelineSpec) (*driver.Pipeline, error) {
	return cast[*driver.Pipeline](s.do(ctx, "CreatePipeline", cfg, func() (any, error) { return s.drv.CreatePipeline(ctx, cfg) }))
}

func (s *SageMaker) DescribePipeline(ctx context.Context, name string) (*driver.Pipeline, error) {
	return cast[*driver.Pipeline](s.do(ctx, "DescribePipeline", name, func() (any, error) { return s.drv.DescribePipeline(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListPipelines(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Pipeline], error) {
	return cast[driver.ListPage[driver.Pipeline]](s.do(ctx, "ListPipelines", opts, func() (any, error) {
		return s.drv.ListPipelines(ctx, opts)
	}))
}

func (s *SageMaker) UpdatePipeline(ctx context.Context, name, definition string) (*driver.Pipeline, error) {
	return cast[*driver.Pipeline](s.do(ctx, "UpdatePipeline", name, func() (any, error) {
		return s.drv.UpdatePipeline(ctx, name, definition)
	}))
}

func (s *SageMaker) DeletePipeline(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeletePipeline", name, func() (any, error) { return nil, s.drv.DeletePipeline(ctx, name) })

	return err
}

func (s *SageMaker) StartPipelineExecution(ctx context.Context, pipelineName string) (*driver.PipelineExecution, error) {
	return cast[*driver.PipelineExecution](s.do(ctx, "StartPipelineExecution", pipelineName, func() (any, error) {
		return s.drv.StartPipelineExecution(ctx, pipelineName)
	}))
}

func (s *SageMaker) DescribePipelineExecution(ctx context.Context, executionARN string) (*driver.PipelineExecution, error) {
	return cast[*driver.PipelineExecution](s.do(ctx, "DescribePipelineExecution", executionARN, func() (any, error) {
		return s.drv.DescribePipelineExecution(ctx, executionARN)
	}))
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListPipelineExecutions(ctx context.Context, pipelineName string, opts driver.ListOptions) (driver.ListPage[driver.PipelineExecution], error) {
	return cast[driver.ListPage[driver.PipelineExecution]](s.do(ctx, "ListPipelineExecutions", pipelineName, func() (any, error) {
		return s.drv.ListPipelineExecutions(ctx, pipelineName, opts)
	}))
}

func (s *SageMaker) StopPipelineExecution(ctx context.Context, executionARN string) error {
	_, err := s.do(ctx, "StopPipelineExecution", executionARN, func() (any, error) {
		return nil, s.drv.StopPipelineExecution(ctx, executionARN)
	})

	return err
}

func (s *SageMaker) CreateExperiment(ctx context.Context, cfg driver.ExperimentSpec) (*driver.Experiment, error) {
	return cast[*driver.Experiment](s.do(ctx, "CreateExperiment", cfg, func() (any, error) { return s.drv.CreateExperiment(ctx, cfg) }))
}

func (s *SageMaker) DescribeExperiment(ctx context.Context, name string) (*driver.Experiment, error) {
	return cast[*driver.Experiment](s.do(ctx, "DescribeExperiment", name, func() (any, error) { return s.drv.DescribeExperiment(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListExperiments(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Experiment], error) {
	return cast[driver.ListPage[driver.Experiment]](s.do(ctx, "ListExperiments", opts, func() (any, error) {
		return s.drv.ListExperiments(ctx, opts)
	}))
}

func (s *SageMaker) DeleteExperiment(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteExperiment", name, func() (any, error) { return nil, s.drv.DeleteExperiment(ctx, name) })

	return err
}

func (s *SageMaker) CreateTrial(ctx context.Context, cfg driver.TrialSpec) (*driver.Trial, error) {
	return cast[*driver.Trial](s.do(ctx, "CreateTrial", cfg, func() (any, error) { return s.drv.CreateTrial(ctx, cfg) }))
}

func (s *SageMaker) DescribeTrial(ctx context.Context, name string) (*driver.Trial, error) {
	return cast[*driver.Trial](s.do(ctx, "DescribeTrial", name, func() (any, error) { return s.drv.DescribeTrial(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListTrials(ctx context.Context, experimentName string, opts driver.ListOptions) (driver.ListPage[driver.Trial], error) {
	return cast[driver.ListPage[driver.Trial]](s.do(ctx, "ListTrials", experimentName, func() (any, error) {
		return s.drv.ListTrials(ctx, experimentName, opts)
	}))
}

func (s *SageMaker) DeleteTrial(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteTrial", name, func() (any, error) { return nil, s.drv.DeleteTrial(ctx, name) })

	return err
}

// --- Tags ---

func (s *SageMaker) AddTags(ctx context.Context, resourceARN string, tags []driver.Tag) ([]driver.Tag, error) {
	return cast[[]driver.Tag](s.do(ctx, "AddTags", resourceARN, func() (any, error) { return s.drv.AddTags(ctx, resourceARN, tags) }))
}

func (s *SageMaker) ListTags(ctx context.Context, resourceARN string) ([]driver.Tag, error) {
	return cast[[]driver.Tag](s.do(ctx, "ListTags", resourceARN, func() (any, error) { return s.drv.ListTags(ctx, resourceARN) }))
}

func (s *SageMaker) DeleteTags(ctx context.Context, resourceARN string, keys []string) error {
	_, err := s.do(ctx, "DeleteTags", resourceARN, func() (any, error) { return nil, s.drv.DeleteTags(ctx, resourceARN, keys) })

	return err
}

// --- Runtime ---

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) InvokeEndpoint(ctx context.Context, in driver.InvokeEndpointInput) (*driver.InvokeEndpointOutput, error) {
	return cast[*driver.InvokeEndpointOutput](s.do(ctx, "InvokeEndpoint", in.EndpointName, func() (any, error) {
		return s.drv.InvokeEndpoint(ctx, in)
	}))
}

func (s *SageMaker) InvokeEndpointAsync(
	ctx context.Context, in driver.InvokeEndpointAsyncInput,
) (*driver.InvokeEndpointAsyncOutput, error) {
	return cast[*driver.InvokeEndpointAsyncOutput](s.do(ctx, "InvokeEndpointAsync", in.EndpointName, func() (any, error) {
		return s.drv.InvokeEndpointAsync(ctx, in)
	}))
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) CreateProject(ctx context.Context, spec driver.ProjectSpec) (*driver.Project, error) {
	return cast[*driver.Project](s.do(ctx, "CreateProject", spec, func() (any, error) { return s.drv.CreateProject(ctx, spec) }))
}

func (s *SageMaker) DescribeProject(ctx context.Context, name string) (*driver.Project, error) {
	return cast[*driver.Project](s.do(ctx, "DescribeProject", name, func() (any, error) { return s.drv.DescribeProject(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListProjects(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Project], error) {
	return cast[driver.ListPage[driver.Project]](s.do(ctx, "ListProjects", opts, func() (any, error) { return s.drv.ListProjects(ctx, opts) }))
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) UpdateProject(ctx context.Context, update driver.ProjectUpdate) (*driver.Project, error) {
	return cast[*driver.Project](s.do(ctx, "UpdateProject", update, func() (any, error) { return s.drv.UpdateProject(ctx, update) }))
}

func (s *SageMaker) DeleteProject(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteProject", name, func() (any, error) { return nil, s.drv.DeleteProject(ctx, name) })

	return err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) CreateWorkforce(ctx context.Context, spec driver.WorkforceSpec) (*driver.Workforce, error) {
	return cast[*driver.Workforce](s.do(ctx, "CreateWorkforce", spec, func() (any, error) { return s.drv.CreateWorkforce(ctx, spec) }))
}

func (s *SageMaker) DescribeWorkforce(ctx context.Context, name string) (*driver.Workforce, error) {
	return cast[*driver.Workforce](s.do(ctx, "DescribeWorkforce", name, func() (any, error) { return s.drv.DescribeWorkforce(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListWorkforces(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Workforce], error) {
	return cast[driver.ListPage[driver.Workforce]](s.do(ctx, "ListWorkforces", opts, func() (any, error) { return s.drv.ListWorkforces(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateWorkforce(ctx context.Context, update driver.WorkforceUpdate) (*driver.Workforce, error) {
	return cast[*driver.Workforce](s.do(ctx, "UpdateWorkforce", update, func() (any, error) { return s.drv.UpdateWorkforce(ctx, update) }))
}

func (s *SageMaker) DeleteWorkforce(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteWorkforce", name, func() (any, error) { return nil, s.drv.DeleteWorkforce(ctx, name) })

	return err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) CreateWorkteam(ctx context.Context, spec driver.WorkteamSpec) (*driver.Workteam, error) {
	return cast[*driver.Workteam](s.do(ctx, "CreateWorkteam", spec, func() (any, error) { return s.drv.CreateWorkteam(ctx, spec) }))
}

func (s *SageMaker) DescribeWorkteam(ctx context.Context, name string) (*driver.Workteam, error) {
	return cast[*driver.Workteam](s.do(ctx, "DescribeWorkteam", name, func() (any, error) { return s.drv.DescribeWorkteam(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListWorkteams(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Workteam], error) {
	return cast[driver.ListPage[driver.Workteam]](s.do(ctx, "ListWorkteams", opts, func() (any, error) { return s.drv.ListWorkteams(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateWorkteam(ctx context.Context, update driver.WorkteamUpdate) (*driver.Workteam, error) {
	return cast[*driver.Workteam](s.do(ctx, "UpdateWorkteam", update, func() (any, error) { return s.drv.UpdateWorkteam(ctx, update) }))
}

func (s *SageMaker) DeleteWorkteam(ctx context.Context, name string) (bool, error) {
	return cast[bool](s.do(ctx, "DeleteWorkteam", name, func() (any, error) { return s.drv.DeleteWorkteam(ctx, name) }))
}

func (s *SageMaker) DescribeSubscribedWorkteam(ctx context.Context, arn string) (*driver.SubscribedWorkteam, error) {
	return cast[*driver.SubscribedWorkteam](s.do(ctx, "DescribeSubscribedWorkteam", arn, func() (any, error) {
		return s.drv.DescribeSubscribedWorkteam(ctx, arn)
	}))
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListSubscribedWorkteams(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.SubscribedWorkteam], error) {
	return cast[driver.ListPage[driver.SubscribedWorkteam]](s.do(ctx, "ListSubscribedWorkteams", opts, func() (any, error) {
		return s.drv.ListSubscribedWorkteams(ctx, opts)
	}))
}

func (s *SageMaker) ListLabelingJobsForWorkteam(
	ctx context.Context, workteamARN string, opts driver.ListOptions, //nolint:gocritic // SDK-shaped; see nongoals/standards
) (driver.ListPage[driver.LabelingJobForWorkteam], error) {
	return cast[driver.ListPage[driver.LabelingJobForWorkteam]](s.do(ctx, "ListLabelingJobsForWorkteam", workteamARN, func() (any, error) {
		return s.drv.ListLabelingJobsForWorkteam(ctx, workteamARN, opts)
	}))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateImage(ctx context.Context, spec driver.ImageSpec) (*driver.Image, error) {
	return cast[*driver.Image](s.do(ctx, "CreateImage", spec, func() (any, error) { return s.drv.CreateImage(ctx, spec) }))
}

func (s *SageMaker) DescribeImage(ctx context.Context, name string) (*driver.Image, error) {
	return cast[*driver.Image](s.do(ctx, "DescribeImage", name, func() (any, error) { return s.drv.DescribeImage(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListImages(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Image], error) {
	return cast[driver.ListPage[driver.Image]](s.do(ctx, "ListImages", opts, func() (any, error) { return s.drv.ListImages(ctx, opts) }))
}

func (s *SageMaker) UpdateImage(ctx context.Context, update driver.ImageUpdate) (*driver.Image, error) {
	return cast[*driver.Image](s.do(ctx, "UpdateImage", update, func() (any, error) { return s.drv.UpdateImage(ctx, update) }))
}

func (s *SageMaker) DeleteImage(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteImage", name, func() (any, error) { return nil, s.drv.DeleteImage(ctx, name) })

	return err
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateImageVersion(ctx context.Context, spec driver.ImageVersionSpec) (*driver.ImageVersion, error) {
	return cast[*driver.ImageVersion](s.do(ctx, "CreateImageVersion", spec, func() (any, error) { return s.drv.CreateImageVersion(ctx, spec) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) DescribeImageVersion(ctx context.Context, ref driver.ImageVersionRef) (*driver.ImageVersion, error) {
	return cast[*driver.ImageVersion](s.do(ctx, "DescribeImageVersion", ref, func() (any, error) { return s.drv.DescribeImageVersion(ctx, ref) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListImageVersions(ctx context.Context, imageName string, opts driver.ListOptions) (driver.ListPage[driver.ImageVersion], error) {
	return cast[driver.ListPage[driver.ImageVersion]](s.do(ctx, "ListImageVersions", imageName, func() (any, error) {
		return s.drv.ListImageVersions(ctx, imageName, opts)
	}))
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) UpdateImageVersion(ctx context.Context, update driver.ImageVersionUpdate) (*driver.ImageVersion, error) {
	return cast[*driver.ImageVersion](s.do(ctx, "UpdateImageVersion", update, func() (any, error) { return s.drv.UpdateImageVersion(ctx, update) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) DeleteImageVersion(ctx context.Context, ref driver.ImageVersionRef) error {
	_, err := s.do(ctx, "DeleteImageVersion", ref, func() (any, error) { return nil, s.drv.DeleteImageVersion(ctx, ref) })

	return err
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListAliases(ctx context.Context, ref driver.ImageVersionRef, opts driver.ListOptions) (driver.ListPage[string], error) {
	return cast[driver.ListPage[string]](s.do(ctx, "ListAliases", ref, func() (any, error) { return s.drv.ListAliases(ctx, ref, opts) }))
}

func (s *SageMaker) CreateAppImageConfig(ctx context.Context, spec driver.AppImageConfigSpec) (*driver.AppImageConfig, error) {
	return cast[*driver.AppImageConfig](s.do(ctx, "CreateAppImageConfig", spec, func() (any, error) { return s.drv.CreateAppImageConfig(ctx, spec) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

func (s *SageMaker) DescribeAppImageConfig(ctx context.Context, name string) (*driver.AppImageConfig, error) {
	return cast[*driver.AppImageConfig](s.do(ctx, "DescribeAppImageConfig", name, func() (any, error) { return s.drv.DescribeAppImageConfig(ctx, name) })) //nolint:lll // SDK-shaped literal
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListAppImageConfigs(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.AppImageConfig], error) {
	return cast[driver.ListPage[driver.AppImageConfig]](s.do(ctx, "ListAppImageConfigs", opts, func() (any, error) { return s.drv.ListAppImageConfigs(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateAppImageConfig(ctx context.Context, spec driver.AppImageConfigSpec) (*driver.AppImageConfig, error) {
	return cast[*driver.AppImageConfig](s.do(ctx, "UpdateAppImageConfig", spec, func() (any, error) { return s.drv.UpdateAppImageConfig(ctx, spec) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

func (s *SageMaker) DeleteAppImageConfig(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteAppImageConfig", name, func() (any, error) { return nil, s.drv.DeleteAppImageConfig(ctx, name) })

	return err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) CreateTrialComponent(ctx context.Context, spec driver.TrialComponentSpec) (*driver.TrialComponent, error) {
	return cast[*driver.TrialComponent](s.do(ctx, "CreateTrialComponent", spec, func() (any, error) { return s.drv.CreateTrialComponent(ctx, spec) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

func (s *SageMaker) DescribeTrialComponent(ctx context.Context, name string) (*driver.TrialComponent, error) {
	return cast[*driver.TrialComponent](s.do(ctx, "DescribeTrialComponent", name, func() (any, error) { return s.drv.DescribeTrialComponent(ctx, name) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListTrialComponents(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.TrialComponent], error) {
	return cast[driver.ListPage[driver.TrialComponent]](s.do(ctx, "ListTrialComponents", opts, func() (any, error) { return s.drv.ListTrialComponents(ctx, opts) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) UpdateTrialComponent(ctx context.Context, update driver.TrialComponentUpdate) (*driver.TrialComponent, error) {
	return cast[*driver.TrialComponent](s.do(ctx, "UpdateTrialComponent", update, func() (any, error) { return s.drv.UpdateTrialComponent(ctx, update) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

func (s *SageMaker) DeleteTrialComponent(ctx context.Context, name string) (*driver.TrialComponent, error) {
	return cast[*driver.TrialComponent](s.do(ctx, "DeleteTrialComponent", name, func() (any, error) { return s.drv.DeleteTrialComponent(ctx, name) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

type linkResult struct{ component, trial string }

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) AssociateTrialComponent(ctx context.Context, componentName, trialName string) (string, string, error) {
	r, err := cast[linkResult](s.do(ctx, "AssociateTrialComponent", componentName, func() (any, error) {
		c, t, e := s.drv.AssociateTrialComponent(ctx, componentName, trialName)

		return linkResult{c, t}, e
	}))

	return r.component, r.trial, err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) DisassociateTrialComponent(ctx context.Context, componentName, trialName string) (string, string, error) {
	r, err := cast[linkResult](s.do(ctx, "DisassociateTrialComponent", componentName, func() (any, error) {
		c, t, e := s.drv.DisassociateTrialComponent(ctx, componentName, trialName)

		return linkResult{c, t}, e
	}))

	return r.component, r.trial, err
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateArtifact(ctx context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "CreateArtifact", spec, func() (any, error) { return s.drv.CreateArtifact(ctx, spec) }))
}

func (s *SageMaker) DescribeArtifact(ctx context.Context, arn string) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "DescribeArtifact", arn, func() (any, error) { return s.drv.DescribeArtifact(ctx, arn) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListArtifacts(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return cast[driver.ListPage[driver.LineageEntity]](s.do(ctx, "ListArtifacts", opts, func() (any, error) { return s.drv.ListArtifacts(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateArtifact(ctx context.Context, update driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "UpdateArtifact", update, func() (any, error) { return s.drv.UpdateArtifact(ctx, update) }))
}

func (s *SageMaker) DeleteArtifact(ctx context.Context, arn, sourceURI string) (string, error) {
	return cast[string](s.do(ctx, "DeleteArtifact", arn, func() (any, error) { return s.drv.DeleteArtifact(ctx, arn, sourceURI) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateAction(ctx context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "CreateAction", spec, func() (any, error) { return s.drv.CreateAction(ctx, spec) }))
}

func (s *SageMaker) DescribeAction(ctx context.Context, name string) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "DescribeAction", name, func() (any, error) { return s.drv.DescribeAction(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListActions(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return cast[driver.ListPage[driver.LineageEntity]](s.do(ctx, "ListActions", opts, func() (any, error) { return s.drv.ListActions(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateAction(ctx context.Context, update driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "UpdateAction", update, func() (any, error) { return s.drv.UpdateAction(ctx, update) }))
}

func (s *SageMaker) DeleteAction(ctx context.Context, name string) (string, error) {
	return cast[string](s.do(ctx, "DeleteAction", name, func() (any, error) { return s.drv.DeleteAction(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateContext(ctx context.Context, spec driver.LineageEntitySpec) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "CreateContext", spec, func() (any, error) { return s.drv.CreateContext(ctx, spec) }))
}

func (s *SageMaker) DescribeContext(ctx context.Context, name string) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "DescribeContext", name, func() (any, error) { return s.drv.DescribeContext(ctx, name) }))
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListContexts(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageEntity], error) {
	return cast[driver.ListPage[driver.LineageEntity]](s.do(ctx, "ListContexts", opts, func() (any, error) { return s.drv.ListContexts(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) UpdateContext(ctx context.Context, update driver.LineageEntityUpdate) (*driver.LineageEntity, error) {
	return cast[*driver.LineageEntity](s.do(ctx, "UpdateContext", update, func() (any, error) { return s.drv.UpdateContext(ctx, update) }))
}

func (s *SageMaker) DeleteContext(ctx context.Context, name string) (string, error) {
	return cast[string](s.do(ctx, "DeleteContext", name, func() (any, error) { return s.drv.DeleteContext(ctx, name) }))
}

func (s *SageMaker) AddAssociation(ctx context.Context, sourceARN, destinationARN, associationType string) error {
	_, err := s.do(ctx, "AddAssociation", sourceARN, func() (any, error) {
		return nil, s.drv.AddAssociation(ctx, sourceARN, destinationARN, associationType)
	})

	return err
}

func (s *SageMaker) DeleteAssociation(ctx context.Context, sourceARN, destinationARN string) error {
	_, err := s.do(ctx, "DeleteAssociation", sourceARN, func() (any, error) {
		return nil, s.drv.DeleteAssociation(ctx, sourceARN, destinationARN)
	})

	return err
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListAssociations(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.AssociationSummary], error) {
	return cast[driver.ListPage[driver.AssociationSummary]](s.do(ctx, "ListAssociations", opts, func() (any, error) { return s.drv.ListAssociations(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) QueryLineage(ctx context.Context, query driver.LineageQuery) (*driver.LineageResult, error) {
	return cast[*driver.LineageResult](s.do(ctx, "QueryLineage", query, func() (any, error) { return s.drv.QueryLineage(ctx, query) }))
}

func (s *SageMaker) DescribeLineageGroup(ctx context.Context, nameOrARN string) (*driver.LineageGroup, error) {
	return cast[*driver.LineageGroup](s.do(ctx, "DescribeLineageGroup", nameOrARN, func() (any, error) { return s.drv.DescribeLineageGroup(ctx, nameOrARN) })) //nolint:lll // SDK-shaped literal
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListLineageGroups(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.LineageGroup], error) {
	return cast[driver.ListPage[driver.LineageGroup]](s.do(ctx, "ListLineageGroups", opts, func() (any, error) { return s.drv.ListLineageGroups(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

type policyResult struct{ arn, policy string }

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) GetLineageGroupPolicy(ctx context.Context, nameOrARN string) (string, string, error) {
	r, err := cast[policyResult](s.do(ctx, "GetLineageGroupPolicy", nameOrARN, func() (any, error) {
		a, p, e := s.drv.GetLineageGroupPolicy(ctx, nameOrARN)

		return policyResult{a, p}, e
	}))

	return r.arn, r.policy, err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) CreateMonitoringSchedule(ctx context.Context, spec driver.MonitoringScheduleSpec) (*driver.MonitoringSchedule, error) {
	return cast[*driver.MonitoringSchedule](s.do(ctx, "CreateMonitoringSchedule", spec, func() (any, error) { return s.drv.CreateMonitoringSchedule(ctx, spec) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) DescribeMonitoringSchedule(ctx context.Context, name string) (*driver.MonitoringSchedule, error) {
	return cast[*driver.MonitoringSchedule](s.do(ctx, "DescribeMonitoringSchedule", name, func() (any, error) { return s.drv.DescribeMonitoringSchedule(ctx, name) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListMonitoringSchedules(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringSchedule], error) {
	return cast[driver.ListPage[driver.MonitoringSchedule]](s.do(ctx, "ListMonitoringSchedules", opts, func() (any, error) { return s.drv.ListMonitoringSchedules(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (s *SageMaker) UpdateMonitoringSchedule(ctx context.Context, spec driver.MonitoringScheduleSpec) (*driver.MonitoringSchedule, error) {
	return cast[*driver.MonitoringSchedule](s.do(ctx, "UpdateMonitoringSchedule", spec, func() (any, error) { return s.drv.UpdateMonitoringSchedule(ctx, spec) })) //nolint:lll // SDK-shaped literal
}

func (s *SageMaker) DeleteMonitoringSchedule(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteMonitoringSchedule", name, func() (any, error) { return nil, s.drv.DeleteMonitoringSchedule(ctx, name) })

	return err
}

func (s *SageMaker) StartMonitoringSchedule(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StartMonitoringSchedule", name, func() (any, error) { return nil, s.drv.StartMonitoringSchedule(ctx, name) })

	return err
}

func (s *SageMaker) StopMonitoringSchedule(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopMonitoringSchedule", name, func() (any, error) { return nil, s.drv.StopMonitoringSchedule(ctx, name) })

	return err
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListMonitoringExecutions(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringExecution], error) {
	return cast[driver.ListPage[driver.MonitoringExecution]](s.do(ctx, "ListMonitoringExecutions", opts, func() (any, error) { return s.drv.ListMonitoringExecutions(ctx, opts) })) //nolint:lll // SDK-shaped literal
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListMonitoringAlerts(ctx context.Context, scheduleName string, opts driver.ListOptions) (driver.ListPage[driver.MonitoringAlert], error) {
	return cast[driver.ListPage[driver.MonitoringAlert]](s.do(ctx, "ListMonitoringAlerts", scheduleName, func() (any, error) {
		return s.drv.ListMonitoringAlerts(ctx, scheduleName, opts)
	}))
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListMonitoringAlertHistory(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.MonitoringAlertEvent], error) {
	return cast[driver.ListPage[driver.MonitoringAlertEvent]](s.do(ctx, "ListMonitoringAlertHistory", opts, func() (any, error) { return s.drv.ListMonitoringAlertHistory(ctx, opts) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

//nolint:lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) UpdateMonitoringAlert(ctx context.Context, scheduleName, alertName string, datapoints, period int) (*driver.MonitoringSchedule, error) {
	return cast[*driver.MonitoringSchedule](s.do(ctx, "UpdateMonitoringAlert", scheduleName, func() (any, error) {
		return s.drv.UpdateMonitoringAlert(ctx, scheduleName, alertName, datapoints, period)
	}))
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) CreateMonitoringJobDefinition(ctx context.Context, spec driver.MonitoringJobDefinitionSpec) (*driver.MonitoringJobDefinition, error) {
	return cast[*driver.MonitoringJobDefinition](s.do(ctx, "CreateMonitoringJobDefinition", spec, func() (any, error) { return s.drv.CreateMonitoringJobDefinition(ctx, spec) })) //nolint:lll // SDK-shaped; see nongoals/standards
}

func (s *SageMaker) DescribeMonitoringJobDefinition(ctx context.Context, kind, name string) (*driver.MonitoringJobDefinition, error) {
	return cast[*driver.MonitoringJobDefinition](s.do(ctx, "DescribeMonitoringJobDefinition", name, func() (any, error) {
		return s.drv.DescribeMonitoringJobDefinition(ctx, kind, name)
	}))
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (s *SageMaker) ListMonitoringJobDefinitions(ctx context.Context, kind string, opts driver.ListOptions) (driver.ListPage[driver.MonitoringJobDefinition], error) {
	return cast[driver.ListPage[driver.MonitoringJobDefinition]](s.do(ctx, "ListMonitoringJobDefinitions", kind, func() (any, error) {
		return s.drv.ListMonitoringJobDefinitions(ctx, kind, opts)
	}))
}

func (s *SageMaker) DeleteMonitoringJobDefinition(ctx context.Context, kind, name string) error {
	_, err := s.do(ctx, "DeleteMonitoringJobDefinition", name, func() (any, error) { return nil, s.drv.DeleteMonitoringJobDefinition(ctx, kind, name) }) //nolint:lll // SDK-shaped; see nongoals/standards

	return err
}

//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (s *SageMaker) Search(ctx context.Context, req driver.SearchRequest) (*driver.SearchResult, error) {
	return cast[*driver.SearchResult](s.do(ctx, "Search", req, func() (any, error) { return s.drv.Search(ctx, req) }))
}

func (s *SageMaker) GetSearchSuggestions(ctx context.Context, resource, hint string) ([]string, error) {
	return cast[[]string](s.do(ctx, "GetSearchSuggestions", resource, func() (any, error) { return s.drv.GetSearchSuggestions(ctx, resource, hint) })) //nolint:lll // SDK-shaped; see nongoals/standards
}
