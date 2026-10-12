package sagemaker

import (
	"context"
	"regexp"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Notebook instance ---

// IAM role ARN length bounds (UpdateNotebookInstance RoleArn).
const (
	minRoleARNLen = 20
	maxRoleARNLen = 2048
)

// roleARNPattern is the IAM role ARN shape SageMaker accepts for RoleArn.
var roleARNPattern = regexp.MustCompile(`^arn:aws[a-z\-]*:iam::\d{12}:role/?[a-zA-Z_0-9+=,.@\-_/]+$`) //nolint:gochecknoglobals // compiled once

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateNotebookInstance(_ context.Context, cfg driver.NotebookInstanceSpec) (*driver.NotebookInstance, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "notebookInstanceName is required")
	}

	if len(cfg.AdditionalCodeRepos) > driver.MaxAdditionalCodeRepos {
		return nil, errors.Newf(errors.InvalidArgument,
			"additionalCodeRepositories must have at most %d items", driver.MaxAdditionalCodeRepos)
	}

	now := m.now()
	arn := m.arn("notebook-instance/" + cfg.Name)
	nb := &driver.NotebookInstance{
		Name:                 cfg.Name,
		ARN:                  arn,
		InstanceType:         cfg.InstanceType,
		RoleARN:              cfg.RoleARN,
		VolumeSizeInGB:       volumeOrDefault(cfg.VolumeSizeInGB),
		LifecycleConfig:      cfg.LifecycleConfig,
		DefaultCodeRepo:      cfg.DefaultCodeRepo,
		AdditionalCodeRepos:  append([]string(nil), cfg.AdditionalCodeRepos...),
		RootAccess:           orDefault(cfg.RootAccess, driver.NotebookRootAccessOn),
		SubnetID:             cfg.SubnetID,
		SecurityGroups:       append([]string(nil), cfg.SecurityGroups...),
		KmsKeyID:             cfg.KmsKeyID,
		DirectInternetAccess: orDefault(cfg.DirectInternetAccess, driver.NotebookRootAccessOn),
		PlatformIdentifier:   orDefault(cfg.PlatformIdentifier, "notebook-al2-v2"),
		Status:               driver.NotebookInService, // synchronous Pending -> InService
		URL:                  cfg.Name + ".notebook." + m.opts.Region + ".sagemaker.aws",
		CreationTime:         now,
		LastModifiedTime:     now,
		Tags:                 copyTags(cfg.Tags),
	}
	if !m.notebooks.SetIfAbsent(cfg.Name, nb) {
		return nil, errors.Newf(errors.AlreadyExists, "notebook instance %q already exists", cfg.Name)
	}
	m.setTags(arn, cfg.Tags)

	return cloneNotebook(nb), nil
}

func volumeOrDefault(gb int) int {
	if gb == 0 {
		return driver.DefaultNotebookVolumeGB
	}

	return gb
}

// cloneNotebook deep-copies the slices of a stored notebook instance.
func cloneNotebook(in *driver.NotebookInstance) *driver.NotebookInstance {
	out := *in
	out.AdditionalCodeRepos = append([]string(nil), in.AdditionalCodeRepos...)
	out.SecurityGroups = append([]string(nil), in.SecurityGroups...)
	out.Tags = copyTags(in.Tags)

	return &out
}

// observeNotebook overlays the in-flight Updating window (--async-settle) on a
// copy of a stored notebook instance.
func (m *Mock) observeNotebook(nb *driver.NotebookInstance) *driver.NotebookInstance {
	out := cloneNotebook(nb)
	// Instances restored from an older snapshot predate these members.
	out.DirectInternetAccess = orDefault(out.DirectInternetAccess, driver.NotebookRootAccessOn)
	out.PlatformIdentifier = orDefault(out.PlatformIdentifier, "notebook-al2-v2")
	out.Status = m.nbSettle.State(nb.Name, m.opts.Clock.Now(), nb.Status)

	return out
}

func (m *Mock) DescribeNotebookInstance(_ context.Context, name string) (*driver.NotebookInstance, error) {
	nb, ok := m.notebooks.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	return m.observeNotebook(nb), nil
}

func (m *Mock) ListNotebookInstances(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.NotebookInstance], error) {
	stored := m.notebooks.SortedValues()
	observed := make([]*driver.NotebookInstance, 0, len(stored))

	for _, nb := range stored {
		observed = append(observed, m.observeNotebook(nb))
	}

	return listItems(observed, listSpec[driver.NotebookInstance]{
		name:     func(t *driver.NotebookInstance) string { return t.Name },
		status:   func(t *driver.NotebookInstance) string { return t.Status },
		created:  func(t *driver.NotebookInstance) string { return t.CreationTime },
		modified: func(t *driver.NotebookInstance) string { return t.LastModifiedTime },
	}, opts)
}

// StartNotebookInstance moves a Stopped instance to InService. Starting an
// instance that is not stopped is rejected, matching real SageMaker.
func (m *Mock) StartNotebookInstance(_ context.Context, name string) error {
	return m.transitionNotebook(name, driver.NotebookInService,
		map[string]bool{driver.NotebookStopped: true})
}

// StopNotebookInstance moves an InService instance to Stopped. Stopping an
// instance that is not in service is rejected, matching real SageMaker.
func (m *Mock) StopNotebookInstance(_ context.Context, name string) error {
	return m.transitionNotebook(name, driver.NotebookStopped,
		map[string]bool{driver.NotebookInService: true})
}

// transitionNotebook copy-then-Sets the notebook to target only when its
// observed status is in allowedFrom; otherwise it returns FailedPrecondition.
func (m *Mock) transitionNotebook(name, target string, allowedFrom map[string]bool) error {
	m.nbMu.Lock()
	defer m.nbMu.Unlock()

	nb, ok := m.notebooks.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	status := m.observeNotebook(nb).Status
	if !allowedFrom[status] {
		return errors.Newf(errors.FailedPrecondition,
			"notebook instance %q is %s; cannot transition to %s", name, status, target)
	}

	updated := *nb
	updated.Status = target
	updated.LastModifiedTime = m.now()
	m.notebooks.Set(name, &updated)

	return nil
}

// UpdateNotebookInstance changes a stopped notebook instance's settings. The
// instance must be Stopped (or Failed); it then passes through Updating and
// returns to Stopped, and the user starts it again.
func (m *Mock) UpdateNotebookInstance(_ context.Context, up driver.NotebookInstanceUpdate) error {
	m.nbMu.Lock()
	defer m.nbMu.Unlock()

	nb, ok := m.notebooks.Get(up.Name)
	if !ok {
		return errors.Newf(errors.NotFound, "notebook instance %q not found", up.Name)
	}

	if status := m.observeNotebook(nb).Status; status != driver.NotebookStopped && status != driver.NotebookFailed {
		return errors.Newf(errors.FailedPrecondition,
			"Status (%s) not in ([Stopped, Failed]). Unable to transition to (Updating) for Notebook Instance (%s)",
			status, nb.ARN)
	}

	updated := *cloneNotebook(nb)
	if err := m.applyNotebookUpdate(&updated, &up); err != nil {
		return err
	}

	updated.Status = driver.NotebookStopped
	updated.LastModifiedTime = m.now()
	m.notebooks.Set(up.Name, &updated)
	m.nbSettle.Begin(up.Name, driver.NotebookUpdating, m.opts.Clock.Now(),
		m.opts.SettleDuration(settle.DefaultNotebookUpdateSettle))

	return nil
}

// applyNotebookUpdate validates up and applies it onto nb.
func (m *Mock) applyNotebookUpdate(nb *driver.NotebookInstance, up *driver.NotebookInstanceUpdate) error {
	if up.InstanceType != nil {
		if *up.InstanceType == "" {
			return errors.New(errors.InvalidArgument, "instanceType must not be empty")
		}

		nb.InstanceType = *up.InstanceType
	}

	if up.RoleARN != nil {
		if len(*up.RoleARN) < minRoleARNLen || len(*up.RoleARN) > maxRoleARNLen || !roleARNPattern.MatchString(*up.RoleARN) {
			return errors.Newf(errors.InvalidArgument, "roleArn %q is not a valid IAM role ARN", *up.RoleARN)
		}

		nb.RoleARN = *up.RoleARN
	}

	if up.VolumeSizeInGB != nil {
		v := *up.VolumeSizeInGB
		if v < driver.MinNotebookVolumeGB || v > driver.MaxNotebookVolumeGB {
			return errors.Newf(errors.InvalidArgument, "volumeSizeInGB must be between %d and %d",
				driver.MinNotebookVolumeGB, driver.MaxNotebookVolumeGB)
		}

		if v < nb.VolumeSizeInGB {
			return errors.Newf(errors.InvalidArgument,
				"volumeSizeInGB can only be increased (currently %d GB)", nb.VolumeSizeInGB)
		}

		nb.VolumeSizeInGB = v
	}

	if up.RootAccess != nil {
		if *up.RootAccess != driver.NotebookRootAccessOn && *up.RootAccess != driver.NotebookRootAccessOff {
			return errors.Newf(errors.InvalidArgument, "rootAccess must be %s or %s",
				driver.NotebookRootAccessOn, driver.NotebookRootAccessOff)
		}

		nb.RootAccess = *up.RootAccess
	}

	return m.applyNotebookAssociations(nb, up)
}

// applyNotebookAssociations applies the lifecycle-config and code-repository
// settings of an update. Disassociating something not associated is not an
// error.
func (m *Mock) applyNotebookAssociations(nb *driver.NotebookInstance, up *driver.NotebookInstanceUpdate) error {
	switch {
	case up.DisassociateLifecycleConfig:
		nb.LifecycleConfig = ""
	case up.LifecycleConfig != nil:
		if !m.notebookLCs.Has(*up.LifecycleConfig) {
			return errors.Newf(errors.InvalidArgument, "lifecycle config %q not found", *up.LifecycleConfig)
		}

		nb.LifecycleConfig = *up.LifecycleConfig
	}

	switch {
	case up.DisassociateDefaultCodeRepo:
		nb.DefaultCodeRepo = ""
	case up.DefaultCodeRepo != nil:
		nb.DefaultCodeRepo = *up.DefaultCodeRepo
	}

	switch {
	case up.DisassociateAdditionalCodeRepos:
		nb.AdditionalCodeRepos = nil
	case up.AdditionalCodeRepos != nil:
		if len(up.AdditionalCodeRepos) > driver.MaxAdditionalCodeRepos {
			return errors.Newf(errors.InvalidArgument,
				"additionalCodeRepositories must have at most %d items", driver.MaxAdditionalCodeRepos)
		}

		nb.AdditionalCodeRepos = append([]string(nil), up.AdditionalCodeRepos...)
	}

	return nil
}

func (m *Mock) DeleteNotebookInstance(_ context.Context, name string) error {
	m.nbMu.Lock()
	defer m.nbMu.Unlock()

	if !m.notebooks.Has(name) {
		return errors.Newf(errors.NotFound, "notebook instance %q not found", name)
	}

	m.notebooks.Delete(name)
	m.nbSettle.Clear(name)
	m.dropTags(m.arn("notebook-instance/" + name))

	return nil
}

// --- Notebook lifecycle config ---

func (m *Mock) CreateNotebookInstanceLifecycleConfig(
	_ context.Context, cfg driver.NotebookLifecycleConfigSpec,
) (*driver.NotebookLifecycleConfig, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "notebookInstanceLifecycleConfigName is required")
	}

	now := m.now()
	lc := &driver.NotebookLifecycleConfig{
		Name:             cfg.Name,
		ARN:              m.arn("notebook-instance-lifecycle-config/" + cfg.Name),
		OnCreate:         cfg.OnCreate,
		OnStart:          cfg.OnStart,
		CreationTime:     now,
		LastModifiedTime: now,
	}
	if !m.notebookLCs.SetIfAbsent(cfg.Name, lc) {
		return nil, errors.Newf(errors.AlreadyExists, "lifecycle config %q already exists", cfg.Name)
	}

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

func (m *Mock) ListNotebookInstanceLifecycleConfigs(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.NotebookLifecycleConfig], error) {
	return listFrom(m.notebookLCs, listSpec[driver.NotebookLifecycleConfig]{
		name:    func(t *driver.NotebookLifecycleConfig) string { return t.Name },
		created: func(t *driver.NotebookLifecycleConfig) string { return t.CreationTime },
	}, opts)
}

// UpdateNotebookInstanceLifecycleConfig replaces the scripts of a lifecycle
// config. There is no status gate: instances using the config pick the change up
// on their next start.
func (m *Mock) UpdateNotebookInstanceLifecycleConfig(_ context.Context, up driver.NotebookLifecycleConfigUpdate) error {
	if !m.notebookLCs.Update(up.Name, func(lc *driver.NotebookLifecycleConfig) *driver.NotebookLifecycleConfig {
		out := *lc
		if up.OnCreate != nil {
			out.OnCreate = *up.OnCreate
		}

		if up.OnStart != nil {
			out.OnStart = *up.OnStart
		}

		out.LastModifiedTime = m.now()

		return &out
	}) {
		return errors.Newf(errors.NotFound, "lifecycle config %q not found", up.Name)
	}

	return nil
}

func (m *Mock) DeleteNotebookInstanceLifecycleConfig(_ context.Context, name string) error {
	if !m.notebookLCs.Has(name) {
		return errors.Newf(errors.NotFound, "lifecycle config %q not found", name)
	}

	m.notebookLCs.Delete(name)
	m.dropTags(m.arn("notebook-instance-lifecycle-config/" + name))

	return nil
}

// --- Code repository ---

func (m *Mock) CreateCodeRepository(_ context.Context, cfg driver.CodeRepositorySpec) (*driver.CodeRepository, error) {
	if cfg.Name == "" {
		return nil, errors.New(errors.InvalidArgument, "codeRepositoryName is required")
	}

	repo := &driver.CodeRepository{
		Name:         cfg.Name,
		ARN:          m.arn("code-repository/" + cfg.Name),
		GitURL:       cfg.GitURL,
		Branch:       cfg.Branch,
		SecretARN:    cfg.SecretARN,
		CreationTime: m.now(),
	}
	if !m.codeRepos.SetIfAbsent(cfg.Name, repo) {
		return nil, errors.Newf(errors.AlreadyExists, "code repository %q already exists", cfg.Name)
	}

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

func (m *Mock) ListCodeRepositories(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.CodeRepository], error) {
	return listFrom(m.codeRepos, listSpec[driver.CodeRepository]{
		name:    func(t *driver.CodeRepository) string { return t.Name },
		created: func(t *driver.CodeRepository) string { return t.CreationTime },
	}, opts)
}

func (m *Mock) DeleteCodeRepository(_ context.Context, name string) error {
	if !m.codeRepos.Has(name) {
		return errors.Newf(errors.NotFound, "code repository %q not found", name)
	}

	m.codeRepos.Delete(name)
	m.dropTags(m.arn("code-repository/" + name))

	return nil
}
