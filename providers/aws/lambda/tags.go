package lambda

import (
	"context"
	"maps"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// TagFunction adds or overwrites tags on a function (Lambda TagResource).
func (m *Mock) TagFunction(_ context.Context, name string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	// Copy before editing: GetFunction and ListFunctionTags read the stored
	// map without mu.
	next := make(map[string]string, len(fd.info.Tags)+len(tags))
	maps.Copy(next, fd.info.Tags)
	maps.Copy(next, tags)
	fd.info.Tags = next

	m.funcs.Set(name, fd)

	return nil
}

// UntagFunction removes tags by key from a function (Lambda UntagResource).
func (m *Mock) UntagFunction(_ context.Context, name string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	next := maps.Clone(fd.info.Tags)
	for _, k := range keys {
		delete(next, k)
	}

	fd.info.Tags = next

	m.funcs.Set(name, fd)

	return nil
}

// ListFunctionTags returns a function's tags (Lambda ListTags).
func (m *Mock) ListFunctionTags(_ context.Context, name string) (map[string]string, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	return maps.Clone(fd.info.Tags), nil
}
