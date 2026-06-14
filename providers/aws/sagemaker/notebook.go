package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// --- Notebook instance ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateNotebookInstance(_ context.Context, cfg driver.NotebookInstanceSpec) (*driver.NotebookInstance, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "notebookInstanceName is required")
	}

	if m.notebooks.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "notebook instance %q already exists", cfg.Name)
	}

	now := m.now()
	arn := m.arn("notebook-instance/" + cfg.Name)
	nb := &driver.NotebookInstance{
		Name:             cfg.Name,
		ARN:              arn,
		InstanceType:     cfg.InstanceType,
		RoleARN:          cfg.RoleARN,
		VolumeSizeInGB:   cfg.VolumeSizeInGB,
		LifecycleConfig:  cfg.LifecycleConfig,
		DefaultCodeRepo:  cfg.DefaultCodeRepo,
		Status:           driver.NotebookInService, // synchronous Pending -> InService
		URL:              cfg.Name + ".notebook." + m.opts.Region + ".sagemaker.aws",
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}
	m.notebooks.Set(cfg.Name, nb)
	m.setTags(arn, cfg.Tags)
	m.emitResourceCreated("NotebookInstance")

	out := *nb

	return &out, nil
}

func (m *Mock) DescribeNotebookInstance(_ context.Context, name string) (*driver.NotebookInstance, error) {
	nb, ok := m.notebooks.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	out := *nb

	return &out, nil
}

func (m *Mock) ListNotebookInstances(_ context.Context) ([]driver.NotebookInstance, error) {
	all := m.notebooks.All()
	out := make([]driver.NotebookInstance, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

// StartNotebookInstance moves a Stopped/Failed instance to InService. Starting
// an instance that is not stopped is rejected, matching real SageMaker.
func (m *Mock) StartNotebookInstance(_ context.Context, name string) error {
	return m.transitionNotebook(name, driver.NotebookInService,
		map[string]bool{driver.NotebookStopped: true, driver.NotebookFailed: true})
}

// StopNotebookInstance moves an InService instance to Stopped. Stopping an
// instance that is not in service is rejected, matching real SageMaker.
func (m *Mock) StopNotebookInstance(_ context.Context, name string) error {
	return m.transitionNotebook(name, driver.NotebookStopped,
		map[string]bool{driver.NotebookInService: true})
}

// transitionNotebook copy-then-Sets the notebook to target only when its
// current status is in allowedFrom; otherwise it returns FailedPrecondition.
func (m *Mock) transitionNotebook(name, target string, allowedFrom map[string]bool) error {
	nb, ok := m.notebooks.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	if !allowedFrom[nb.Status] {
		return errors.Newf(errors.FailedPrecondition,
			"notebook instance %q is %s; cannot transition to %s", name, nb.Status, target)
	}

	updated := *nb
	updated.Status = target
	updated.LastModifiedTime = m.now()
	m.notebooks.Set(name, &updated)

	return nil
}

func (m *Mock) DeleteNotebookInstance(_ context.Context, name string) error {
	if !m.notebooks.Has(name) {
		return errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	m.notebooks.Delete(name)

	return nil
}

// --- Notebook lifecycle config ---

func (m *Mock) CreateNotebookInstanceLifecycleConfig(
	_ context.Context, cfg driver.NotebookLifecycleConfigSpec,
) (*driver.NotebookLifecycleConfig, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "notebookInstanceLifecycleConfigName is required")
	}

	if m.notebookLCs.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "lifecycle config %q already exists", cfg.Name)
	}

	lc := &driver.NotebookLifecycleConfig{
		Name:         cfg.Name,
		ARN:          m.arn("notebook-instance-lifecycle-config/" + cfg.Name),
		OnCreate:     cfg.OnCreate,
		OnStart:      cfg.OnStart,
		CreationTime: m.now(),
	}
	m.notebookLCs.Set(cfg.Name, lc)

	out := *lc

	return &out, nil
}

func (m *Mock) DescribeNotebookInstanceLifecycleConfig(_ context.Context, name string) (*driver.NotebookLifecycleConfig, error) {
	lc, ok := m.notebookLCs.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "lifecycle config %q not found", name)
	}

	out := *lc

	return &out, nil
}

func (m *Mock) ListNotebookInstanceLifecycleConfigs(_ context.Context) ([]driver.NotebookLifecycleConfig, error) {
	all := m.notebookLCs.All()
	out := make([]driver.NotebookLifecycleConfig, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteNotebookInstanceLifecycleConfig(_ context.Context, name string) error {
	if !m.notebookLCs.Has(name) {
		return errors.Newf(errors.NotFound, "lifecycle config %q not found", name)
	}

	m.notebookLCs.Delete(name)

	return nil
}

// --- Code repository ---

func (m *Mock) CreateCodeRepository(_ context.Context, cfg driver.CodeRepositorySpec) (*driver.CodeRepository, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "codeRepositoryName is required")
	}

	if m.codeRepos.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "code repository %q already exists", cfg.Name)
	}

	repo := &driver.CodeRepository{
		Name:         cfg.Name,
		ARN:          m.arn("code-repository/" + cfg.Name),
		GitURL:       cfg.GitURL,
		Branch:       cfg.Branch,
		SecretARN:    cfg.SecretARN,
		CreationTime: m.now(),
	}
	m.codeRepos.Set(cfg.Name, repo)

	out := *repo

	return &out, nil
}

func (m *Mock) DescribeCodeRepository(_ context.Context, name string) (*driver.CodeRepository, error) {
	repo, ok := m.codeRepos.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "code repository %q not found", name)
	}

	out := *repo

	return &out, nil
}

func (m *Mock) ListCodeRepositories(_ context.Context) ([]driver.CodeRepository, error) {
	all := m.codeRepos.All()
	out := make([]driver.CodeRepository, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteCodeRepository(_ context.Context, name string) error {
	if !m.codeRepos.Has(name) {
		return errors.Newf(errors.NotFound, "code repository %q not found", name)
	}

	m.codeRepos.Delete(name)

	return nil
}
