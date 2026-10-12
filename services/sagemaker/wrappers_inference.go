package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateModel(ctx context.Context, cfg driver.ModelConfig) (*driver.Model, error) {
	return cast[*driver.Model](s.do(ctx, "CreateModel", cfg, func() (any, error) { return s.drv.CreateModel(ctx, cfg) }))
}

func (s *SageMaker) DescribeModel(ctx context.Context, name string) (*driver.Model, error) {
	return cast[*driver.Model](s.do(ctx, "DescribeModel", name, func() (any, error) { return s.drv.DescribeModel(ctx, name) }))
}

func (s *SageMaker) ListModels(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Model], error) {
	return cast[driver.ListPage[driver.Model]](s.do(ctx, "ListModels", opts, func() (any, error) {
		return s.drv.ListModels(ctx, opts)
	}))
}

func (s *SageMaker) DeleteModel(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteModel", name, func() (any, error) { return nil, s.drv.DeleteModel(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateEndpointConfig(ctx context.Context, cfg driver.EndpointConfigSpec) (*driver.EndpointConfig, error) {
	return cast[*driver.EndpointConfig](s.do(ctx, "CreateEndpointConfig", cfg, func() (any, error) {
		return s.drv.CreateEndpointConfig(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeEndpointConfig(ctx context.Context, name string) (*driver.EndpointConfig, error) {
	return cast[*driver.EndpointConfig](s.do(ctx, "DescribeEndpointConfig", name, func() (any, error) {
		return s.drv.DescribeEndpointConfig(ctx, name)
	}))
}

func (s *SageMaker) ListEndpointConfigs(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.EndpointConfig], error) {
	return cast[driver.ListPage[driver.EndpointConfig]](s.do(ctx, "ListEndpointConfigs", opts, func() (any, error) {
		return s.drv.ListEndpointConfigs(ctx, opts)
	}))
}

func (s *SageMaker) DeleteEndpointConfig(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteEndpointConfig", name, func() (any, error) { return nil, s.drv.DeleteEndpointConfig(ctx, name) })

	return err
}

func (s *SageMaker) CreateEndpoint(ctx context.Context, cfg driver.EndpointSpec) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](s.do(ctx, "CreateEndpoint", cfg, func() (any, error) { return s.drv.CreateEndpoint(ctx, cfg) }))
}

func (s *SageMaker) DescribeEndpoint(ctx context.Context, name string) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](s.do(ctx, "DescribeEndpoint", name, func() (any, error) { return s.drv.DescribeEndpoint(ctx, name) }))
}

func (s *SageMaker) ListEndpoints(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.Endpoint], error) {
	return cast[driver.ListPage[driver.Endpoint]](s.do(ctx, "ListEndpoints", opts, func() (any, error) {
		return s.drv.ListEndpoints(ctx, opts)
	}))
}

func (s *SageMaker) UpdateEndpoint(ctx context.Context, spec driver.UpdateEndpointSpec) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](s.do(ctx, "UpdateEndpoint", spec, func() (any, error) {
		return s.drv.UpdateEndpoint(ctx, spec)
	}))
}

func (s *SageMaker) UpdateEndpointWeightsAndCapacities(
	ctx context.Context, name string, weights []driver.VariantWeight,
) (*driver.Endpoint, error) {
	return cast[*driver.Endpoint](s.do(ctx, "UpdateEndpointWeightsAndCapacities", name, func() (any, error) {
		return s.drv.UpdateEndpointWeightsAndCapacities(ctx, name, weights)
	}))
}

func (s *SageMaker) DeleteEndpoint(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteEndpoint", name, func() (any, error) { return nil, s.drv.DeleteEndpoint(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateInferenceComponent(ctx context.Context, cfg driver.InferenceComponentSpec) (*driver.InferenceComponent, error) {
	return cast[*driver.InferenceComponent](s.do(ctx, "CreateInferenceComponent", cfg, func() (any, error) {
		return s.drv.CreateInferenceComponent(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeInferenceComponent(ctx context.Context, name string) (*driver.InferenceComponent, error) {
	return cast[*driver.InferenceComponent](s.do(ctx, "DescribeInferenceComponent", name, func() (any, error) {
		return s.drv.DescribeInferenceComponent(ctx, name)
	}))
}

func (s *SageMaker) ListInferenceComponents(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.InferenceComponent], error) {
	return cast[driver.ListPage[driver.InferenceComponent]](s.do(ctx, "ListInferenceComponents", opts, func() (any, error) {
		return s.drv.ListInferenceComponents(ctx, opts)
	}))
}

func (s *SageMaker) DeleteInferenceComponent(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteInferenceComponent", name, func() (any, error) { return nil, s.drv.DeleteInferenceComponent(ctx, name) })

	return err
}

func (s *SageMaker) CreateModelPackageGroup(ctx context.Context, cfg driver.ModelPackageGroupSpec) (*driver.ModelPackageGroup, error) {
	return cast[*driver.ModelPackageGroup](s.do(ctx, "CreateModelPackageGroup", cfg, func() (any, error) {
		return s.drv.CreateModelPackageGroup(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeModelPackageGroup(ctx context.Context, name string) (*driver.ModelPackageGroup, error) {
	return cast[*driver.ModelPackageGroup](s.do(ctx, "DescribeModelPackageGroup", name, func() (any, error) {
		return s.drv.DescribeModelPackageGroup(ctx, name)
	}))
}

func (s *SageMaker) ListModelPackageGroups(ctx context.Context, opts driver.ListOptions) (driver.ListPage[driver.ModelPackageGroup], error) {
	return cast[driver.ListPage[driver.ModelPackageGroup]](s.do(ctx, "ListModelPackageGroups", opts, func() (any, error) {
		return s.drv.ListModelPackageGroups(ctx, opts)
	}))
}

func (s *SageMaker) DeleteModelPackageGroup(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteModelPackageGroup", name, func() (any, error) { return nil, s.drv.DeleteModelPackageGroup(ctx, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateModelPackage(ctx context.Context, cfg driver.ModelPackageSpec) (*driver.ModelPackage, error) {
	return cast[*driver.ModelPackage](s.do(ctx, "CreateModelPackage", cfg, func() (any, error) { return s.drv.CreateModelPackage(ctx, cfg) }))
}

func (s *SageMaker) DescribeModelPackage(ctx context.Context, arn string) (*driver.ModelPackage, error) {
	return cast[*driver.ModelPackage](s.do(ctx, "DescribeModelPackage", arn, func() (any, error) {
		return s.drv.DescribeModelPackage(ctx, arn)
	}))
}

func (s *SageMaker) ListModelPackages(ctx context.Context, groupName string, opts driver.ListOptions) (driver.ListPage[driver.ModelPackage], error) {
	return cast[driver.ListPage[driver.ModelPackage]](s.do(ctx, "ListModelPackages", groupName, func() (any, error) {
		return s.drv.ListModelPackages(ctx, groupName, opts)
	}))
}

func (s *SageMaker) UpdateModelPackage(ctx context.Context, arn, approvalStatus string) (*driver.ModelPackage, error) {
	return cast[*driver.ModelPackage](s.do(ctx, "UpdateModelPackage", arn, func() (any, error) {
		return s.drv.UpdateModelPackage(ctx, arn, approvalStatus)
	}))
}

func (s *SageMaker) DeleteModelPackage(ctx context.Context, arn string) error {
	_, err := s.do(ctx, "DeleteModelPackage", arn, func() (any, error) { return nil, s.drv.DeleteModelPackage(ctx, arn) })

	return err
}

func (s *SageMaker) BatchDescribeModelPackage(ctx context.Context, arns []string) (*driver.BatchDescribeResult, error) {
	return cast[*driver.BatchDescribeResult](s.do(ctx, "BatchDescribeModelPackage", arns, func() (any, error) {
		return s.drv.BatchDescribeModelPackage(ctx, arns)
	}))
}

func (s *SageMaker) PutModelPackageGroupPolicy(ctx context.Context, groupName, policy string) (string, error) {
	return cast[string](s.do(ctx, "PutModelPackageGroupPolicy", groupName, func() (any, error) {
		return s.drv.PutModelPackageGroupPolicy(ctx, groupName, policy)
	}))
}

func (s *SageMaker) GetModelPackageGroupPolicy(ctx context.Context, groupName string) (string, error) {
	return cast[string](s.do(ctx, "GetModelPackageGroupPolicy", groupName, func() (any, error) {
		return s.drv.GetModelPackageGroupPolicy(ctx, groupName)
	}))
}

func (s *SageMaker) DeleteModelPackageGroupPolicy(ctx context.Context, groupName string) error {
	_, err := s.do(ctx, "DeleteModelPackageGroupPolicy", groupName, func() (any, error) {
		return nil, s.drv.DeleteModelPackageGroupPolicy(ctx, groupName)
	})

	return err
}
