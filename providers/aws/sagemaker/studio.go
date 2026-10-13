package sagemaker

import (
	"context"
	"strings"

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
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
func (m *Mock) ListDomains(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Domain], error) {
	return listFrom(m.domains, listSpec[driver.Domain]{
		name:    func(t *driver.Domain) string { return t.DomainName },
		status:  func(t *driver.Domain) string { return t.Status },
		created: func(t *driver.Domain) string { return t.CreationTime },
	}, opts)
}

func (m *Mock) DeleteDomain(_ context.Context, domainID string) error {
	if !m.domains.Has(domainID) {
		return errors.Newf(errors.NotFound, "domain %q not found", domainID)
	}

	if m.domainHasChildren(domainID) {
		return driver.InUse("Domain %q still has user profiles, spaces or apps", domainID)
	}

	m.domains.Delete(domainID)
	m.dropTags(m.arn("domain/" + domainID))

	return nil
}

// --- User profile ---

func (m *Mock) CreateUserProfile(_ context.Context, cfg driver.UserProfileSpec) (*driver.UserProfile, error) {
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if cfg.DomainID == "" || cfg.UserProfileName == "" {
		return nil, errors.New(errors.InvalidArgument, "domainId and userProfileName are required")
	}

	if !m.domains.Has(cfg.DomainID) {
		return nil, errors.Newf(errors.InvalidArgument, "domain %q not found", cfg.DomainID)
	}

	key := scopedKey(cfg.DomainID, cfg.UserProfileName)
	arn := m.arn("user-profile/" + cfg.DomainID + "/" + cfg.UserProfileName)
	now := m.now()
	up := &driver.UserProfile{
		DomainID:         cfg.DomainID,
		UserProfileName:  cfg.UserProfileName,
		UserProfileARN:   arn,
		ExecutionRoleARN: cfg.ExecutionRoleARN,
		Status:           driver.StudioInService,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}

	if !m.userProfiles.SetIfAbsent(key, up) {
		return nil, driver.InUse("user profile %q already exists", cfg.UserProfileName)
	}
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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
func (m *Mock) ListUserProfiles(_ context.Context, domainID string, opts driver.ListOptions) (driver.ListPage[driver.UserProfile], error) {
	return listFrom(m.userProfiles, listSpec[driver.UserProfile]{
		name:    func(t *driver.UserProfile) string { return t.UserProfileName },
		status:  func(t *driver.UserProfile) string { return t.Status },
		created: func(t *driver.UserProfile) string { return t.CreationTime },
		modified: func(t *driver.UserProfile) string {
			return modifiedOrCreated(t.LastModifiedTime, t.CreationTime)
		},
		extra: func(t *driver.UserProfile, f map[string]string) bool {
			return (domainID == "" || t.DomainID == domainID) && strings.Contains(t.UserProfileName, f["UserProfileNameContains"])
		},
	}, opts)
}

func (m *Mock) DeleteUserProfile(_ context.Context, domainID, name string) error {
	key := scopedKey(domainID, name)
	if !m.userProfiles.Has(key) {
		return errors.Newf(errors.NotFound, "user profile %q not found", name)
	}

	m.userProfiles.Delete(key)
	m.dropTags(m.arn("user-profile/" + domainID + "/" + name))

	return nil
}

// --- Space ---

func (m *Mock) CreateSpace(_ context.Context, cfg driver.SpaceSpec) (*driver.Space, error) {
	if err := validateTags(cfg.Tags); err != nil {
		return nil, err
	}

	if cfg.DomainID == "" || cfg.SpaceName == "" {
		return nil, errors.New(errors.InvalidArgument, "domainId and spaceName are required")
	}

	if !m.domains.Has(cfg.DomainID) {
		return nil, errors.Newf(errors.InvalidArgument, "domain %q not found", cfg.DomainID)
	}

	key := scopedKey(cfg.DomainID, cfg.SpaceName)
	arn := m.arn("space/" + cfg.DomainID + "/" + cfg.SpaceName)
	now := m.now()
	sp := &driver.Space{
		DomainID:         cfg.DomainID,
		SpaceName:        cfg.SpaceName,
		SpaceARN:         arn,
		Status:           driver.StudioInService,
		CreationTime:     now,
		LastModifiedTime: now,
		Tags:             copyTags(cfg.Tags),
	}

	if !m.spaces.SetIfAbsent(key, sp) {
		return nil, driver.InUse("space %q already exists", cfg.SpaceName)
	}
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

//nolint:dupl,gocritic // per-resource shim mirrors the SDK shape; large value params mirror the driver interface
func (m *Mock) ListSpaces(_ context.Context, domainID string, opts driver.ListOptions) (driver.ListPage[driver.Space], error) {
	return listFrom(m.spaces, listSpec[driver.Space]{
		name:    func(t *driver.Space) string { return t.SpaceName },
		status:  func(t *driver.Space) string { return t.Status },
		created: func(t *driver.Space) string { return t.CreationTime },
		modified: func(t *driver.Space) string {
			return modifiedOrCreated(t.LastModifiedTime, t.CreationTime)
		},
		extra: func(t *driver.Space, f map[string]string) bool {
			return (domainID == "" || t.DomainID == domainID) && strings.Contains(t.SpaceName, f["SpaceNameContains"])
		},
	}, opts)
}

func (m *Mock) DeleteSpace(_ context.Context, domainID, name string) error {
	key := scopedKey(domainID, name)
	if !m.spaces.Has(key) {
		return errors.Newf(errors.NotFound, "space %q not found", name)
	}

	m.spaces.Delete(key)
	m.dropTags(m.arn("space/" + domainID + "/" + name))

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

	if !m.apps.SetIfAbsent(key, app) {
		return nil, driver.InUse("app %q already exists", in.AppName)
	}

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

//nolint:gocritic // large value params mirror the driver interface
func (m *Mock) ListApps(_ context.Context, domainID string, opts driver.ListOptions) (driver.ListPage[driver.App], error) {
	return listFrom(m.apps, listSpec[driver.App]{
		name:    func(t *driver.App) string { return t.AppName },
		status:  func(t *driver.App) string { return t.Status },
		created: func(t *driver.App) string { return t.CreationTime },
		extra: func(t *driver.App, f map[string]string) bool {
			return (domainID == "" || t.DomainID == domainID) && equalsOrUnset(f["UserProfileNameEquals"], t.UserProfileName) &&
				equalsOrUnset(f["SpaceNameEquals"], t.SpaceName)
		},
	}, opts)
}

//nolint:gocritic // in matches the driver signature; copied on entry.
func (m *Mock) DeleteApp(_ context.Context, in driver.AppSpec) error {
	key := appKey(&in)

	app, ok := m.apps.Get(key)
	if !ok {
		return errors.Newf(errors.NotFound, "app %q not found", in.AppName)
	}

	m.apps.Delete(key)
	m.dropTags(app.AppARN)

	return nil
}

// domainHasChildren reports whether a user profile, space or app still lives in
// the domain (SageMaker refuses to delete such a domain).
func (m *Mock) domainHasChildren(domainID string) bool {
	for _, u := range m.userProfiles.All() {
		if u.DomainID == domainID {
			return true
		}
	}

	for _, sp := range m.spaces.All() {
		if sp.DomainID == domainID {
			return true
		}
	}

	for _, a := range m.apps.All() {
		if a.DomainID == domainID {
			return true
		}
	}

	return false
}

// equalsOrUnset reports whether a filter is unset or equal to the value.
func equalsOrUnset(filter, value string) bool {
	return filter == "" || filter == value
}
