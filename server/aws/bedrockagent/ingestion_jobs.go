package bedrockagent

import (
	"net/http"

	badriver "github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

type ingestionStatisticsJSON struct {
	NumberOfDocumentsScanned          int64 `json:"numberOfDocumentsScanned"`
	NumberOfMetadataDocumentsScanned  int64 `json:"numberOfMetadataDocumentsScanned"`
	NumberOfNewDocumentsIndexed       int64 `json:"numberOfNewDocumentsIndexed"`
	NumberOfModifiedDocumentsIndexed  int64 `json:"numberOfModifiedDocumentsIndexed"`
	NumberOfMetadataDocumentsModified int64 `json:"numberOfMetadataDocumentsModified"`
	NumberOfDocumentsDeleted          int64 `json:"numberOfDocumentsDeleted"`
	NumberOfDocumentsFailed           int64 `json:"numberOfDocumentsFailed"`
}

type ingestionJobFilterJSON struct {
	Attribute string   `json:"attribute"`
	Operator  string   `json:"operator"`
	Values    []string `json:"values"`
}

type ingestionJobSortJSON struct {
	Attribute string `json:"attribute"`
	Order     string `json:"order"`
}

type listIngestionJobsRequest struct {
	listRequest

	Filters []ingestionJobFilterJSON `json:"filters"`
	SortBy  *ingestionJobSortJSON    `json:"sortBy"`
}

type listIngestionJobsResponse struct {
	IngestionJobSummaries []ingestionJobJSON `json:"ingestionJobSummaries"`
	NextToken             string             `json:"nextToken,omitempty"`
}

func listIngestionJobs(w http.ResponseWriter, r *http.Request, c badriver.IngestionJobs, kbID, dsID string) {
	var in listIngestionJobsRequest
	if !decodeBody(w, r, &in) {
		return
	}

	query := badriver.ListIngestionJobsInput{KnowledgeBaseID: kbID, DataSourceID: dsID, Page: in.page()}

	for _, f := range in.Filters {
		query.Filters = append(query.Filters, badriver.IngestionJobFilter{
			Attribute: f.Attribute, Operator: f.Operator, Values: f.Values,
		})
	}

	if in.SortBy != nil {
		query.SortBy = &badriver.IngestionJobSort{Attribute: in.SortBy.Attribute, Order: in.SortBy.Order}
	}

	jobs, next, err := c.ListIngestionJobs(r.Context(), query)
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]ingestionJobJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, toIngestionJobJSON(&jobs[i]))
	}

	writeJSON(w, listIngestionJobsResponse{IngestionJobSummaries: out, NextToken: next})
}

// writeIngestionJob returns a writer for a (job, err) result with the
// operation's success status.
func writeIngestionJob(w http.ResponseWriter, status int) func(*badriver.IngestionJob, error) {
	return func(job *badriver.IngestionJob, err error) {
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSONStatus(w, status, ingestionJobEnvelope{IngestionJob: toIngestionJobJSON(job)})
	}
}

func toStatisticsJSON(s badriver.IngestionJobStatistics) *ingestionStatisticsJSON {
	return &ingestionStatisticsJSON{
		NumberOfDocumentsScanned:          s.NumberOfDocumentsScanned,
		NumberOfMetadataDocumentsScanned:  s.NumberOfMetadataDocumentsScanned,
		NumberOfNewDocumentsIndexed:       s.NumberOfNewDocumentsIndexed,
		NumberOfModifiedDocumentsIndexed:  s.NumberOfModifiedDocumentsIndexed,
		NumberOfMetadataDocumentsModified: s.NumberOfMetadataDocumentsModified,
		NumberOfDocumentsDeleted:          s.NumberOfDocumentsDeleted,
		NumberOfDocumentsFailed:           s.NumberOfDocumentsFailed,
	}
}
