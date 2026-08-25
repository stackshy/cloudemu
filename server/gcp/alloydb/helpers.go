package alloydb

import (
	"encoding/json"
	"net/http"
	"time"

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

func (*Handler) toWireCluster(c *rdsdriver.Cluster, info *rdsdriver.AlloyDBClusterInfo) *alloydb.Cluster {
	out := &alloydb.Cluster{
		Name:            c.ARN,
		DisplayName:     info.DisplayName,
		DatabaseVersion: info.DatabaseVersion,
		Network:         info.Network,
		ClusterType:     info.ClusterType,
		State:           alloyDBState(c.State),
		Uid:             info.UID,
		Labels:          c.Tags,
		CreateTime:      formatTime(info.CreateTime),
		UpdateTime:      formatTime(info.UpdateTime),
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

// formatTime renders t as an RFC3339Nano timestamp, matching AlloyDB's
// output-only createTime/updateTime; a zero time renders as the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}

func (*Handler) toWireInstance(inst *rdsdriver.Instance, info *rdsdriver.AlloyDBInstanceInfo) *alloydb.Instance {
	return &alloydb.Instance{
		Name:             inst.ARN,
		DisplayName:      inst.ID,
		InstanceType:     info.InstanceType,
		AvailabilityType: info.AvailabilityType,
		IpAddress:        info.IPAddress,
		GceZone:          info.GceZone,
		State:            alloyDBState(inst.State),
		Uid:              inst.ID,
		CreateTime:       formatTime(info.CreateTime),
		UpdateTime:       formatTime(info.UpdateTime),
		MachineConfig:    &alloydb.MachineConfig{CpuCount: int64(info.CPUCount)},
	}
}

// alloyDBState maps the relationaldb driver's lifecycle state to AlloyDB's
// wire state enum, so a just-created or stopped resource reports its real
// state instead of always "READY".
const stateReady = "READY"

func alloyDBState(driverState string) string {
	switch driverState {
	case rdsdriver.StateAvailable, "":
		return stateReady
	case rdsdriver.StateCreating, rdsdriver.StateStarting:
		return "CREATING"
	case rdsdriver.StateDeleting:
		return "DELETING"
	case rdsdriver.StateStopped, rdsdriver.StateStopping:
		return "STOPPED"
	case rdsdriver.StateModifying, rdsdriver.StateRebooting, rdsdriver.StateBackingUp:
		return "MAINTENANCE"
	default:
		return stateReady
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

func toWireUser(p *alloyPath, u *rdsdriver.User) *alloydb.User {
	name := "projects/" + p.project + "/locations/" + p.location +
		"/clusters/" + u.Instance + "/users/" + u.Name

	return &alloydb.User{
		Name:     name,
		UserType: "ALLOYDB_BUILT_IN",
	}
}
