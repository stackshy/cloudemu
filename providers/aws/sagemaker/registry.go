package sagemaker

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// --- Model package group ---

func (m *Mock) CreateModelPackageGroup(_ context.Context, cfg driver.ModelPackageGroupSpec) (*driver.ModelPackageGroup, error) {
	if cfg.GroupName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelPackageGroupName is required")
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

	if !m.packageGroups.SetIfAbsent(cfg.GroupName, g) {
		return nil, errors.Newf(errors.AlreadyExists, "model package group %q already exists", cfg.GroupName)
	}
	m.setTags(arn, cfg.Tags)

	return cloneGroup(g), nil
}

// groupNameOf accepts a model package group name or its ARN.
func (m *Mock) groupNameOf(nameOrARN string) string {
	return strings.TrimPrefix(nameOrARN, m.arn("model-package-group/"))
}

func (m *Mock) DescribeModelPackageGroup(_ context.Context, name string) (*driver.ModelPackageGroup, error) {
	name = m.groupNameOf(name)

	g, ok := m.packageGroups.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	return cloneGroup(g), nil
}

//nolint:gocritic // SDK-shaped; see nongoals/standards
func (m *Mock) ListModelPackageGroups(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.ModelPackageGroup], error) {
	return listFrom(m.packageGroups, listSpec[driver.ModelPackageGroup]{
		clone:   func(t *driver.ModelPackageGroup) driver.ModelPackageGroup { return *cloneGroup(t) },
		name:    func(t *driver.ModelPackageGroup) string { return t.GroupName },
		status:  func(t *driver.ModelPackageGroup) string { return t.Status },
		created: func(t *driver.ModelPackageGroup) string { return t.CreationTime },
	}, opts)
}

// DeleteModelPackageGroup deletes a group (by name or ARN) together with its
// model package versions, its resource policy and their tags, so nothing is left
// orphaned.
func (m *Mock) DeleteModelPackageGroup(_ context.Context, nameOrARN string) error {
	name := m.groupNameOf(nameOrARN)

	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	if !m.packageGroups.Has(name) {
		return errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	for arn, p := range m.packages.All() {
		if p.GroupName == name {
			m.packages.Delete(arn)
			m.dropTags(arn)
		}
	}

	m.packageGroups.Delete(name)
	m.packagePolicies.Delete(name)
	m.dropTags(m.arn("model-package-group/" + name))

	return nil
}

// --- Model package (versioned) ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateModelPackage(ctx context.Context, cfg driver.ModelPackageSpec) (*driver.ModelPackage, error) {
	pkg, err := m.createModelPackage(cfg)
	if err != nil {
		return nil, err
	}

	m.emitModelPackageEvent(ctx, pkg)

	return pkg, nil
}

// createModelPackage holds pkgMu for the whole create, so the event is
// published by the caller after it is released.
//
//nolint:gocritic // driver-signature params and SDK-shaped helpers
func (m *Mock) createModelPackage(cfg driver.ModelPackageSpec) (*driver.ModelPackage, error) {
	if cfg.GroupName == "" {
		return nil, errors.New(errors.InvalidArgument, "modelPackageGroupName is required")
	}

	// pkgMu serializes version assignment (two concurrent creates for the same
	// group would otherwise compute the same N+1) and the group's existence check
	// with DeleteModelPackageGroup, so a package is never created for a group
	// that is being deleted.
	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	if !m.packageGroups.Has(cfg.GroupName) {
		return nil, errors.Newf(errors.InvalidArgument, "model package group %q not found", cfg.GroupName)
	}

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

	return clonePackage(p), nil
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

	return clonePackage(p), nil
}

//nolint:gocritic,lll // SDK-shaped; see nongoals/standards
func (m *Mock) ListModelPackages(_ context.Context, groupName string, opts driver.ListOptions) (driver.ListPage[driver.ModelPackage], error) {
	return listFrom(m.packages, listSpec[driver.ModelPackage]{
		clone:   func(t *driver.ModelPackage) driver.ModelPackage { return *clonePackage(t) },
		name:    func(t *driver.ModelPackage) string { return t.PackageARN },
		status:  func(t *driver.ModelPackage) string { return t.Status },
		created: func(t *driver.ModelPackage) string { return t.CreationTime },
		extra: func(t *driver.ModelPackage, filters map[string]string) bool {
			if status := filters["ModelApprovalStatus"]; status != "" && t.ApprovalStatus != status {
				return false
			}

			return groupName == "" || t.GroupName == m.groupNameOf(groupName)
		},
	}, opts)
}

func (m *Mock) UpdateModelPackage(ctx context.Context, arn, approvalStatus string) (*driver.ModelPackage, error) {
	var out driver.ModelPackage

	if !m.packages.Update(arn, func(p *driver.ModelPackage) *driver.ModelPackage {
		updated := *p
		updated.Tags = copyTags(p.Tags)

		if approvalStatus != "" {
			updated.ApprovalStatus = approvalStatus
		}

		out = updated

		return &updated
	}) {
		return nil, errors.Newf(errors.NotFound, "model package %q not found", arn)
	}

	if approvalStatus != "" {
		m.emitModelPackageEvent(ctx, &out, "ModelApprovalStatus")
	}

	return &out, nil
}

func (m *Mock) DeleteModelPackage(_ context.Context, arn string) error {
	if !m.packages.Has(arn) {
		return errors.Newf(errors.NotFound, "model package %q not found", arn)
	}

	m.packages.Delete(arn)
	m.dropTags(arn)

	return nil
}

// maxPackageARNLen is the longest model package ARN SageMaker accepts.
const maxPackageARNLen = 2048

// packageARNPattern is the model package ARN shape BatchDescribeModelPackage
// accepts.
var packageARNPattern = regexp.MustCompile(`^arn:aws[a-z\-]*:sagemaker:[a-z0-9\-]{9,16}:[0-9]{12}:model-package/\S+$`)

// BatchDescribeModelPackage describes up to 100 model packages. A package that
// does not exist is reported in the per-ARN error map; it never fails the call.
func (m *Mock) BatchDescribeModelPackage(_ context.Context, arns []string) (*driver.BatchDescribeResult, error) {
	if len(arns) == 0 || len(arns) > driver.MaxBatchDescribePackages {
		return nil, errors.Newf(errors.InvalidArgument,
			"modelPackageArnList must contain between 1 and %d ARNs", driver.MaxBatchDescribePackages)
	}

	for _, a := range arns {
		if len(a) > maxPackageARNLen || !packageARNPattern.MatchString(a) {
			return nil, errors.Newf(errors.InvalidArgument, "Value '%s' at 'modelPackageArnList' is not a valid model package ARN", a)
		}
	}

	out := &driver.BatchDescribeResult{
		Packages: make(map[string]driver.ModelPackage, len(arns)),
		Errors:   map[string]driver.BatchPackageError{},
	}

	for _, a := range arns {
		p, ok := m.packages.Get(a)
		if !ok {
			out.Errors[a] = driver.BatchPackageError{Code: "ResourceNotFound", Message: "Model package " + a + " does not exist"}

			continue
		}

		out.Packages[a] = *clonePackage(p)
	}

	return out, nil
}

// PutModelPackageGroupPolicy sets (replacing any previous) the resource policy
// of a model package group and returns the group ARN.
func (m *Mock) PutModelPackageGroupPolicy(_ context.Context, nameOrARN, policy string) (string, error) {
	if policy == "" || len(policy) > driver.MaxGroupPolicyLength {
		return "", errors.Newf(errors.InvalidArgument, "resourcePolicy must be between 1 and %d characters", driver.MaxGroupPolicyLength)
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return "", errors.New(errors.InvalidArgument, "resourcePolicy must be a valid JSON policy document")
	}

	name := m.groupNameOf(nameOrARN)

	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	if !m.packageGroups.Has(name) {
		return "", errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	m.packagePolicies.Set(name, policy)

	return m.arn("model-package-group/" + name), nil
}

// GetModelPackageGroupPolicy returns the stored policy verbatim.
func (m *Mock) GetModelPackageGroupPolicy(_ context.Context, nameOrARN string) (string, error) {
	name := m.groupNameOf(nameOrARN)

	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	if !m.packageGroups.Has(name) {
		return "", errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	policy, ok := m.packagePolicies.Get(name)
	if !ok {
		return "", errors.Newf(errors.NotFound, "model package group %q has no resource policy", name)
	}

	return policy, nil
}

// DeleteModelPackageGroupPolicy removes a group's resource policy.
func (m *Mock) DeleteModelPackageGroupPolicy(_ context.Context, nameOrARN string) error {
	name := m.groupNameOf(nameOrARN)

	m.pkgMu.Lock()
	defer m.pkgMu.Unlock()

	if !m.packageGroups.Has(name) {
		return errors.Newf(errors.NotFound, "model package group %q not found", name)
	}

	m.packagePolicies.Delete(name)

	return nil
}

func cloneGroup(in *driver.ModelPackageGroup) *driver.ModelPackageGroup {
	out := *in
	out.Tags = copyTags(in.Tags)

	return &out
}

func clonePackage(in *driver.ModelPackage) *driver.ModelPackage {
	out := *in
	out.Tags = copyTags(in.Tags)

	return &out
}
