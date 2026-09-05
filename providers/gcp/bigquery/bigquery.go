// Package bigquery provides an in-memory mock of the GCP BigQuery metadata
// control plane (datasets and tables). It implements the bigquery driver so a
// real google.golang.org/api/bigquery/v2 client, gcloud, or Terraform's
// google_bigquery_dataset / google_bigquery_table resources drive it end to
// end through the GCP wire handler.
package bigquery

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// defaultLocation is the BigQuery dataset location used when a create request
// omits one, matching real BigQuery's default multi-region.
const defaultLocation = "US"

// Compile-time check that Mock implements the BigQuery driver.
var _ driver.BigQuery = (*Mock)(nil)

// datasetEntry is a stored dataset together with its tables. The mutex guards
// both info and tables so a dataset patch and a concurrent table insert stay
// consistent.
type datasetEntry struct {
	info   driver.Dataset
	tables map[string]*driver.Table
	mu     sync.RWMutex
}

// Mock is the in-memory BigQuery metadata backend.
type Mock struct {
	datasets *memstore.Store[*datasetEntry]
	opts     *config.Options
}

// New returns an empty BigQuery mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		datasets: memstore.New[*datasetEntry](),
		opts:     opts,
	}
}

// now returns the provider clock's current time (real clock when unset).
func (m *Mock) now() time.Time {
	if m.opts != nil && m.opts.Clock != nil {
		return m.opts.Clock.Now().UTC()
	}

	return time.Now().UTC()
}

// newEtag returns a fresh opaque optimistic-concurrency tag.
func newEtag() string {
	return idgen.GenerateID("etag-")
}

// dsKey scopes a dataset by project so two projects can share a dataset id.
func dsKey(project, datasetID string) string {
	return project + "\x00" + datasetID
}

// InsertDataset creates a dataset under project.
func (m *Mock) InsertDataset(_ context.Context, project string, ds *driver.Dataset) (*driver.Dataset, error) {
	if ds == nil || ds.DatasetID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "datasetReference.datasetId is required")
	}

	now := m.now()
	stored := cloneDataset(ds)
	stored.ProjectID = project
	stored.Location = firstNonEmpty(ds.Location, defaultLocation)
	stored.Etag = newEtag()
	stored.CreationTime = now
	stored.LastModifiedTime = now

	entry := &datasetEntry{info: *stored, tables: map[string]*driver.Table{}}

	if !m.datasets.SetIfAbsent(dsKey(project, ds.DatasetID), entry) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "dataset %s:%s already exists", project, ds.DatasetID)
	}

	return cloneDataset(stored), nil
}

// GetDataset returns the dataset, or NotFound.
func (m *Mock) GetDataset(_ context.Context, project, datasetID string) (*driver.Dataset, error) {
	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	entry.mu.RLock()
	defer entry.mu.RUnlock()

	return cloneDataset(&entry.info), nil
}

// ListDatasets returns every dataset in project ordered by datasetId.
func (m *Mock) ListDatasets(_ context.Context, project string) ([]*driver.Dataset, error) {
	var out []*driver.Dataset

	for _, entry := range m.datasets.All() {
		entry.mu.RLock()

		if entry.info.ProjectID == project {
			out = append(out, cloneDataset(&entry.info))
		}

		entry.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].DatasetID < out[j].DatasetID })

	return out, nil
}

// PatchDataset merges the supplied fields into the dataset.
func (m *Mock) PatchDataset(
	_ context.Context, project, datasetID string, patch *driver.DatasetPatch,
) (*driver.Dataset, error) {
	return m.applyDataset(project, datasetID, patch, false)
}

// UpdateDataset replaces the dataset's mutable fields.
func (m *Mock) UpdateDataset(
	_ context.Context, project, datasetID string, patch *driver.DatasetPatch,
) (*driver.Dataset, error) {
	return m.applyDataset(project, datasetID, patch, true)
}

// applyDataset applies a patch (merge) or update (replace) to a dataset.
func (m *Mock) applyDataset(
	project, datasetID string, patch *driver.DatasetPatch, replace bool,
) (*driver.Dataset, error) {
	if patch == nil {
		patch = &driver.DatasetPatch{}
	}

	entry, ok := m.datasets.Get(dsKey(project, datasetID))
	if !ok {
		return nil, notFoundDataset(project, datasetID)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if patch.Etag != "" && patch.Etag != entry.info.Etag {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "etag mismatch for dataset %s:%s", project, datasetID)
	}

	applyDatasetFields(&entry.info, patch, replace)
	entry.info.Etag = newEtag()
	entry.info.LastModifiedTime = m.now()

	return cloneDataset(&entry.info), nil
}

// DeleteDataset removes the dataset, failing when non-empty unless deleteContents.
func (m *Mock) DeleteDataset(_ context.Context, project, datasetID string, deleteContents bool) error {
	key := dsKey(project, datasetID)

	entry, ok := m.datasets.Get(key)
	if !ok {
		return notFoundDataset(project, datasetID)
	}

	entry.mu.RLock()
	nonEmpty := len(entry.tables) > 0
	entry.mu.RUnlock()

	if nonEmpty && !deleteContents {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"dataset %s:%s is still in use; set deleteContents to delete it with its tables", project, datasetID)
	}

	m.datasets.Delete(key)

	return nil
}

// applyDatasetFields mutates info per patch. In replace mode an omitted
// (nil/unset) field is cleared; in merge mode an omitted field is left intact.
func applyDatasetFields(info *driver.Dataset, patch *driver.DatasetPatch, replace bool) {
	applyString(&info.FriendlyName, patch.FriendlyName, replace)
	applyString(&info.Description, patch.Description, replace)
	applyInt64(&info.DefaultTableExpirationMs, patch.DefaultTableExpirationMs, replace)

	// Location is immutable in BigQuery: honor an explicit value but never
	// clear it on a replace.
	if patch.Location != nil && *patch.Location != "" {
		info.Location = *patch.Location
	}

	info.Labels = mergeLabels(info.Labels, patch.Labels, patch.LabelsSet, replace)

	if patch.AccessSet || replace {
		info.Access = cloneAccess(patch.Access)
	}
}

// applyString sets *dst from src when supplied; on replace it clears an omitted
// value, on merge it leaves it intact.
func applyString(dst, src *string, replace bool) {
	switch {
	case src != nil:
		*dst = *src
	case replace:
		*dst = ""
	}
}

// applyInt64 is applyString for an int64 field.
func applyInt64(dst, src *int64, replace bool) {
	switch {
	case src != nil:
		*dst = *src
	case replace:
		*dst = 0
	}
}

// mergeLabels applies a labels patch: replace swaps the whole map; merge
// overlays supplied keys (a nil value deletes that key) onto the existing map.
func mergeLabels(existing, patch map[string]string, set, replace bool) map[string]string {
	if replace {
		return cloneStringMap(patch)
	}

	if !set {
		return existing
	}

	out := cloneStringMap(existing)
	if out == nil {
		out = map[string]string{}
	}

	for k, v := range patch {
		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func notFoundDataset(project, datasetID string) error {
	return cerrors.Newf(cerrors.NotFound, "Not found: Dataset %s:%s", project, datasetID)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
