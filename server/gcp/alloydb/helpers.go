package alloydb

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	alloydb "google.golang.org/api/alloydb/v1"
	"google.golang.org/api/googleapi"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/gcp/opmeta"
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
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", msg)
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", msg)
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", msg)
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION", msg)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", msg)
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

// AlloyDB Any type URLs a done operation carries.
const (
	clusterTypeURL  = "type.googleapis.com/google.cloud.alloydb.v1.Cluster"
	instanceTypeURL = "type.googleapis.com/google.cloud.alloydb.v1.Instance"
	backupTypeURL   = "type.googleapis.com/google.cloud.alloydb.v1.Backup"
	opMetaTypeURL   = "type.googleapis.com/google.cloud.alloydb.v1.OperationMetadata"
)

// doneOperation builds a completed AlloyDB LRO envelope carrying the resource as
// a typed Any response (google.protobuf.Empty when response is nil, i.e. a
// delete) plus an OperationMetadata Any, and records it with the shared poller
// so a client that polls the returned name resolves the same done operation.
// verb is "<verb>-<kind>" (e.g. "create-cluster"); its first word is the
// metadata verb. Each call mints a fresh operation id.
func (h *Handler) doneOperation(p *alloyPath, verb, typeURL string, response any) *alloydb.Operation {
	now := h.clock.Now()
	name := "projects/" + p.project + "/locations/" + p.location + "/operations/" + opmeta.NewID(now)

	resp := opmeta.Empty()
	if response != nil {
		resp = opmeta.Response(response, typeURL)
	}

	metaVerb, _, _ := strings.Cut(verb, "-")
	meta := opmeta.Metadata(opMetaTypeURL, now, p.target(), metaVerb)

	h.ops.RegisterWithMetadata(name, resp, meta)

	return &alloydb.Operation{Name: name, Done: true, Response: googleapi.RawMessage(resp), Metadata: googleapi.RawMessage(meta)}
}

// target is the resource name an operation on p acts on.
func (p *alloyPath) target() string {
	base := "projects/" + p.project + "/locations/" + p.location

	switch {
	case p.backupID != "":
		return base + "/backups/" + p.backupID
	case p.clusterID != "" && p.subID != "":
		return base + "/clusters/" + p.clusterID + "/" + p.sub + "/" + p.subID
	case p.clusterID != "":
		return base + "/clusters/" + p.clusterID
	default:
		return base + "/" + p.collection
	}
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

	if info.Network != "" {
		out.NetworkConfig = &alloydb.NetworkConfig{Network: info.Network}
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
	out := &alloydb.Instance{
		Name:             inst.ARN,
		DisplayName:      inst.ID,
		InstanceType:     info.InstanceType,
		AvailabilityType: info.AvailabilityType,
		IpAddress:        info.IPAddress,
		State:            alloyDBState(inst.State),
		Uid:              inst.ID,
		Labels:           inst.Tags,
		CreateTime:       formatTime(info.CreateTime),
		UpdateTime:       formatTime(info.UpdateTime),
		MachineConfig:    &alloydb.MachineConfig{CpuCount: int64(info.CPUCount)},
	}

	// gceZone applies only to a ZONAL instance; a REGIONAL one has none.
	if info.AvailabilityType == availabilityZonal {
		out.GceZone = info.GceZone
	}

	return out
}

const availabilityZonal = "ZONAL"

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
