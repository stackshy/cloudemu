package sagemaker

import (
	"context"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxProjectNameLen   = 32
	maxProjectDescLen   = 1024
	projectSettleWindow = settle.DefaultProjectSettle
)

var projectNamePattern = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9])*$`)

// observeProject overlays an in-flight --async-settle window on a copy of a
// stored project.
func (m *Mock) observeProject(p *driver.Project) *driver.Project {
	out := cloneProject(p)
	out.Status = m.projSettle.State(p.Name, m.opts.Clock.Now(), p.Status)

	return out
}

func cloneProject(in *driver.Project) *driver.Project {
	out := *in
	out.Tags = copyTags(in.Tags)

	if in.ServiceCatalog != nil {
		sc := *in.ServiceCatalog
		sc.Parameters = append([]driver.ProvisioningParameter(nil), in.ServiceCatalog.Parameters...)
		out.ServiceCatalog = &sc
	}

	out.TemplateProviders = nil

	for _, t := range in.TemplateProviders {
		t.Parameters = append([]driver.ProvisioningParameter(nil), t.Parameters...)
		out.TemplateProviders = append(out.TemplateProviders, t)
	}

	return &out
}

// CreateProject creates a project. At most MaxProjects exist per account and
// region; the cap and the create are one critical section.
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) CreateProject(_ context.Context, spec driver.ProjectSpec) (*driver.Project, error) {
	if err := validateProject(&spec); err != nil {
		return nil, err
	}

	m.projMu.Lock()
	defer m.projMu.Unlock()

	if m.projects.Has(spec.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "project %q already exists", spec.Name)
	}

	if m.projects.Len() >= driver.MaxProjects {
		return nil, errors.Newf(errors.ResourceExhausted, "the account has reached the limit of %d projects", driver.MaxProjects)
	}

	now := m.now()
	arn := m.arn("project/" + spec.Name)
	p := &driver.Project{
		Name:              spec.Name,
		ARN:               arn,
		ID:                strings.ToLower(idgen.GenerateID("p-")),
		Description:       spec.Description,
		Status:            driver.ProjectCreateCompleted,
		ServiceCatalog:    spec.ServiceCatalog,
		TemplateProviders: spec.TemplateProviders,
		CreationTime:      now,
		LastModifiedTime:  now,
		Tags:              copyTags(spec.Tags),
	}

	if spec.ServiceCatalog != nil {
		p.ProvisionedProductID = strings.ToLower(idgen.GenerateID("pp-"))
	}

	p = cloneProject(p)

	if !m.projects.SetIfAbsent(spec.Name, p) {
		return nil, errors.Newf(errors.AlreadyExists, "project %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)
	m.projSettle.Begin(spec.Name, driver.ProjectCreateInProgres, m.opts.Clock.Now(), m.opts.SettleDuration(projectSettleWindow))

	return m.observeProject(p), nil
}

//nolint:gocyclo // validation branches mirror the service's rules
func validateProject(spec *driver.ProjectSpec) error {
	if spec.Name == "" || len(spec.Name) > maxProjectNameLen || !projectNamePattern.MatchString(spec.Name) {
		return errors.Newf(errors.InvalidArgument, "projectName %q must be 1-%d characters of letters, digits and hyphens", spec.Name, maxProjectNameLen) //nolint:lll // SDK-shaped literal
	}

	if len(spec.Description) > maxProjectDescLen {
		return errors.Newf(errors.InvalidArgument, "projectDescription must be at most %d characters", maxProjectDescLen)
	}

	if err := validateTags(spec.Tags); err != nil {
		return err
	}

	if len(spec.Tags) > maxTagsPerResource {
		return errors.Newf(errors.InvalidArgument, "tags must have at most %d items", maxTagsPerResource)
	}

	hasSC, hasTP := spec.ServiceCatalog != nil, len(spec.TemplateProviders) > 0
	if hasSC == hasTP {
		return errors.New(errors.InvalidArgument,
			"specify exactly one of serviceCatalogProvisioningDetails and templateProviders")
	}

	if hasSC && spec.ServiceCatalog.ProductID == "" {
		return errors.New(errors.InvalidArgument, "serviceCatalogProvisioningDetails.productId is required")
	}

	if len(spec.TemplateProviders) > 1 {
		return errors.New(errors.InvalidArgument, "templateProviders must contain exactly 1 item")
	}

	return nil
}

func (m *Mock) DescribeProject(_ context.Context, name string) (*driver.Project, error) {
	p, ok := m.projects.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "project %q not found", name)
	}

	return m.observeProject(p), nil
}

//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListProjects(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Project], error) {
	return listObserved(m.projects, m.observeProject, listSpec[driver.Project]{
		name:    func(t *driver.Project) string { return t.Name },
		status:  func(t *driver.Project) string { return t.Status },
		created: func(t *driver.Project) string { return t.CreationTime },
	}, opts)
}

// inFlight reports whether a project status means an operation is running.
func projectInFlight(status string) bool {
	switch status {
	case driver.ProjectPending, driver.ProjectCreateInProgres, driver.ProjectUpdateInProgres, driver.ProjectDeleteInProgres:
		return true
	default:
		return false
	}
}

// UpdateProject updates a project's description, provisioning artifact and
// parameters, tags and template providers. A project that is being created,
// updated or deleted cannot be updated (ConflictException).
//
//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) UpdateProject(_ context.Context, up driver.ProjectUpdate) (*driver.Project, error) {
	m.projMu.Lock()
	defer m.projMu.Unlock()

	p, ok := m.projects.Get(up.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "project %q not found", up.Name)
	}

	if status := m.observeProject(p).Status; projectInFlight(status) {
		return nil, driver.Conflict("Project %q is %s and cannot be updated", up.Name, status)
	}

	if up.Description != nil && len(*up.Description) > maxProjectDescLen {
		return nil, errors.Newf(errors.InvalidArgument, "projectDescription must be at most %d characters", maxProjectDescLen)
	}

	if err := validateTags(up.Tags); err != nil {
		return nil, err
	}

	if len(up.Tags) > maxTagsPerResource {
		return nil, errors.Newf(errors.InvalidArgument, "tags must have at most %d items", maxTagsPerResource)
	}

	updated := cloneProject(p)
	applyProjectUpdate(updated, &up)
	updated.Status = driver.ProjectUpdateCompleted
	updated.LastModifiedTime = m.now()
	m.projects.Set(up.Name, updated)

	if up.Tags != nil {
		m.setTags(p.ARN, up.Tags)
	}

	m.projSettle.Begin(up.Name, driver.ProjectUpdateInProgres, m.opts.Clock.Now(), m.opts.SettleDuration(projectSettleWindow))

	return m.observeProject(updated), nil
}

//nolint:gocyclo // validation branches mirror the service's rules
func applyProjectUpdate(p *driver.Project, up *driver.ProjectUpdate) {
	if up.Description != nil {
		p.Description = *up.Description
	}

	if p.ServiceCatalog != nil && (up.ProvisioningArtifactID != "" || up.Parameters != nil) {
		if up.ProvisioningArtifactID != "" {
			p.ServiceCatalog.ProvisioningArtifactID = up.ProvisioningArtifactID
		}

		if up.Parameters != nil {
			p.ServiceCatalog.Parameters = append([]driver.ProvisioningParameter(nil), up.Parameters...)
		}
	}

	if up.Tags != nil {
		p.Tags = copyTags(up.Tags)
	}

	if len(up.TemplateProvidersToUpdate) == 1 && len(p.TemplateProviders) == 1 {
		// The update shape has no RoleARN, and omitted members keep their value.
		cur := p.TemplateProviders[0]
		t := up.TemplateProvidersToUpdate[0]

		cur.TemplateName = orDefault(t.TemplateName, cur.TemplateName)
		cur.TemplateURL = orDefault(t.TemplateURL, cur.TemplateURL)

		if t.Parameters != nil {
			cur.Parameters = append([]driver.ProvisioningParameter(nil), t.Parameters...)
		}

		p.TemplateProviders = []driver.TemplateProvider{cur}
	}
}

// DeleteProject deletes a project that is not mid-operation.
func (m *Mock) DeleteProject(_ context.Context, name string) error {
	m.projMu.Lock()
	defer m.projMu.Unlock()

	p, ok := m.projects.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "project %q not found", name)
	}

	if status := m.observeProject(p).Status; projectInFlight(status) {
		return driver.Conflict("Project %q is %s and cannot be deleted", name, status)
	}

	m.projects.Delete(name)
	m.projSettle.Clear(name)
	m.dropTags(p.ARN)

	return nil
}
