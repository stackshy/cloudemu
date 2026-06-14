package sagemaker

import (
	"context"
	"strconv"

	"github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/sagemaker/driver"
)

// --- Model package group ---

func (m *Mock) CreateModelPackageGroup(_ context.Context, cfg driver.ModelPackageGroupSpec) (*driver.ModelPackageGroup, error) {
	if cfg.GroupName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelPackageGroupName is required")
	}

	if m.packageGroups.Has(cfg.GroupName) {
		return nil, errors.Newf(errors.AlreadyExists, "model package group %q already exists", cfg.GroupName)
	}

	arn := m.arn("model-package-group/" + cfg.GroupName)
	g := &driver.ModelPackageGroup{
		GroupName:    cfg.GroupName,
		GroupARN:     arn,
		Description:  cfg.Description,
		Status:       driver.PackageCompleted,
		CreationTime: m.now(),
		Tags:         copyTags(cfg.Tags),
	}
	m.packageGroups.Set(cfg.GroupName, g)
	m.setTags(arn, cfg.Tags)

	out := *g

	return &out, nil
}

func (m *Mock) DescribeModelPackageGroup(_ context.Context, name string) (*driver.ModelPackageGroup, error) {
	g, ok := m.packageGroups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	out := *g

	return &out, nil
}

func (m *Mock) ListModelPackageGroups(_ context.Context) ([]driver.ModelPackageGroup, error) {
	all := m.packageGroups.All()
	out := make([]driver.ModelPackageGroup, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteModelPackageGroup(_ context.Context, name string) error {
	if !m.packageGroups.Has(name) {
		return errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	m.packageGroups.Delete(name)

	return nil
}

// --- Model package (versioned) ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateModelPackage(_ context.Context, cfg driver.ModelPackageSpec) (*driver.ModelPackage, error) {
	if cfg.GroupName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelPackageGroupName is required")
	}

	if !m.packageGroups.Has(cfg.GroupName) {
		return nil, errors.Newf(errors.InvalidArgument, "model package group %q not found", cfg.GroupName)
	}

	// Serialize version assignment: without the lock two concurrent creates for
	// the same group could compute the same N+1 and mint a colliding ARN, and
	// the second Set would silently overwrite the first.
	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	version := m.nextPackageVersion(cfg.GroupName)
	arn := m.arn("model-package/" + cfg.GroupName + "/" + strconv.Itoa(version))
	p := &driver.ModelPackage{
		PackageARN:     arn,
		GroupName:      cfg.GroupName,
		Version:        version,
		Description:    cfg.Description,
		InferenceImage: cfg.InferenceImage,
		ModelDataURL:   cfg.ModelDataURL,
		Status:         driver.PackageCompleted,
		ApprovalStatus: orDefault(cfg.ApprovalStatus, driver.ApprovalPendingManual),
		CreationTime:   m.now(),
		Tags:           copyTags(cfg.Tags),
	}
	m.packages.Set(arn, p)
	m.setTags(arn, cfg.Tags)

	out := *p

	return &out, nil
}

func (m *Mock) nextPackageVersion(group string) int {
	highest := 0
	for _, p := range m.packages.All() {
		if p.GroupName == group && p.Version > highest {
			highest = p.Version
		}
	}

	return highest + 1
}

func (m *Mock) DescribeModelPackage(_ context.Context, arn string) (*driver.ModelPackage, error) {
	p, ok := m.packages.Get(arn)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model package %q not found", arn)
	}

	out := *p

	return &out, nil
}

func (m *Mock) ListModelPackages(_ context.Context, groupName string) ([]driver.ModelPackage, error) {
	out := make([]driver.ModelPackage, 0)

	for _, p := range m.packages.All() {
		if groupName == "" || p.GroupName == groupName {
			out = append(out, *p)
		}
	}

	return out, nil
}

func (m *Mock) UpdateModelPackage(_ context.Context, arn, approvalStatus string) (*driver.ModelPackage, error) {
	p, ok := m.packages.Get(arn)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model package %q not found", arn)
	}

	updated := *p
	if approvalStatus != "" {
		updated.ApprovalStatus = approvalStatus
	}

	m.packages.Set(arn, &updated)

	out := updated

	return &out, nil
}

func (m *Mock) DeleteModelPackage(_ context.Context, arn string) error {
	if !m.packages.Has(arn) {
		return errors.Newf(errors.NotFound, "model package %q not found", arn)
	}

	m.packages.Delete(arn)

	return nil
}
