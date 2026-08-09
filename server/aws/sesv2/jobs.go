package sesv2

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// serveImportJobs routes /import-jobs and its sub-paths.
func (h *Handler) serveImportJobs(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		jobID, err := h.ses.CreateImportJob(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, createJobResponse{JobID: jobID})
	case 1:
		if rest[0] == segList && r.Method == http.MethodPost {
			h.listImportJobs(w, r)

			return
		}

		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		job, err := h.ses.GetImportJob(r.Context(), rest[0])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, importJobToJSON(job))
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) listImportJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.ses.ListImportJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]jobSummaryJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobToSummary(&jobs[i]))
	}

	writeJSON(w, listImportJobsResponse{ImportJobs: out})
}

// serveExportJobs routes /export-jobs and its sub-paths.
func (h *Handler) serveExportJobs(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		if r.Method != http.MethodPost {
			methodNotAllowed(w)

			return
		}

		jobID, err := h.ses.CreateExportJob(r.Context())
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, createJobResponse{JobID: jobID})
	case 1:
		if r.Method != http.MethodGet {
			methodNotAllowed(w)

			return
		}

		job, err := h.ses.GetExportJob(r.Context(), rest[0])
		if err != nil {
			writeErr(w, err)

			return
		}

		writeJSON(w, exportJobToJSON(job))
	case twoSegments:
		if rest[1] == "cancel" && r.Method == http.MethodPut {
			writeOK(w, h.ses.CancelExportJob(r.Context(), rest[0]))

			return
		}

		notFound(w, r.URL.Path)
	default:
		notFound(w, r.URL.Path)
	}
}

// serveListExportJobs routes /list-export-jobs (POST).
func (h *Handler) serveListExportJobs(w http.ResponseWriter, r *http.Request, rest []string) {
	if len(rest) != 0 || r.Method != http.MethodPost {
		notFound(w, r.URL.Path)

		return
	}

	jobs, err := h.ses.ListExportJobs(r.Context())
	if err != nil {
		writeErr(w, err)

		return
	}

	out := make([]jobSummaryJSON, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobToSummary(&jobs[i]))
	}

	writeJSON(w, listExportJobsResponse{ExportJobs: out})
}

func importJobToJSON(j *driver.Job) getImportJobResponse {
	return getImportJobResponse{
		JobID:            j.JobID,
		JobStatus:        j.Status,
		CreatedTimestamp: epochSeconds(j.CreatedAt),
	}
}

func exportJobToJSON(j *driver.Job) getExportJobResponse {
	return getExportJobResponse{
		JobID:            j.JobID,
		JobStatus:        j.Status,
		CreatedTimestamp: epochSeconds(j.CreatedAt),
	}
}

func jobToSummary(j *driver.Job) jobSummaryJSON {
	return jobSummaryJSON{
		JobID:            j.JobID,
		JobStatus:        j.Status,
		CreatedTimestamp: epochSeconds(j.CreatedAt),
	}
}
