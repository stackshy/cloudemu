package bigquery

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	"github.com/stackshy/cloudemu/v2/services/bigquery/driver"
)

// maxBodyBytes caps an incoming request body. A table with a large schema is
// still well under 4 MiB.
const maxBodyBytes = 4 << 20

// readBody reads the (capped) request body, writing a 400 and returning
// ok=false on a read error.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return nil, false
	}

	return body, true
}

// decodeJSON unmarshals body into v, writing a 400 and returning false on error.
// An empty body decodes to the zero value (a PATCH may legitimately be empty).
func decodeJSON(w http.ResponseWriter, body []byte, v any) bool {
	if len(body) == 0 {
		return true
	}

	if err := json.Unmarshal(body, v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", err.Error())
		return false
	}

	return true
}

// presentKeys returns the set of top-level JSON keys in body, so patch/update
// can tell an omitted field from one sent empty.
func presentKeys(body []byte) map[string]bool {
	out := map[string]bool{}
	if len(body) == 0 {
		return out
	}

	var raw map[string]json.RawMessage
	if json.Unmarshal(body, &raw) != nil {
		return out
	}

	for k := range raw {
		out[k] = true
	}

	return out
}

// datasetPatchFromBody builds a DatasetPatch from a PATCH/PUT body, using the
// present-key set so an omitted field stays nil (untouched on merge, cleared on
// replace) while a supplied one is applied.
func datasetPatchFromBody(w http.ResponseWriter, body []byte) (*driver.DatasetPatch, bool) {
	var wd wireDataset
	if !decodeJSON(w, body, &wd) {
		return nil, false
	}

	keys := presentKeys(body)
	patch := &driver.DatasetPatch{Etag: wd.Etag, Labels: wd.Labels, LabelsSet: keys["labels"]}

	if keys["friendlyName"] {
		v := wd.FriendlyName
		patch.FriendlyName = &v
	}

	if keys["description"] {
		v := wd.Description
		patch.Description = &v
	}

	if keys["defaultTableExpirationMs"] {
		v := int64(wd.DefaultTableExpirationMs)
		patch.DefaultTableExpirationMs = &v
	}

	if keys["location"] {
		v := wd.Location
		patch.Location = &v
	}

	if keys["access"] {
		patch.Access = accessFromWire(wd.Access)
		patch.AccessSet = true
	}

	return patch, true
}

// datasetList is the datasets.list response envelope.
type datasetList struct {
	Kind     string             `json:"kind"`
	Etag     string             `json:"etag,omitempty"`
	Datasets []datasetListEntry `json:"datasets,omitempty"`
}

// datasetListEntry is one datasets.list item (a projection of the resource).
type datasetListEntry struct {
	Kind             string            `json:"kind"`
	ID               string            `json:"id"`
	DatasetReference *wireDatasetRef   `json:"datasetReference,omitempty"`
	FriendlyName     string            `json:"friendlyName,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Location         string            `json:"location,omitempty"`
}

// tableList is the tables.list response envelope.
type tableList struct {
	Kind   string           `json:"kind"`
	Etag   string           `json:"etag,omitempty"`
	Tables []tableListEntry `json:"tables,omitempty"`
	// TotalItems is the number of tables in the dataset.
	TotalItems int `json:"totalItems"`
}

// tableListEntry is one tables.list item (a projection of the resource).
type tableListEntry struct {
	Kind             string                `json:"kind"`
	ID               string                `json:"id"`
	TableReference   *wireTableRef         `json:"tableReference,omitempty"`
	FriendlyName     string                `json:"friendlyName,omitempty"`
	Labels           map[string]string     `json:"labels,omitempty"`
	Type             string                `json:"type,omitempty"`
	TimePartitioning *wireTimePartitioning `json:"timePartitioning,omitempty"`
	Clustering       *wireClustering       `json:"clustering,omitempty"`
	View             *wireView             `json:"view,omitempty"`
	CreationTime     string                `json:"creationTime,omitempty"`
	ExpirationTime   string                `json:"expirationTime,omitempty"`
}
