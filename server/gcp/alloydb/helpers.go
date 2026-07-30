package alloydb

import (
	"encoding/json"
	"net/http"

	alloydb "google.golang.org/api/alloydb/v1"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// gcpError is the google.rpc-style REST error envelope.
type gcpError struct {
	Error gcpErrorBody `json:"error"`
}

type gcpErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// writeError writes a GCP-style JSON error response.
func writeError(w http.ResponseWriter, status int, gcpStatus, msg string) {
	writeJSON(w, status, gcpError{Error: gcpErrorBody{Code: status, Message: msg, Status: gcpStatus}})
}

// writeCErr maps a CloudEmu canonical error to the matching GCP HTTP status.
func writeCErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}

// decodeJSON reads a JSON request body into v; writes a 400 and returns false
// on error.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return false
	}

	return true
}

// doneOperation builds a completed AlloyDB LRO envelope carrying the resource
// as its response. AlloyDB REST callers receive a terminal operation, so no
// polling is required.
func (*Handler) doneOperation(p *alloyPath, verb string, response any) *alloydb.Operation {
	name := "projects/" + p.project + "/locations/" + p.location + "/operations/op-" + verb

	op := &alloydb.Operation{Name: name, Done: true}

	if response != nil {
		if raw, err := json.Marshal(response); err == nil {
			op.Response = raw
		}
	}

	return op
}

// alloyCap returns the AlloyDB optional capability, or false if unsupported.
func (h *Handler) alloyCap() (rdsdriver.AlloyDB, bool) {
	c, ok := h.db.(rdsdriver.AlloyDB)

	return c, ok
}

// usersCap returns the Users optional capability, or false if unsupported.
func (h *Handler) usersCap() (rdsdriver.Users, bool) {
	c, ok := h.db.(rdsdriver.Users)

	return c, ok
}

// ---- driver → SDK wire conversions ----

func (*Handler) toWireCluster(c *rdsdriver.Cluster, info *rdsdriver.AlloyDBClusterInfo) *alloydb.Cluster {
	out := &alloydb.Cluster{
		Name:            c.ARN,
		DisplayName:     c.ID,
		DatabaseVersion: info.DatabaseVersion,
		Network:         info.Network,
		ClusterType:     info.ClusterType,
		State:           "READY",
		Uid:             c.ID,
		ContinuousBackupConfig: &alloydb.ContinuousBackupConfig{
			Enabled: info.ContinuousBackup,
		},
		AutomatedBackupPolicy: &alloydb.AutomatedBackupPolicy{
			Enabled: info.AutomatedBackupEnabled,
		},
	}

	if info.PrimaryCluster != "" {
		out.SecondaryConfig = &alloydb.SecondaryConfig{PrimaryClusterName: info.PrimaryCluster}
	}

	return out
}

func (*Handler) toWireInstance(inst *rdsdriver.Instance, info *rdsdriver.AlloyDBInstanceInfo) *alloydb.Instance {
	return &alloydb.Instance{
		Name:             inst.ARN,
		DisplayName:      inst.ID,
		InstanceType:     info.InstanceType,
		AvailabilityType: info.AvailabilityType,
		IpAddress:        info.IPAddress,
		GceZone:          info.GceZone,
		State:            "READY",
		Uid:              inst.ID,
		MachineConfig:    &alloydb.MachineConfig{CpuCount: int64(info.CPUCount)},
	}
}

func toWireBackup(s *rdsdriver.ClusterSnapshot, backupType string) *alloydb.Backup {
	return &alloydb.Backup{
		Name:            s.ARN,
		DisplayName:     s.ID,
		ClusterName:     s.ClusterID,
		DatabaseVersion: s.EngineVersion,
		Type:            backupType,
		State:           "READY",
		Uid:             s.ID,
	}
}

func toWireUser(u *rdsdriver.User) *alloydb.User {
	return &alloydb.User{
		Name:     u.Name,
		UserType: "ALLOYDB_BUILT_IN",
	}
}
