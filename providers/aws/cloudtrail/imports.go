package cloudtrail

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// StartImport records an import job. There is no real S3 source to read, so the
// job is created COMPLETED (documented) — the local-dev analog of an
// instantaneous ingest with no failures.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) StartImport(_ context.Context, in driver.Import) (*driver.Import, error) {
	now := m.now()

	// Resuming an existing import: return it unchanged.
	if in.ID != "" {
		imp, ok := m.imports.Get(in.ID)
		if !ok {
			return nil, errImportNotFound(in.ID)
		}

		return cloneImport(imp), nil
	}

	in.ID = idgen.GenerateID("import-")
	in.Status = driver.ImportStatusCompleted
	in.CreatedAt = now
	in.UpdatedAt = now

	m.imports.Set(in.ID, &in)

	out := in

	return &out, nil
}

// GetImport returns an import job by ID.
func (m *Mock) GetImport(_ context.Context, id string) (*driver.Import, error) {
	imp, ok := m.imports.Get(id)
	if !ok {
		return nil, errImportNotFound(id)
	}

	return cloneImport(imp), nil
}

// StopImport marks an import STOPPED.
func (m *Mock) StopImport(_ context.Context, id string) (*driver.Import, error) {
	imp, ok := m.imports.Get(id)
	if !ok {
		return nil, errImportNotFound(id)
	}

	imp.Status = driver.ImportStatusStopped
	imp.UpdatedAt = m.now()

	return cloneImport(imp), nil
}

// ListImports returns imports ordered by ID, optionally filtered by status,
// paginated.
func (m *Mock) ListImports(
	_ context.Context, _, importStatus, nextToken string, maxResults int32,
) ([]driver.Import, string, error) {
	all := m.imports.All()

	ids := make([]string, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]driver.Import, 0, len(ids))
	started := nextToken == ""

	for _, id := range ids {
		if !started {
			if id == nextToken {
				started = true
			}

			continue
		}

		imp := all[id]
		if importStatus != "" && imp.Status != importStatus {
			continue
		}

		if len(out) == limit {
			return out, out[len(out)-1].ID, nil
		}

		out = append(out, *imp)
	}

	return out, "", nil
}

// ListImportFailures returns an import's failures. Emulated imports complete
// with no failures, so this is always empty for a known import.
func (m *Mock) ListImportFailures(
	_ context.Context, id, _ string, _ int32,
) ([]driver.ImportFailure, string, error) {
	if _, ok := m.imports.Get(id); !ok {
		return nil, "", errImportNotFound(id)
	}

	return []driver.ImportFailure{}, "", nil
}

func cloneImport(in *driver.Import) *driver.Import {
	out := *in
	out.Destinations = append([]string(nil), in.Destinations...)

	return &out
}
