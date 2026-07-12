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

func (s *SageMaker) ListClusters(ctx context.Context) ([]driver.Cluster, error) {
	return cast[[]driver.Cluster](s.do(ctx, "ListClusters", nil, func() (any, error) { return s.drv.ListClusters(ctx) }))
}

func (s *SageMaker) UpdateCluster(ctx context.Context, name string, groups []driver.ClusterInstanceGroupSpec) (*driver.Cluster, error) {
	return cast[*driver.Cluster](s.do(ctx, "UpdateCluster", name, func() (any, error) { return s.drv.UpdateCluster(ctx, name, groups) }))
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

func (s *SageMaker) ListFeatureGroups(ctx context.Context) ([]driver.FeatureGroup, error) {
	return cast[[]driver.FeatureGroup](s.do(ctx, "ListFeatureGroups", nil, func() (any, error) { return s.drv.ListFeatureGroups(ctx) }))
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

func (s *SageMaker) ListPipelines(ctx context.Context) ([]driver.Pipeline, error) {
	return cast[[]driver.Pipeline](s.do(ctx, "ListPipelines", nil, func() (any, error) { return s.drv.ListPipelines(ctx) }))
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

func (s *SageMaker) ListPipelineExecutions(ctx context.Context, pipelineName string) ([]driver.PipelineExecution, error) {
	return cast[[]driver.PipelineExecution](s.do(ctx, "ListPipelineExecutions", pipelineName, func() (any, error) {
		return s.drv.ListPipelineExecutions(ctx, pipelineName)
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

func (s *SageMaker) ListExperiments(ctx context.Context) ([]driver.Experiment, error) {
	return cast[[]driver.Experiment](s.do(ctx, "ListExperiments", nil, func() (any, error) { return s.drv.ListExperiments(ctx) }))
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

func (s *SageMaker) ListTrials(ctx context.Context, experimentName string) ([]driver.Trial, error) {
	return cast[[]driver.Trial](s.do(ctx, "ListTrials", experimentName, func() (any, error) { return s.drv.ListTrials(ctx, experimentName) }))
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
