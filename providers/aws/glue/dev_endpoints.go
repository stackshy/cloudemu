package glue

import (
	"context"
	"sync"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// devEndpointData is a dev endpoint plus its own lock.
type devEndpointData struct {
	endpoint driver.DevEndpoint
	mu       sync.RWMutex
}

// CreateDevEndpoint creates a dev endpoint in the READY state, atomically. The
// emulator has no real notebook host, so the endpoint is immediately READY.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface / copy semantics
func (m *Mock) CreateDevEndpoint(_ context.Context, e driver.DevEndpoint) (*driver.DevEndpoint, error) {
	if !validName(e.EndpointName) {
		return nil, invalidInput("dev endpoint name %q is invalid", e.EndpointName)
	}

	now := m.now()
	e.Status = "READY"
	e.CreatedTimestamp = now
	e.LastModifiedTimestamp = now
	stored := copyDevEndpoint(e)

	if !m.devEndpoints.SetIfAbsent(e.EndpointName, &devEndpointData{endpoint: stored}) {
		return nil, alreadyExists("DevEndpoint already exists: %s", e.EndpointName)
	}

	out := copyDevEndpoint(stored)

	return &out, nil
}

func (m *Mock) getDevEndpointData(name string) (*devEndpointData, error) {
	if !validName(name) {
		return nil, invalidInput("dev endpoint name %q is invalid", name)
	}

	ed, ok := m.devEndpoints.Get(name)
	if !ok {
		return nil, entityNotFound("DevEndpoint not found: %s", name)
	}

	return ed, nil
}

// GetDevEndpoint returns a copy of a dev endpoint.
func (m *Mock) GetDevEndpoint(_ context.Context, name string) (*driver.DevEndpoint, error) {
	ed, err := m.getDevEndpointData(name)
	if err != nil {
		return nil, err
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	out := copyDevEndpoint(ed.endpoint)

	return &out, nil
}

// UpdateDevEndpoint updates a dev endpoint's arguments.
func (m *Mock) UpdateDevEndpoint(_ context.Context, name string, args map[string]string) error {
	ed, err := m.getDevEndpointData(name)
	if err != nil {
		return err
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	ed.endpoint.Arguments = copyTags(args)
	ed.endpoint.LastModifiedTimestamp = m.now()

	return nil
}

// DeleteDevEndpoint removes a dev endpoint.
func (m *Mock) DeleteDevEndpoint(_ context.Context, name string) error {
	if _, err := m.getDevEndpointData(name); err != nil {
		return err
	}

	m.devEndpoints.Delete(name)

	return nil
}

// GetDevEndpoints lists dev endpoints with pagination.
//
//nolint:dupl // near-identical list/batch body per resource; separate is clearer than reflection
func (m *Mock) GetDevEndpoints(
	_ context.Context, page driver.TablePagination,
) ([]driver.DevEndpoint, string, error) {
	keys := sortedKeys(m.devEndpoints.Keys())
	all := make([]driver.DevEndpoint, 0, len(keys))

	for _, key := range keys {
		ed, ok := m.devEndpoints.Get(key)
		if !ok {
			continue
		}

		ed.mu.RLock()
		all = append(all, copyDevEndpoint(ed.endpoint))
		ed.mu.RUnlock()
	}

	return paginate(all, page)
}

// ListDevEndpoints returns dev endpoint names with pagination.
//
//nolint:gocritic // unnamedResult: thin pass-through to paginate; names add no clarity
func (m *Mock) ListDevEndpoints(_ context.Context, page driver.TablePagination) ([]string, string, error) {
	return paginate(sortedKeys(m.devEndpoints.Keys()), page)
}

// BatchGetDevEndpoints returns the found endpoints and the missing names.
//
//nolint:dupl // near-identical CRUD/batch bodies per resource; separate is clearer than reflection
func (m *Mock) BatchGetDevEndpoints(_ context.Context, names []string) ([]driver.DevEndpoint, []string, error) {
	if len(names) > maxBatchGet {
		return nil, nil, invalidInput("cannot request more than %d dev endpoints", maxBatchGet)
	}

	found := make([]driver.DevEndpoint, 0, len(names))

	var notFound []string

	for _, n := range names {
		e, err := m.GetDevEndpoint(context.Background(), n)
		if err != nil {
			notFound = append(notFound, n)

			continue
		}

		found = append(found, *e)
	}

	return found, notFound, nil
}
