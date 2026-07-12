package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateDomain(ctx context.Context, cfg driver.DomainSpec) (*driver.Domain, error) {
	return cast[*driver.Domain](s.do(ctx, "CreateDomain", cfg, func() (any, error) { return s.drv.CreateDomain(ctx, cfg) }))
}

func (s *SageMaker) DescribeDomain(ctx context.Context, domainID string) (*driver.Domain, error) {
	return cast[*driver.Domain](s.do(ctx, "DescribeDomain", domainID, func() (any, error) { return s.drv.DescribeDomain(ctx, domainID) }))
}

func (s *SageMaker) ListDomains(ctx context.Context) ([]driver.Domain, error) {
	return cast[[]driver.Domain](s.do(ctx, "ListDomains", nil, func() (any, error) { return s.drv.ListDomains(ctx) }))
}

func (s *SageMaker) DeleteDomain(ctx context.Context, domainID string) error {
	_, err := s.do(ctx, "DeleteDomain", domainID, func() (any, error) { return nil, s.drv.DeleteDomain(ctx, domainID) })

	return err
}

func (s *SageMaker) CreateUserProfile(ctx context.Context, cfg driver.UserProfileSpec) (*driver.UserProfile, error) {
	return cast[*driver.UserProfile](s.do(ctx, "CreateUserProfile", cfg, func() (any, error) { return s.drv.CreateUserProfile(ctx, cfg) }))
}

func (s *SageMaker) DescribeUserProfile(ctx context.Context, domainID, name string) (*driver.UserProfile, error) {
	return cast[*driver.UserProfile](s.do(ctx, "DescribeUserProfile", name, func() (any, error) {
		return s.drv.DescribeUserProfile(ctx, domainID, name)
	}))
}

func (s *SageMaker) ListUserProfiles(ctx context.Context, domainID string) ([]driver.UserProfile, error) {
	return cast[[]driver.UserProfile](s.do(ctx, "ListUserProfiles", domainID, func() (any, error) {
		return s.drv.ListUserProfiles(ctx, domainID)
	}))
}

func (s *SageMaker) DeleteUserProfile(ctx context.Context, domainID, name string) error {
	_, err := s.do(ctx, "DeleteUserProfile", name, func() (any, error) { return nil, s.drv.DeleteUserProfile(ctx, domainID, name) })

	return err
}

func (s *SageMaker) CreateSpace(ctx context.Context, cfg driver.SpaceSpec) (*driver.Space, error) {
	return cast[*driver.Space](s.do(ctx, "CreateSpace", cfg, func() (any, error) { return s.drv.CreateSpace(ctx, cfg) }))
}

func (s *SageMaker) DescribeSpace(ctx context.Context, domainID, name string) (*driver.Space, error) {
	return cast[*driver.Space](s.do(ctx, "DescribeSpace", name, func() (any, error) { return s.drv.DescribeSpace(ctx, domainID, name) }))
}

func (s *SageMaker) ListSpaces(ctx context.Context, domainID string) ([]driver.Space, error) {
	return cast[[]driver.Space](s.do(ctx, "ListSpaces", domainID, func() (any, error) { return s.drv.ListSpaces(ctx, domainID) }))
}

func (s *SageMaker) DeleteSpace(ctx context.Context, domainID, name string) error {
	_, err := s.do(ctx, "DeleteSpace", name, func() (any, error) { return nil, s.drv.DeleteSpace(ctx, domainID, name) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateApp(ctx context.Context, in driver.AppSpec) (*driver.App, error) {
	return cast[*driver.App](s.do(ctx, "CreateApp", in, func() (any, error) { return s.drv.CreateApp(ctx, in) }))
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) DescribeApp(ctx context.Context, in driver.AppSpec) (*driver.App, error) {
	return cast[*driver.App](s.do(ctx, "DescribeApp", in, func() (any, error) { return s.drv.DescribeApp(ctx, in) }))
}

func (s *SageMaker) ListApps(ctx context.Context, domainID string) ([]driver.App, error) {
	return cast[[]driver.App](s.do(ctx, "ListApps", domainID, func() (any, error) { return s.drv.ListApps(ctx, domainID) }))
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) DeleteApp(ctx context.Context, in driver.AppSpec) error {
	_, err := s.do(ctx, "DeleteApp", in, func() (any, error) { return nil, s.drv.DeleteApp(ctx, in) })

	return err
}

//nolint:gocritic // cfg/in matches the driver signature; forwarded unchanged.
func (s *SageMaker) CreateNotebookInstance(ctx context.Context, cfg driver.NotebookInstanceSpec) (*driver.NotebookInstance, error) {
	return cast[*driver.NotebookInstance](s.do(ctx, "CreateNotebookInstance", cfg, func() (any, error) {
		return s.drv.CreateNotebookInstance(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeNotebookInstance(ctx context.Context, name string) (*driver.NotebookInstance, error) {
	return cast[*driver.NotebookInstance](s.do(ctx, "DescribeNotebookInstance", name, func() (any, error) {
		return s.drv.DescribeNotebookInstance(ctx, name)
	}))
}

func (s *SageMaker) ListNotebookInstances(ctx context.Context) ([]driver.NotebookInstance, error) {
	return cast[[]driver.NotebookInstance](s.do(ctx, "ListNotebookInstances", nil, func() (any, error) {
		return s.drv.ListNotebookInstances(ctx)
	}))
}

func (s *SageMaker) StartNotebookInstance(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StartNotebookInstance", name, func() (any, error) { return nil, s.drv.StartNotebookInstance(ctx, name) })

	return err
}

func (s *SageMaker) StopNotebookInstance(ctx context.Context, name string) error {
	_, err := s.do(ctx, "StopNotebookInstance", name, func() (any, error) { return nil, s.drv.StopNotebookInstance(ctx, name) })

	return err
}

func (s *SageMaker) DeleteNotebookInstance(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteNotebookInstance", name, func() (any, error) { return nil, s.drv.DeleteNotebookInstance(ctx, name) })

	return err
}

func (s *SageMaker) CreateNotebookInstanceLifecycleConfig(
	ctx context.Context, cfg driver.NotebookLifecycleConfigSpec,
) (*driver.NotebookLifecycleConfig, error) {
	return cast[*driver.NotebookLifecycleConfig](s.do(ctx, "CreateNotebookInstanceLifecycleConfig", cfg, func() (any, error) {
		return s.drv.CreateNotebookInstanceLifecycleConfig(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeNotebookInstanceLifecycleConfig(ctx context.Context, name string) (*driver.NotebookLifecycleConfig, error) {
	return cast[*driver.NotebookLifecycleConfig](s.do(ctx, "DescribeNotebookInstanceLifecycleConfig", name, func() (any, error) {
		return s.drv.DescribeNotebookInstanceLifecycleConfig(ctx, name)
	}))
}

func (s *SageMaker) ListNotebookInstanceLifecycleConfigs(ctx context.Context) ([]driver.NotebookLifecycleConfig, error) {
	return cast[[]driver.NotebookLifecycleConfig](s.do(ctx, "ListNotebookInstanceLifecycleConfigs", nil, func() (any, error) {
		return s.drv.ListNotebookInstanceLifecycleConfigs(ctx)
	}))
}

func (s *SageMaker) DeleteNotebookInstanceLifecycleConfig(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteNotebookInstanceLifecycleConfig", name, func() (any, error) {
		return nil, s.drv.DeleteNotebookInstanceLifecycleConfig(ctx, name)
	})

	return err
}

func (s *SageMaker) CreateCodeRepository(ctx context.Context, cfg driver.CodeRepositorySpec) (*driver.CodeRepository, error) {
	return cast[*driver.CodeRepository](s.do(ctx, "CreateCodeRepository", cfg, func() (any, error) {
		return s.drv.CreateCodeRepository(ctx, cfg)
	}))
}

func (s *SageMaker) DescribeCodeRepository(ctx context.Context, name string) (*driver.CodeRepository, error) {
	return cast[*driver.CodeRepository](s.do(ctx, "DescribeCodeRepository", name, func() (any, error) {
		return s.drv.DescribeCodeRepository(ctx, name)
	}))
}

func (s *SageMaker) ListCodeRepositories(ctx context.Context) ([]driver.CodeRepository, error) {
	return cast[[]driver.CodeRepository](s.do(ctx, "ListCodeRepositories", nil, func() (any, error) {
		return s.drv.ListCodeRepositories(ctx)
	}))
}

func (s *SageMaker) DeleteCodeRepository(ctx context.Context, name string) error {
	_, err := s.do(ctx, "DeleteCodeRepository", name, func() (any, error) { return nil, s.drv.DeleteCodeRepository(ctx, name) })

	return err
}
