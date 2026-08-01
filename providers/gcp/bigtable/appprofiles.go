package bigtable

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	btdriver "github.com/stackshy/cloudemu/v2/services/bigtable/driver"
)

func cloneAppProfile(in *btdriver.AppProfile) btdriver.AppProfile {
	a := *in
	a.MultiClusterClusterIDs = append([]string(nil), in.MultiClusterClusterIDs...)

	return a
}

func appProfileFrom(cfg *btdriver.CreateAppProfileConfig, name string) btdriver.AppProfile {
	return btdriver.AppProfile{
		Name:                     name,
		Description:              cfg.Description,
		MultiClusterRoutingAny:   cfg.MultiClusterRoutingAny,
		MultiClusterClusterIDs:   append([]string(nil), cfg.MultiClusterClusterIDs...),
		SingleClusterID:          cfg.SingleClusterID,
		AllowTransactionalWrites: cfg.AllowTransactionalWrites,
		Priority:                 orDefault(cfg.Priority, "PRIORITY_HIGH"),
		Etag:                     "ACAB",
	}
}

// CreateAppProfile creates an app profile in an instance.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateAppProfile(_ context.Context, cfg btdriver.CreateAppProfileConfig) (*btdriver.AppProfile, error) {
	if cfg.AppProfileID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "appProfileId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.instances.Has(cfg.Parent) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "instance %q not found", cfg.Parent)
	}

	name := cfg.Parent + "/appProfiles/" + cfg.AppProfileID
	if m.appProfiles.Has(name) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "app profile %q already exists", name)
	}

	a := appProfileFrom(&cfg, name)
	m.appProfiles.Set(name, a)

	out := cloneAppProfile(&a)

	return &out, nil
}

// GetAppProfile returns an app profile by full name.
func (m *Mock) GetAppProfile(_ context.Context, name string) (*btdriver.AppProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	a, ok := m.appProfiles.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "app profile %q not found", name)
	}

	out := cloneAppProfile(&a)

	return &out, nil
}

// ListAppProfiles returns the app profiles of an instance.
func (m *Mock) ListAppProfiles(_ context.Context, instance string) ([]btdriver.AppProfile, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := instance + "/appProfiles/"
	all := m.appProfiles.SortedValues()
	out := make([]btdriver.AppProfile, 0, len(all))

	for i := range all {
		if strings.HasPrefix(all[i].Name, prefix) {
			out = append(out, cloneAppProfile(&all[i]))
		}
	}

	return out, nil
}

// UpdateAppProfile replaces an app profile's routing/config (LRO).
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) UpdateAppProfile(
	_ context.Context, name string, cfg btdriver.CreateAppProfileConfig,
) (*btdriver.AppProfile, *btdriver.Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.appProfiles.Has(name) {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "app profile %q not found", name)
	}

	a := appProfileFrom(&cfg, name)
	m.appProfiles.Set(name, a)

	op := m.newOp("update-appprofile", name)
	out := cloneAppProfile(&a)

	return &out, op, nil
}

// DeleteAppProfile removes an app profile.
func (m *Mock) DeleteAppProfile(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.appProfiles.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "app profile %q not found", name)
	}

	m.appProfiles.Delete(name)

	return nil
}
