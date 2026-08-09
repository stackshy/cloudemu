package efs

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// serveFileSystems routes /file-systems and its sub-paths.
func (h *Handler) serveFileSystems(w http.ResponseWriter, r *http.Request, rest []string) {
	switch len(rest) {
	case 0:
		// /file-systems : POST create, GET describe
		switch r.Method {
		case http.MethodPost:
			h.createFileSystem(w, r)
		case http.MethodGet:
			h.describeFileSystems(w, r)
		default:
			methodNotAllowed(w)
		}
	case 1:
		// /file-systems/{id} : PUT update, DELETE delete.
		// /file-systems/replication-configurations : GET describe-all (P3).
		h.serveFileSystemByID(w, r, rest[0])
	default:
		// /file-systems/{id}/<sub> : policy (P1); lifecycle/backup/replication (P3).
		h.serveFileSystemSub(w, r, rest[0], rest[1])
	}
}

func (h *Handler) serveFileSystemByID(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPut:
		h.updateFileSystem(w, r, id)
	case http.MethodDelete:
		h.deleteFileSystem(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

// serveFileSystemSub routes /file-systems/{id}/{sub}. P1 handles "policy";
// later phases add lifecycle-configuration, backup-policy, replication.
func (h *Handler) serveFileSystemSub(w http.ResponseWriter, r *http.Request, id, sub string) {
	if sub == "policy" {
		switch r.Method {
		case http.MethodGet:
			h.describeFileSystemPolicy(w, r, id)
		case http.MethodPut:
			h.putFileSystemPolicy(w, r, id)
		case http.MethodDelete:
			h.deleteFileSystemPolicy(w, r, id)
		default:
			methodNotAllowed(w)
		}

		return
	}

	notFound(w, r.URL.Path)
}

func (h *Handler) createFileSystem(w http.ResponseWriter, r *http.Request) {
	var req createFileSystemRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fs, err := h.efs.CreateFileSystem(r.Context(), driver.CreateFileSystemInput{
		CreationToken:                req.CreationToken,
		PerformanceMode:              req.PerformanceMode,
		Encrypted:                    req.Encrypted,
		KMSKeyID:                     req.KMSKeyID,
		ThroughputMode:               req.ThroughputMode,
		ProvisionedThroughputInMibps: req.ProvisionedThroughputInMibps,
		AvailabilityZoneName:         req.AvailabilityZoneName,
		Backup:                       req.Backup,
		Tags:                         tagsToMap(req.Tags),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// EFS returns 201 Created for CreateFileSystem.
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(http.StatusCreated)
	encodeJSON(w, fileSystemToWire(fs))
}

func (h *Handler) describeFileSystems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	systems, err := h.efs.DescribeFileSystems(r.Context(), q.Get("FileSystemId"), q.Get("CreationToken"))
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]fileSystemJSON, 0, len(systems))
	for i := range systems {
		out = append(out, fileSystemToWire(&systems[i]))
	}

	writeJSON(w, describeFileSystemsResponse{FileSystems: out})
}

func (h *Handler) updateFileSystem(w http.ResponseWriter, r *http.Request, id string) {
	var req updateFileSystemRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	fs, err := h.efs.UpdateFileSystem(r.Context(), driver.UpdateFileSystemInput{
		FileSystemID:                 id,
		ThroughputMode:               req.ThroughputMode,
		ProvisionedThroughputInMibps: req.ProvisionedThroughputInMibps,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, fileSystemToWire(fs))
}

func (h *Handler) deleteFileSystem(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.efs.DeleteFileSystem(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusNoContent)
}

func (h *Handler) putFileSystemPolicy(w http.ResponseWriter, r *http.Request, id string) {
	var req putFileSystemPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	policy, err := h.efs.PutFileSystemPolicy(r.Context(), id, req.Policy, req.BypassPolicyLockoutSafetyCheck)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, fileSystemPolicyResponse{FileSystemID: id, Policy: policy})
}

func (h *Handler) describeFileSystemPolicy(w http.ResponseWriter, r *http.Request, id string) {
	policy, err := h.efs.DescribeFileSystemPolicy(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, fileSystemPolicyResponse{FileSystemID: id, Policy: policy})
}

func (h *Handler) deleteFileSystemPolicy(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.efs.DeleteFileSystemPolicy(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}

	writeStatus(w, http.StatusOK)
}
