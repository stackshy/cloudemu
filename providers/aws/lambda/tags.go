package lambda

import (
	"context"
	"maps"
)

// TagFunction adds or overwrites tags on a function (Lambda TagResource).
func (m *Mock) TagFunction(ctx context.Context, name string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return m.errFunctionNotFound(ctx, name, "")
	}

	// Real Lambda rejects TagResource while an update is InProgress.
	if err := m.checkNotUpdating(&fd); err != nil {
		return err
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
func (m *Mock) UntagFunction(ctx context.Context, name string, keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fd, ok := m.funcs.Get(name)
	if !ok {
		return m.errFunctionNotFound(ctx, name, "")
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
func (m *Mock) ListFunctionTags(ctx context.Context, name string) (map[string]string, error) {
	fd, ok := m.funcs.Get(name)
	if !ok {
		return nil, m.errFunctionNotFound(ctx, name, "")
	}

	return maps.Clone(fd.info.Tags), nil
}
