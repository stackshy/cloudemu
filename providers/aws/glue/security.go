package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// secConfigData is a security configuration plus its own lock.
type secConfigData struct {
	sc driver.SecurityConfiguration
	mu sync.RWMutex
}

// CreateSecurityConfiguration creates a security configuration, atomically.
func (m *Mock) CreateSecurityConfiguration(_ context.Context, sc driver.SecurityConfiguration) error {
	if !validName(sc.Name) {
		return invalidInput("security configuration name %q is invalid", sc.Name)
	}

	sc.CreatedTimeStamp = m.now()
	stored := copySecConfig(sc)

	if !m.secConfigs.SetIfAbsent(sc.Name, &secConfigData{sc: stored}) {
		return alreadyExists("SecurityConfiguration already exists: %s", sc.Name)
	}

	return nil
}

// GetSecurityConfiguration returns a copy of a security configuration.
func (m *Mock) GetSecurityConfiguration(_ context.Context, name string) (*driver.SecurityConfiguration, error) {
	if !validName(name) {
		return nil, invalidInput("security configuration name %q is invalid", name)
	}

	sd, ok := m.secConfigs.Get(name)
	if !ok {
		return nil, entityNotFound("SecurityConfiguration not found: %s", name)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := copySecConfig(sd.sc)

	return &out, nil
}

// DeleteSecurityConfiguration removes a security configuration.
func (m *Mock) DeleteSecurityConfiguration(_ context.Context, name string) error {
	if !m.secConfigs.Delete(name) {
		return entityNotFound("SecurityConfiguration not found: %s", name)
	}

	return nil
}

// GetSecurityConfigurations lists security configurations with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetSecurityConfigurations(
	_ context.Context, page driver.TablePagination,
) ([]driver.SecurityConfiguration, string, error) {
	keys := sortedKeys(m.secConfigs.Keys())
	all := make([]driver.SecurityConfiguration, 0, len(keys))

	for _, key := range keys {
		sd, ok := m.secConfigs.Get(key)
		if !ok {
			continue
		}

		sd.mu.RLock()
		all = append(all, copySecConfig(sd.sc))
		sd.mu.RUnlock()
	}

	return paginate(all, page)
}
