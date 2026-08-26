package cloudsql

import (
	"crypto/sha1" //nolint:gosec // SHA-1 fingerprints are the Cloud SQL cert identifier format, not a security control.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Cloud SQL activation policy values. Real Cloud SQL exposes a third
// "ON_DEMAND" value for second-gen instances, but we never emit it.
const (
	activationAlways = "ALWAYS"
	activationNever  = "NEVER"

	// availabilityRegional / availabilityZonal are the Cloud SQL
	// settings.availabilityType enum values. REGIONAL denotes a highly-available
	// (multi-zone) instance; ZONAL is the single-zone default.
	availabilityRegional = "REGIONAL"
	availabilityZonal    = "ZONAL"

	// serverCaCertPEM is the placeholder PEM returned as the instance's
	// serverCaCert. It is a well-formed shape for SDK round-trips, not a real CA.
	serverCaCertPEM = "-----BEGIN CERTIFICATE-----\nMOCK\n-----END CERTIFICATE-----"

	// selfLinkBase is the resource URI prefix Cloud SQL stamps on every
	// selfLink/targetLink. Even the v1 client is answered with sql/v1beta4
	// selfLinks — that is the version the real service returns.
	selfLinkBase = "https://sqladmin.googleapis.com/sql/v1beta4/projects/"

	// operationUser is the caller identity Cloud SQL records on an operation.
	// The emulator enforces no auth, so a fixed service-account address stands
	// in for the authenticated principal.
	operationUser = "cloudemu@cloudemu.iam.gserviceaccount.com"

	// rfc3339Milli is the millisecond RFC3339 layout Cloud SQL uses for its
	// timestamp fields (createTime, insertTime, startTime, endTime).
	rfc3339Milli = "2006-01-02T15:04:05.000Z"
)

// sqlInstance is the JSON shape Cloud SQL expects for DatabaseInstance.
type sqlInstance struct {
	Kind               string       `json:"kind,omitempty"`
	Name               string       `json:"name,omitempty"`
	Project            string       `json:"project,omitempty"`
	Region             string       `json:"region,omitempty"`
	DatabaseVersion    string       `json:"databaseVersion,omitempty"`
	State              string       `json:"state,omitempty"`
	BackendType        string       `json:"backendType,omitempty"`
	ConnectionName     string       `json:"connectionName,omitempty"`
	SelfLink           string       `json:"selfLink,omitempty"`
	RootPassword       string       `json:"rootPassword,omitempty"`
	MasterInstanceName string       `json:"masterInstanceName,omitempty"`
	ReplicaNames       []string     `json:"replicaNames,omitempty"`
	IPAddresses        []ipMapping  `json:"ipAddresses,omitempty"`
	Settings           *sqlSettings `json:"settings,omitempty"`
	ServerCaCert       *sslCert     `json:"serverCaCert,omitempty"`
	CreateTime         string       `json:"createTime,omitempty"`
}

type sqlSettings struct {
	Tier             string            `json:"tier,omitempty"`
	ActivationPolicy string            `json:"activationPolicy,omitempty"`
	DataDiskSizeGb   int               `json:"dataDiskSizeGb,string,omitempty"`
	DataDiskType     string            `json:"dataDiskType,omitempty"`
	AvailabilityType string            `json:"availabilityType,omitempty"`
	UserLabels       map[string]string `json:"userLabels,omitempty"`
	// databaseFlags, backupConfiguration and ipConfiguration are carried as
	// opaque JSON so they round-trip unchanged on Insert->Get and Patch without
	// the wire layer modeling every nested field real Cloud SQL exposes.
	DatabaseFlags       json.RawMessage `json:"databaseFlags,omitempty"`
	BackupConfiguration json.RawMessage `json:"backupConfiguration,omitempty"`
	IPConfiguration     json.RawMessage `json:"ipConfiguration,omitempty"`
}

type ipMapping struct {
	IPAddress string `json:"ipAddress"`
	Type      string `json:"type"`
}

type sqlInstanceList struct {
	Kind          string        `json:"kind"`
	Items         []sqlInstance `json:"items"`
	NextPageToken string        `json:"nextPageToken,omitempty"`
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

// doneOperationWithTarget builds a DONE operation that carries the full record
// a real Cloud SQL operation exposes: the affected resource (targetId /
// targetLink), the acting user, insert/start/end timestamps, and its own
// selfLink. Mutating handlers persist the result so a later Operations.Get
// returns this same record rather than a synthetic stand-in.
func doneOperationWithTarget(project, name, opType, resourceType, target string) operation {
	now := time.Now().UTC().Format(rfc3339Milli)

	return operation{
		Kind:          "sql#operation",
		Name:          name,
		OperationType: opType,
		Status:        "DONE",
		User:          operationUser,
		InsertTime:    now,
		StartTime:     now,
		EndTime:       now,
		TargetID:      target,
		TargetProject: project,
		TargetLink:    selfLinkBase + project + "/" + resourceType + "/" + target,
		SelfLink:      selfLinkBase + project + "/operations/" + name,
	}
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
		// connectionName is keyed on the REQUEST project (from the URL), matching
		// real Cloud SQL's {project}:{region}:{instance} — not the server's
		// configured project, which the stored inst.ConnectionName carries.
		ConnectionName:     project + ":" + inst.AvailabilityZone + ":" + inst.ID,
		MasterInstanceName: inst.ReadReplicaSource,
		ReplicaNames:       inst.ReadReplicaTargets,
		SelfLink:           selfLinkBase + project + "/instances/" + inst.ID,
		// PRIMARY is the public IP a client is meant to connect to. Endpoint holds
		// the reachable host: a synthetic IP normally, or the real engine host
		// when a database engine backs the instance.
		IPAddresses: []ipMapping{
			{IPAddress: inst.Endpoint, Type: "PRIMARY"},
		},
		Settings: &sqlSettings{
			Tier:                inst.InstanceClass,
			DataDiskSizeGb:      inst.AllocatedStorage,
			DataDiskType:        inst.StorageType,
			UserLabels:          inst.Tags,
			ActivationPolicy:    activationFromState(inst.State),
			AvailabilityType:    availabilityType(inst.MultiAZ),
			DatabaseFlags:       rawJSONOrNil(inst.GCPDatabaseFlags),
			BackupConfiguration: rawJSONOrNil(inst.GCPBackupConfig),
			IPConfiguration:     rawJSONOrNil(inst.GCPIPConfig),
		},
		ServerCaCert: serverCaCertFor(inst),
		CreateTime:   inst.CreatedAt.UTC().Format(rfc3339Milli),
	}
}

// availabilityType maps the portable MultiAZ flag to the Cloud SQL
// availabilityType enum: REGIONAL for a highly available (multi-zone) instance,
// ZONAL otherwise — matching what real Cloud SQL returns on a Get.
func availabilityType(multiAZ bool) string {
	if multiAZ {
		return availabilityRegional
	}

	return availabilityZonal
}

// rawJSONOrNil returns the stored opaque JSON as a RawMessage, or nil when empty
// so the field is omitted rather than emitted as an empty value.
func rawJSONOrNil(s string) json.RawMessage {
	if s == "" {
		return nil
	}

	return json.RawMessage(s)
}

// serverCaCertFor builds the synthetic server CA certificate Cloud SQL reports
// on every instance. Terraform reads server_ca_cert as a computed attribute, so
// it must be populated; the fingerprint is derived from the instance id so it is
// stable across reads.
func serverCaCertFor(inst *rdsdriver.Instance) *sslCert {
	sum := sha1.Sum([]byte("serverCaCert/" + inst.ID)) //nolint:gosec // fingerprint id, not a security control.
	fp := hex.EncodeToString(sum[:])

	return &sslCert{
		Kind:             "sql#sslCert",
		CommonName:       "Google Cloud SQL Server CA",
		Sha1Fingerprint:  fp,
		CertSerialNumber: fp[:16],
		Cert:             serverCaCertPEM,
		Instance:         inst.ID,
		CreateTime:       inst.CreatedAt.UTC().Format(rfc3339Milli),
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
		StartTime:  snap.CreatedAt.UTC().Format(rfc3339Milli),
		EndTime:    snap.CreatedAt.UTC().Format(rfc3339Milli),
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
