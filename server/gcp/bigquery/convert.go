package bigquery

import (
	"strconv"
	"time"

	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// millisOrZero returns t as epoch-millis for a set time, or 0 (which omitempty
// drops) for the zero time — so an unset expiry is omitted, not rendered as a
// year-1 timestamp.
func millisOrZero(t time.Time) int64Wire {
	if t.IsZero() {
		return 0
	}

	return int64Wire(t.UnixMilli())
}

// millisToTime converts epoch-millis to a UTC time, mapping 0 to the zero time.
func millisToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// datasetID builds the "{project}:{dataset}" id BigQuery stamps on a dataset.
func datasetIDStr(project, datasetID string) string {
	return project + ":" + datasetID
}

// tableIDStr builds the "{project}:{dataset}.{table}" id BigQuery stamps on a
// table (colon between project and dataset, dot before the table).
func tableIDStr(project, datasetID, tableID string) string {
	return project + ":" + datasetID + "." + tableID
}

// datasetToWire renders a dataset in the datasets resource wire shape.
func datasetToWire(host string, ds *driver.Dataset) wireDataset {
	out := wireDataset{
		Kind:     kindDataset,
		Etag:     ds.Etag,
		ID:       datasetIDStr(ds.ProjectID, ds.DatasetID),
		SelfLink: datasetSelfLink(host, ds.ProjectID, ds.DatasetID),
		DatasetReference: &wireDatasetRef{
			ProjectID: ds.ProjectID,
			DatasetID: ds.DatasetID,
		},
		FriendlyName:             ds.FriendlyName,
		Description:              ds.Description,
		Labels:                   ds.Labels,
		Access:                   accessToWire(ds.Access),
		Location:                 ds.Location,
		CreationTime:             epochMillis(ds.CreationTime),
		LastModifiedTime:         epochMillis(ds.LastModifiedTime),
		DefaultTableExpirationMs: int64Wire(ds.DefaultTableExpirationMs),
	}

	return out
}

// accessToWire renders a dataset access list.
func accessToWire(entries []driver.AccessEntry) []wireAccess {
	if entries == nil {
		return nil
	}

	out := make([]wireAccess, len(entries))
	for i := range entries {
		out[i] = accessEntryToWire(&entries[i])
	}

	return out
}

func accessEntryToWire(e *driver.AccessEntry) wireAccess {
	out := wireAccess{
		Role:         e.Role,
		UserByEmail:  e.UserByEmail,
		GroupByEmail: e.GroupByEmail,
		SpecialGroup: e.SpecialGroup,
		Domain:       e.Domain,
		IamMember:    e.IamMember,
	}

	if e.View != nil {
		out.View = &wireTableRef{ProjectID: e.View.ProjectID, DatasetID: e.View.DatasetID, TableID: e.View.TableID}
	}

	if e.Routine != nil {
		out.Routine = &wireRoutineRef{
			ProjectID: e.Routine.ProjectID, DatasetID: e.Routine.DatasetID, RoutineID: e.Routine.RoutineID,
		}
	}

	if e.Dataset != nil {
		da := &wireDatasetAccess{TargetTypes: e.Dataset.TargetTypes}
		if e.Dataset.Dataset != nil {
			da.Dataset = &wireDatasetRef{ProjectID: e.Dataset.Dataset.ProjectID, DatasetID: e.Dataset.Dataset.DatasetID}
		}

		out.Dataset = da
	}

	return out
}

// accessFromWire converts a wire access list to driver form.
func accessFromWire(entries []wireAccess) []driver.AccessEntry {
	if entries == nil {
		return nil
	}

	out := make([]driver.AccessEntry, len(entries))
	for i := range entries {
		out[i] = accessEntryFromWire(&entries[i])
	}

	return out
}

func accessEntryFromWire(e *wireAccess) driver.AccessEntry {
	out := driver.AccessEntry{
		Role:         e.Role,
		UserByEmail:  e.UserByEmail,
		GroupByEmail: e.GroupByEmail,
		SpecialGroup: e.SpecialGroup,
		Domain:       e.Domain,
		IamMember:    e.IamMember,
	}

	if e.View != nil {
		out.View = &driver.TableReference{ProjectID: e.View.ProjectID, DatasetID: e.View.DatasetID, TableID: e.View.TableID}
	}

	if e.Routine != nil {
		out.Routine = &driver.RoutineReference{
			ProjectID: e.Routine.ProjectID, DatasetID: e.Routine.DatasetID, RoutineID: e.Routine.RoutineID,
		}
	}

	if e.Dataset != nil {
		da := &driver.DatasetAccessEntry{TargetTypes: e.Dataset.TargetTypes}
		if e.Dataset.Dataset != nil {
			da.Dataset = &driver.DatasetReference{
				ProjectID: e.Dataset.Dataset.ProjectID, DatasetID: e.Dataset.Dataset.DatasetID,
			}
		}

		out.Dataset = da
	}

	return out
}

// fieldsToWire renders a schema field tree, echoing mode NULLABLE on any field
// that stored no mode so the default round-trips.
func fieldsToWire(fields []driver.Field) []wireField {
	if fields == nil {
		return nil
	}

	out := make([]wireField, len(fields))

	for i := range fields {
		mode := fields[i].Mode
		if mode == "" {
			mode = modeNullable
		}

		out[i] = wireField{
			Name:        fields[i].Name,
			Type:        fields[i].Type,
			Mode:        mode,
			Description: fields[i].Description,
			Fields:      fieldsToWire(fields[i].Fields),
		}
	}

	return out
}

// fieldsFromWire converts a wire schema field tree to driver form, preserving
// an explicit REQUIRED/REPEATED mode and defaulting an omitted mode to NULLABLE.
func fieldsFromWire(fields []wireField) []driver.Field {
	if fields == nil {
		return nil
	}

	out := make([]driver.Field, len(fields))

	for i := range fields {
		mode := fields[i].Mode
		if mode == "" {
			mode = modeNullable
		}

		out[i] = driver.Field{
			Name:        fields[i].Name,
			Type:        fields[i].Type,
			Mode:        mode,
			Description: fields[i].Description,
			Fields:      fieldsFromWire(fields[i].Fields),
		}
	}

	return out
}

// tableToWire renders a table in the tables resource wire shape.
func tableToWire(host string, t *driver.Table) wireTable {
	out := wireTable{
		Kind:     kindTable,
		Etag:     t.Etag,
		ID:       tableIDStr(t.ProjectID, t.DatasetID, t.TableID),
		SelfLink: tableSelfLink(host, t.ProjectID, t.DatasetID, t.TableID),
		TableReference: &wireTableRef{
			ProjectID: t.ProjectID, DatasetID: t.DatasetID, TableID: t.TableID,
		},
		FriendlyName:     t.FriendlyName,
		Description:      t.Description,
		Type:             t.Type,
		Labels:           t.Labels,
		NumRows:          strconv.FormatInt(t.NumRows, 10),
		NumBytes:         strconv.FormatInt(t.NumBytes, 10),
		CreationTime:     epochMillis(t.CreationTime),
		LastModifiedTime: epochMillis(t.LastModifiedTime),
		ExpirationTime:   millisOrZero(t.ExpirationTime),
	}

	if len(t.Schema) > 0 {
		out.Schema = &wireSchema{Fields: fieldsToWire(t.Schema)}
	}

	if t.TimePartitioning != nil {
		out.TimePartitioning = timePartitioningToWire(t.TimePartitioning)
	}

	if len(t.Clustering) > 0 {
		out.Clustering = &wireClustering{Fields: t.Clustering}
	}

	if t.View != nil {
		useLegacy := t.View.UseLegacySQL
		out.View = &wireView{Query: t.View.Query, UseLegacySQL: &useLegacy}
	}

	return out
}

func timePartitioningToWire(tp *driver.TimePartitioning) *wireTimePartitioning {
	return &wireTimePartitioning{
		Type:         tp.Type,
		Field:        tp.Field,
		ExpirationMs: int64Wire(tp.ExpirationMs),
	}
}
