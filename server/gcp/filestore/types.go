package filestore

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// jsonInt64 is a GCP REST int64 field: it decodes from either a JSON string
// ("1024") or a JSON number (1024) and always marshals to the quoted-string
// form real Google REST APIs emit for int64/uint64 values.
type jsonInt64 int64

// UnmarshalJSON accepts a quoted or bare integer.
func (n *jsonInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == nullLiteral {
		return nil
	}

	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}

	*n = jsonInt64(v)

	return nil
}

// MarshalJSON emits the value as a quoted string, matching GCP's REST wire form.
func (n jsonInt64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(n), 10) + `"`), nil
}

// --- request bodies ---

// instanceRequest is the create/patch body. Enum fields use rawEnum so both the
// canonical STRING name and the protobuf INTEGER form decode.
type instanceRequest struct {
	Description string            `json:"description"`
	Tier        rawEnum           `json:"tier"`
	Labels      map[string]string `json:"labels"`
	FileShares  []fileShareReq    `json:"fileShares"`
	Networks    []networkReq      `json:"networks"`
	Etag        string            `json:"etag"`
	KmsKeyName  string            `json:"kmsKeyName"`
}

type fileShareReq struct {
	Name             string         `json:"name"`
	CapacityGb       jsonInt64      `json:"capacityGb"`
	SourceBackup     string         `json:"sourceBackup"`
	NfsExportOptions []nfsExportReq `json:"nfsExportOptions"`
}

type nfsExportReq struct {
	IPRanges   []string  `json:"ipRanges"`
	Network    string    `json:"network"`
	AccessMode rawEnum   `json:"accessMode"`
	SquashMode rawEnum   `json:"squashMode"`
	AnonUID    jsonInt64 `json:"anonUid"`
	AnonGID    jsonInt64 `json:"anonGid"`
}

type networkReq struct {
	Network         string    `json:"network"`
	Modes           []rawEnum `json:"modes"`
	ReservedIPRange string    `json:"reservedIpRange"`
	ConnectMode     rawEnum   `json:"connectMode"`
}

// --- response bodies ---

type instanceJSON struct {
	Name          string            `json:"name"`
	Description   string            `json:"description,omitempty"`
	State         string            `json:"state,omitempty"`
	StatusMessage string            `json:"statusMessage,omitempty"`
	CreateTime    string            `json:"createTime,omitempty"`
	Tier          string            `json:"tier,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	FileShares    []fileShareJSON   `json:"fileShares,omitempty"`
	Networks      []networkJSON     `json:"networks,omitempty"`
	Etag          string            `json:"etag,omitempty"`
	KmsKeyName    string            `json:"kmsKeyName,omitempty"`
}

type fileShareJSON struct {
	Name             string          `json:"name,omitempty"`
	CapacityGb       jsonInt64       `json:"capacityGb,omitempty"`
	SourceBackup     string          `json:"sourceBackup,omitempty"`
	NfsExportOptions []nfsExportJSON `json:"nfsExportOptions,omitempty"`
}

type nfsExportJSON struct {
	IPRanges   []string  `json:"ipRanges,omitempty"`
	Network    string    `json:"network,omitempty"`
	AccessMode string    `json:"accessMode,omitempty"`
	SquashMode string    `json:"squashMode,omitempty"`
	AnonUID    jsonInt64 `json:"anonUid,omitempty"`
	AnonGID    jsonInt64 `json:"anonGid,omitempty"`
}

type networkJSON struct {
	Network         string   `json:"network,omitempty"`
	Modes           []string `json:"modes,omitempty"`
	ReservedIPRange string   `json:"reservedIpRange,omitempty"`
	IPAddresses     []string `json:"ipAddresses,omitempty"`
	ConnectMode     string   `json:"connectMode,omitempty"`
}

type listInstancesResponse struct {
	Instances     []instanceJSON `json:"instances"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// operationJSON mirrors google.longrunning.Operation. Every mutation completes
// inline, so done is always true.
type operationJSON struct {
	Name     string          `json:"name"`
	Done     bool            `json:"done"`
	Response json.RawMessage `json:"response,omitempty"`
}

// --- name builders ---

func instanceName(project, location, instanceID string) string {
	return "projects/" + project + "/locations/" + location + "/instances/" + instanceID
}

func operationName(project, location, opID string) string {
	return "projects/" + project + "/locations/" + location + "/operations/" + opID
}

// locationPrefix is the resource-name prefix shared by every instance in a
// (project, location).
func locationPrefix(project, location string) string {
	return "projects/" + project + "/locations/" + location + "/instances/"
}

// --- time helper ---

// rfc3339 formats t as the RFC 3339 UTC timestamp Filestore emits.
func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
