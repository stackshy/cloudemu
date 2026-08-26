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
		// /file-systems/replication-configurations : GET describe-all replication.
		if rest[0] == "replication-configurations" && r.Method == http.MethodGet {
			h.describeReplicationConfigurations(w, r, "")
			return
		}
		// /file-systems/{id} : PUT update, DELETE delete.
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

// serveFileSystemSub routes /file-systems/{id}/{sub}: policy, lifecycle-
// configuration, backup-policy, and replication-configuration.
func (h *Handler) serveFileSystemSub(w http.ResponseWriter, r *http.Request, id, sub string) {
	switch sub {
	case "policy":
		h.serveFSPolicy(w, r, id)
	case "lifecycle-configuration":
		h.serveLifecycleConfig(w, r, id)
	case "backup-policy":
		h.serveBackupPolicy(w, r, id)
	case "replication-configuration":
		h.serveReplicationConfig(w, r, id)
	default:
		notFound(w, r.URL.Path)
	}
}

func (h *Handler) serveFSPolicy(w http.ResponseWriter, r *http.Request, id string) {
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
}

func (h *Handler) serveLifecycleConfig(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeLifecycleConfiguration(w, r, id)
	case http.MethodPut:
		h.putLifecycleConfiguration(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveBackupPolicy(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		h.describeBackupPolicy(w, r, id)
	case http.MethodPut:
		h.putBackupPolicy(w, r, id)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) serveReplicationConfig(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		h.createReplicationConfiguration(w, r, id)
	case http.MethodDelete:
		h.deleteReplicationConfiguration(w, r, id)
	default:
		methodNotAllowed(w)
	}
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

	respondPaged(w, systems, err, lessFileSystem,
		q.Get("Marker"), parseMax(q.Get("MaxItems"), defaultPageSize), fileSystemToWire, buildFileSystems)
}

func lessFileSystem(a, b *driver.FileSystem) bool { return a.FileSystemID < b.FileSystemID }

func buildFileSystems(out []fileSystemJSON, next string) any {
	return describeFileSystemsResponse{FileSystems: out, NextMarker: next}
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
