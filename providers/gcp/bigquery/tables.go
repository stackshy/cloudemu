package bigquery

import (
	"context"
	"sort"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// tableTypeTable / tableTypeView are the table.type enum values the control
// plane assigns: a base TABLE, or a logical VIEW when the request carries a
// view definition.
const (
	tableTypeTable = "TABLE"
	tableTypeView  = "VIEW"
)

// InsertTable creates a table under project/datasetID.
func (m *Mock) InsertTable(_ context.Context, project, datasetID string, tbl *driver.Table) (*driver.Table, error) {
	if tbl == nil || tbl.TableID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "tableReference.tableId is required")
	}

	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	now := m.now()
	stored := cloneTable(tbl)
	stored.ProjectID = project
	stored.DatasetID = datasetID
	stored.Type = tableType(tbl)
	stored.Etag = newEtag()
	stored.CreationTime = now
	stored.LastModifiedTime = now

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if _, exists := entry.tables[tbl.TableID]; exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %s:%s.%s already exists", project, datasetID, tbl.TableID)
	}

	entry.tables[tbl.TableID] = stored

	return cloneTable(stored), nil
}

// GetTable returns the table, or NotFound.
func (m *Mock) GetTable(_ context.Context, project, datasetID, tableID string) (*driver.Table, error) {
	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	tbl, ok := entry.tables[tableID]
	if !ok {
		return nil, notFoundTable(project, datasetID, tableID)
	}

	return cloneTable(tbl), nil
}

// ListTables returns every table in the dataset ordered by tableId.
func (m *Mock) ListTables(_ context.Context, project, datasetID string) ([]*driver.Table, error) {
	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	out := make([]*driver.Table, 0, len(entry.tables))
	for _, tbl := range entry.tables {
		out = append(out, cloneTable(tbl))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].TableID < out[j].TableID })

	return out, nil
}

// PatchTable merges the supplied fields into the table.
func (m *Mock) PatchTable(
	_ context.Context, project, datasetID, tableID string, patch *driver.TablePatch,
) (*driver.Table, error) {
	return m.applyTable(project, datasetID, tableID, patch, false)
}

// UpdateTable replaces the table's mutable fields.
func (m *Mock) UpdateTable(
	_ context.Context, project, datasetID, tableID string, patch *driver.TablePatch,
) (*driver.Table, error) {
	return m.applyTable(project, datasetID, tableID, patch, true)
}

// applyTable applies a patch (merge) or update (replace) to a table.
func (m *Mock) applyTable(
	project, datasetID, tableID string, patch *driver.TablePatch, replace bool,
) (*driver.Table, error) {
	if patch == nil {
		patch = &driver.TablePatch{}
	}

	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	tbl, ok := entry.tables[tableID]
	if !ok {
		return nil, notFoundTable(project, datasetID, tableID)
	}

	if patch.Etag != "" && patch.Etag != tbl.Etag {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "etag mismatch for table %s:%s.%s", project, datasetID, tableID)
	}

	applyTableFields(tbl, patch, replace)
	tbl.Type = tableType(tbl)
	tbl.Etag = newEtag()
	tbl.LastModifiedTime = m.now()

	return cloneTable(tbl), nil
}

// applyTableFields mutates tbl per patch. In replace mode an omitted field is
// cleared; in merge mode it is left intact.
func applyTableFields(tbl *driver.Table, patch *driver.TablePatch, replace bool) {
	if patch.FriendlyName != nil {
		tbl.FriendlyName = *patch.FriendlyName
	} else if replace {
		tbl.FriendlyName = ""
	}

	if patch.Description != nil {
		tbl.Description = *patch.Description
	} else if replace {
		tbl.Description = ""
	}

	if patch.SchemaSet {
		tbl.Schema = cloneFields(patch.Schema)
	} else if replace {
		tbl.Schema = nil
	}

	tbl.Labels = mergeLabels(tbl.Labels, patch.Labels, patch.LabelsSet, replace)

	applyTableSubResources(tbl, patch, replace)
}

// applyTableSubResources applies the partitioning, clustering, view, and
// expiration fields of a table patch.
func applyTableSubResources(tbl *driver.Table, patch *driver.TablePatch, replace bool) {
	if patch.TimePartitioning != nil {
		tp := *patch.TimePartitioning
		tbl.TimePartitioning = &tp
	} else if replace {
		tbl.TimePartitioning = nil
	}

	if patch.ClusteringSet {
		tbl.Clustering = cloneStrings(patch.Clustering)
	} else if replace {
		tbl.Clustering = nil
	}

	if patch.View != nil {
		v := *patch.View
		tbl.View = &v
	} else if replace {
		tbl.View = nil
	}

	if patch.ExpirationTime != nil {
		tbl.ExpirationTime = *patch.ExpirationTime
	} else if replace {
		tbl.ExpirationTime = time.Time{}
	}
}

// tableType resolves a table's type: an explicit non-empty type wins, else a
// view definition implies VIEW, otherwise TABLE.
func tableType(tbl *driver.Table) string {
	if tbl.Type != "" {
		return tbl.Type
	}

	if tbl.View != nil {
		return tableTypeView
	}

	return tableTypeTable
}

// DeleteTable removes the table, or NotFound.
func (m *Mock) DeleteTable(_ context.Context, project, datasetID, tableID string) error {
	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return notFoundDataset(project, datasetID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if _, ok := entry.tables[tableID]; !ok {
		return notFoundTable(project, datasetID, tableID)
	}

	delete(entry.tables, tableID)

	return nil
}

func notFoundTable(project, datasetID, tableID string) error {
	return cerrors.Newf(cerrors.NotFound, "Not found: Table %s:%s.%s", project, datasetID, tableID)
}
