package ec2

import (
	"context"
	"sync/atomic"

	"github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
)

//nolint:gochecknoglobals // atomic counter for template versioning
var templateVersion uint64

// CreateLaunchTemplate creates a new launch template.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) CreateLaunchTemplate(
	_ context.Context, cfg driver.LaunchTemplateConfig,
) (*driver.LaunchTemplate, error) {
	if cfg.Name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "template name is required")
	}

	if m.templates.Has(cfg.Name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "template %q already exists", cfg.Name)
	}

	ver := int(atomic.AddUint64(&templateVersion, 1)) //nolint:gosec // overflow impossible in mock

	tmpl := &driver.LaunchTemplate{
		ID:             idgen.GenerateID("lt-"),
		Name:           cfg.Name,
		Version:        ver,
		InstanceConfig: cfg.InstanceConfig,
		CreatedAt:      m.opts.Clock.Now().UTC().Format(timeFormat),
	}

	m.templates.Set(cfg.Name, tmpl)

	result := *tmpl

	return &result, nil
}

// DeleteLaunchTemplate deletes a launch template by name.
func (m *Mock) DeleteLaunchTemplate(_ context.Context, name string) error {
	if !m.templates.Delete(name) {
		return cerrors.Newf(cerrors.NotFound, "template %q not found", name)
	}

	return nil
}

// GetLaunchTemplate returns a launch template by name.
func (m *Mock) GetLaunchTemplate(_ context.Context, name string) (*driver.LaunchTemplate, error) {
	tmpl, ok := m.templates.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "template %q not found", name)
	}

	result := *tmpl

	return &result, nil
}

// ListLaunchTemplates returns all launch templates.
func (m *Mock) ListLaunchTemplates(_ context.Context) ([]driver.LaunchTemplate, error) {
	all := m.templates.All()
	results := make([]driver.LaunchTemplate, 0, len(all))

	for _, tmpl := range all {
		results = append(results, *tmpl)
	}

	return results, nil
}
