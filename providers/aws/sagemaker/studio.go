package sagemaker

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

// scopedKey joins a domain ID and a child name into a single store key.
func scopedKey(domainID, name string) string {
	return domainID + "\x00" + name
}

// appKey builds the composite key identifying a Studio app.
func appKey(in *driver.AppSpec) string {
	return in.DomainID + "\x00" + in.UserProfileName + "\x00" + in.SpaceName + "\x00" + in.AppType + "\x00" + in.AppName
}

// --- Domain ---

//nolint:gocritic // cfg matches the driver signature; copied on entry.
func (m *Mock) CreateDomain(_ context.Context, cfg driver.DomainSpec) (*driver.Domain, error) {
	if cfg.DomainName == "" {
		return nil, errors.New(errors.InvalidArgument, "domainName is required")
	}

	id := "d-" + idgen.GenerateID("")
	arn := m.arn("domain/" + id)
	d := &driver.Domain{
		DomainID:         id,
		DomainName:       cfg.DomainName,
		DomainARN:        arn,
		AuthMode:         orDefault(cfg.AuthMode, "IAM"),
		VPCID:            cfg.VPCID,
		SubnetIDs:        cfg.SubnetIDs,
		ExecutionRoleARN: cfg.ExecutionRoleARN,
		URL:              "https://" + id + ".studio." + m.opts.Region + ".sagemaker.aws",
		Status:           driver.StudioInService,
		CreationTime:     m.now(),
		Tags:             copyTags(cfg.Tags),
	}
	m.domains.Set(id, d)
	m.setTags(arn, cfg.Tags)

	out := *d

	return &out, nil
}

func (m *Mock) DescribeDomain(_ context.Context, domainID string) (*driver.Domain, error) {
	d, ok := m.domains.Get(domainID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "domain %q not found", domainID)
	}

	out := *d

	return &out, nil
}

func (m *Mock) ListDomains(_ context.Context) ([]driver.Domain, error) {
	all := m.domains.All()
	out := make([]driver.Domain, 0, len(all))

	for _, v := range all {
		out = append(out, *v)
	}

	return out, nil
}

func (m *Mock) DeleteDomain(_ context.Context, domainID string) error {
	if !m.domains.Has(domainID) {
		return errors.Newf(errors.NotFound, "domain %q not found", domainID)
	}

	m.domains.Delete(domainID)

	return nil
}

// --- User profile ---

func (m *Mock) CreateUserProfile(_ context.Context, cfg driver.UserProfileSpec) (*driver.UserProfile, error) {
	if cfg.DomainID == "" || cfg.UserProfileName == "" {
		return nil, errors.New(errors.InvalidArgument, "domainId and userProfileName are required")
	}

	if !m.domains.Has(cfg.DomainID) {
		return nil, errors.Newf(errors.InvalidArgument, "domain %q not found", cfg.DomainID)
	}

	key := scopedKey(cfg.DomainID, cfg.UserProfileName)
	if m.userProfiles.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "user profile %q already exists", cfg.UserProfileName)
	}

	arn := m.arn("user-profile/" + cfg.DomainID + "/" + cfg.UserProfileName)
	up := &driver.UserProfile{
		DomainID:         cfg.DomainID,
		UserProfileName:  cfg.UserProfileName,
		UserProfileARN:   arn,
		ExecutionRoleARN: cfg.ExecutionRoleARN,
		Status:           driver.StudioInService,
		CreationTime:     m.now(),
		Tags:             copyTags(cfg.Tags),
	}
	m.userProfiles.Set(key, up)
	m.setTags(arn, cfg.Tags)

	out := *up

	return &out, nil
}

func (m *Mock) DescribeUserProfile(_ context.Context, domainID, name string) (*driver.UserProfile, error) {
	up, ok := m.userProfiles.Get(scopedKey(domainID, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "user profile %q not found", name)
	}

	out := *up

	return &out, nil
}

func (m *Mock) ListUserProfiles(_ context.Context, domainID string) ([]driver.UserProfile, error) {
	out := make([]driver.UserProfile, 0)

	for _, up := range m.userProfiles.All() {
		if domainID == "" || up.DomainID == domainID {
			out = append(out, *up)
		}
	}

	return out, nil
}

func (m *Mock) DeleteUserProfile(_ context.Context, domainID, name string) error {
	key := scopedKey(domainID, name)
	if !m.userProfiles.Has(key) {
		return errors.Newf(errors.NotFound, "user profile %q not found", name)
	}

	m.userProfiles.Delete(key)

	return nil
}

// --- Space ---

func (m *Mock) CreateSpace(_ context.Context, cfg driver.SpaceSpec) (*driver.Space, error) {
	if cfg.DomainID == "" || cfg.SpaceName == "" {
		return nil, errors.New(errors.InvalidArgument, "domainId and spaceName are required")
	}

	if !m.domains.Has(cfg.DomainID) {
		return nil, errors.Newf(errors.InvalidArgument, "domain %q not found", cfg.DomainID)
	}

	key := scopedKey(cfg.DomainID, cfg.SpaceName)
	if m.spaces.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "space %q already exists", cfg.SpaceName)
	}

	arn := m.arn("space/" + cfg.DomainID + "/" + cfg.SpaceName)
	sp := &driver.Space{
		DomainID:     cfg.DomainID,
		SpaceName:    cfg.SpaceName,
		SpaceARN:     arn,
		Status:       driver.StudioInService,
		CreationTime: m.now(),
		Tags:         copyTags(cfg.Tags),
	}
	m.spaces.Set(key, sp)
	m.setTags(arn, cfg.Tags)

	out := *sp

	return &out, nil
}

func (m *Mock) DescribeSpace(_ context.Context, domainID, name string) (*driver.Space, error) {
	sp, ok := m.spaces.Get(scopedKey(domainID, name))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "space %q not found", name)
	}

	out := *sp

	return &out, nil
}

func (m *Mock) ListSpaces(_ context.Context, domainID string) ([]driver.Space, error) {
	out := make([]driver.Space, 0)

	for _, sp := range m.spaces.All() {
		if domainID == "" || sp.DomainID == domainID {
			out = append(out, *sp)
		}
	}

	return out, nil
}

func (m *Mock) DeleteSpace(_ context.Context, domainID, name string) error {
	key := scopedKey(domainID, name)
	if !m.spaces.Has(key) {
		return errors.Newf(errors.NotFound, "space %q not found", name)
	}

	m.spaces.Delete(key)

	return nil
}

// --- App ---

//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) CreateApp(_ context.Context, in driver.AppSpec) (*driver.App, error) {
	if in.DomainID == "" || in.AppName == "" || in.AppType == "" {
		return nil, errors.New(errors.InvalidArgument, "domainId, appType and appName are required")
	}

	if !m.domains.Has(in.DomainID) {
		return nil, errors.Newf(errors.InvalidArgument, "domain %q not found", in.DomainID)
	}

	key := appKey(&in)
	if m.apps.Has(key) {
		return nil, errors.Newf(errors.AlreadyExists, "app %q already exists", in.AppName)
	}

	arn := m.arn("app/" + in.DomainID + "/" + in.AppType + "/" + in.AppName)
	app := &driver.App{
		DomainID:        in.DomainID,
		UserProfileName: in.UserProfileName,
		SpaceName:       in.SpaceName,
		AppType:         in.AppType,
		AppName:         in.AppName,
		AppARN:          arn,
		Status:          driver.AppInService,
		CreationTime:    m.now(),
	}
	m.apps.Set(key, app)

	out := *app

	return &out, nil
}

//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) DescribeApp(_ context.Context, in driver.AppSpec) (*driver.App, error) {
	app, ok := m.apps.Get(appKey(&in))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "app %q not found", in.AppName)
	}

	out := *app

	return &out, nil
}

func (m *Mock) ListApps(_ context.Context, domainID string) ([]driver.App, error) {
	out := make([]driver.App, 0)

	for _, app := range m.apps.All() {
		if domainID == "" || app.DomainID == domainID {
			out = append(out, *app)
		}
	}

	return out, nil
}

//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) DeleteApp(_ context.Context, in driver.AppSpec) error {
	key := appKey(&in)

	app, ok := m.apps.Get(key)
	if !ok {
		return errors.Newf(errors.NotFound, "app %q not found", in.AppName)
	}

	app.Status = driver.AppDeleted

	m.apps.Delete(key)

	return nil
}
