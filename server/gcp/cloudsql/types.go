package cloudsql

import (
	"encoding/json"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/errors"
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

// Cloud SQL activation policy values. Real Cloud SQL exposes a third
// "ON_DEMAND" value for second-gen instances, but we never emit it.
const (
	activationAlways = "ALWAYS"
	activationNever  = "NEVER"
)

// sqlInstance is the JSON shape Cloud SQL expects for DatabaseInstance.
type sqlInstance struct {
	Kind            string       `json:"kind,omitempty"`
	Name            string       `json:"name,omitempty"`
	Project         string       `json:"project,omitempty"`
	Region          string       `json:"region,omitempty"`
	DatabaseVersion string       `json:"databaseVersion,omitempty"`
	State           string       `json:"state,omitempty"`
	BackendType     string       `json:"backendType,omitempty"`
	ConnectionName  string       `json:"connectionName,omitempty"`
	SelfLink        string       `json:"selfLink,omitempty"`
	RootPassword    string       `json:"rootPassword,omitempty"`
	IPAddresses     []ipMapping  `json:"ipAddresses,omitempty"`
	Settings        *sqlSettings `json:"settings,omitempty"`
	CreateTime      string       `json:"createTime,omitempty"`
}

type sqlSettings struct {
	Tier             string            `json:"tier,omitempty"`
	ActivationPolicy string            `json:"activationPolicy,omitempty"`
	DataDiskSizeGb   int               `json:"dataDiskSizeGb,string,omitempty"`
	DataDiskType     string            `json:"dataDiskType,omitempty"`
	AvailabilityType string            `json:"availabilityType,omitempty"`
	UserLabels       map[string]string `json:"userLabels,omitempty"`
}

type ipMapping struct {
	IPAddress string `json:"ipAddress"`
	Type      string `json:"type"`
}

type sqlInstanceList struct {
	Kind  string        `json:"kind"`
	Items []sqlInstance `json:"items"`
}

type backupRun struct {
	Kind        string `json:"kind,omitempty"`
	ID          string `json:"id,omitempty"`
	Status      string `json:"status,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Instance    string `json:"instance,omitempty"`
	StartTime   string `json:"startTime,omitempty"`
	EndTime     string `json:"endTime,omitempty"`
	BackupKind  string `json:"backupKind,omitempty"`
}

type backupRunList struct {
	Kind  string      `json:"kind"`
	Items []backupRun `json:"items"`
}

type restoreBackupBody struct {
	RestoreBackupContext struct {
		BackupRunID string `json:"backupRunId"`
		InstanceID  string `json:"instanceId,omitempty"`
		Project     string `json:"project,omitempty"`
	} `json:"restoreBackupContext"`
}

// operation is the Cloud SQL Admin LRO envelope. Real Cloud SQL does long-
// running ops; the mock returns DONE immediately.
type operation struct {
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	OperationType string `json:"operationType,omitempty"`
	Status        string `json:"status,omitempty"`
	User          string `json:"user,omitempty"`
	InsertTime    string `json:"insertTime,omitempty"`
	StartTime     string `json:"startTime,omitempty"`
	EndTime       string `json:"endTime,omitempty"`
	TargetID      string `json:"targetId,omitempty"`
	TargetProject string `json:"targetProject,omitempty"`
	TargetLink    string `json:"targetLink,omitempty"`
	SelfLink      string `json:"selfLink,omitempty"`
}

// doneOperation builds an LRO Operation with status=DONE and a synthetic name
// derived from project+verb.
func doneOperation(project, name, opType, _ string) operation {
	return operation{
		Kind:          "sql#operation",
		Name:          name,
		OperationType: opType,
		Status:        "DONE",
		TargetProject: project,
	}
}

// doneOperationWithTarget builds a DONE operation that also carries a
// targetLink/targetId pointing at the affected resource.
func doneOperationWithTarget(project, name, opType, resourceType, target string) operation {
	op := doneOperation(project, name, opType, "")
	op.TargetID = target
	op.TargetLink = "/sql/v1beta4/projects/" + project + "/" + resourceType + "/" + target

	return op
}

// toSQLInstance converts a portable Instance to the wire shape.
func toSQLInstance(inst *rdsdriver.Instance, project string) sqlInstance {
	return sqlInstance{
		Kind:            "sql#instance",
		Name:            inst.ID,
		Project:         project,
		Region:          inst.AvailabilityZone,
		DatabaseVersion: inst.Engine,
		State:           sqlState(inst.State),
		BackendType:     "SECOND_GEN",
		ConnectionName:  inst.Endpoint,
		SelfLink:        "/sql/v1beta4/projects/" + project + "/instances/" + inst.ID,
		IPAddresses: []ipMapping{
			{IPAddress: "10.0.0.1", Type: "PRIVATE"},
		},
		Settings: &sqlSettings{
			Tier:             inst.InstanceClass,
			DataDiskSizeGb:   inst.AllocatedStorage,
			DataDiskType:     inst.StorageType,
			UserLabels:       inst.Tags,
			ActivationPolicy: activationFromState(inst.State),
		},
		CreateTime: inst.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	}
}

// toBackupRun converts a portable Snapshot to the Cloud SQL backupRun shape.
func toBackupRun(snap *rdsdriver.Snapshot) backupRun {
	return backupRun{
		Kind:       "sql#backupRun",
		ID:         snap.ID,
		Status:     sqlBackupStatus(snap.State),
		Type:       "ON_DEMAND",
		Instance:   snap.InstanceID,
		StartTime:  snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		EndTime:    snap.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		BackupKind: "SNAPSHOT",
	}
}

// sqlState maps the portable lifecycle to the Cloud SQL DatabaseInstance.state
// enum (RUNNABLE, SUSPENDED, PENDING_CREATE, MAINTENANCE, FAILED, UNKNOWN_STATE).
func sqlState(s string) string {
	switch s {
	case rdsdriver.StateAvailable:
		return "RUNNABLE"
	case rdsdriver.StateStopped:
		return "SUSPENDED"
	case rdsdriver.StateCreating, rdsdriver.StateStarting:
		return "PENDING_CREATE"
	case rdsdriver.StateModifying, rdsdriver.StateRebooting:
		return "MAINTENANCE"
	case rdsdriver.StateDeleting:
		return "PENDING_DELETE"
	default:
		return "UNKNOWN_STATE"
	}
}

func activationFromState(s string) string {
	if s == rdsdriver.StateStopped {
		return activationNever
	}

	return activationAlways
}

func sqlBackupStatus(s string) string {
	if s == rdsdriver.SnapshotAvailable {
		return "SUCCESSFUL"
	}

	return "RUNNING"
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, reason, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  reason,
		},
	})
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case cerrors.IsNotFound(err):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case cerrors.IsAlreadyExists(err):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case cerrors.IsInvalidArgument(err):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	case cerrors.IsFailedPrecondition(err):
		writeError(w, http.StatusConflict, "FAILED_PRECONDITION", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}
