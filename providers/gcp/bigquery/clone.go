package bigquery

import "github.com/stackshy/cloudemu/v2/services/bigquery/driver"

// cloneStringMap returns a shallow copy of a string map, or nil for nil/empty.
func cloneStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}

	return out
}

// cloneStrings copies a string slice, preserving nil.
func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}

	out := make([]string, len(s))
	copy(out, s)

	return out
}

// cloneFields deep-copies a schema field tree (recursive for RECORD/STRUCT).
func cloneFields(fields []driver.Field) []driver.Field {
	if fields == nil {
		return nil
	}

	out := make([]driver.Field, len(fields))
	for i := range fields {
		out[i] = fields[i]
		out[i].Fields = cloneFields(fields[i].Fields)
	}

	return out
}

// cloneAccess deep-copies a dataset access list.
func cloneAccess(entries []driver.AccessEntry) []driver.AccessEntry {
	if entries == nil {
		return nil
	}

	out := make([]driver.AccessEntry, len(entries))
	for i := range entries {
		out[i] = cloneAccessEntry(&entries[i])
	}

	return out
}

func cloneAccessEntry(e *driver.AccessEntry) driver.AccessEntry {
	out := *e

	if e.View != nil {
		v := *e.View
		out.View = &v
	}

	if e.Routine != nil {
		r := *e.Routine
		out.Routine = &r
	}

	if e.Dataset != nil {
		d := driver.DatasetAccessEntry{TargetTypes: cloneStrings(e.Dataset.TargetTypes)}

		if e.Dataset.Dataset != nil {
			ref := *e.Dataset.Dataset
			d.Dataset = &ref
		}

		out.Dataset = &d
	}

	return out
}

// cloneDataset returns a deep copy of a dataset.
func cloneDataset(ds *driver.Dataset) *driver.Dataset {
	if ds == nil {
		return nil
	}

	out := *ds
	out.Labels = cloneStringMap(ds.Labels)
	out.Access = cloneAccess(ds.Access)

	return &out
}

// cloneTable returns a deep copy of a table.
func cloneTable(tbl *driver.Table) *driver.Table {
	if tbl == nil {
		return nil
	}

	out := *tbl
	out.Labels = cloneStringMap(tbl.Labels)
	out.Schema = cloneFields(tbl.Schema)
	out.Clustering = cloneStrings(tbl.Clustering)

	if tbl.TimePartitioning != nil {
		tp := *tbl.TimePartitioning
		out.TimePartitioning = &tp
	}

	if tbl.View != nil {
		v := *tbl.View
		out.View = &v
	}

	return &out
}
