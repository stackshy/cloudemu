package secretmanager

import (
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// stateEnabled is the lifecycle state reported for every version — the mock
// models neither disabled nor destroyed versions.
const stateEnabled = "ENABLED"

type automaticJSON struct{}

type replicationJSON struct {
	Automatic *automaticJSON `json:"automatic,omitempty"`
}

type secretJSON struct {
	Name        string            `json:"name"`
	Replication replicationJSON   `json:"replication"`
	CreateTime  string            `json:"createTime,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

type versionResourceJSON struct {
	Name       string `json:"name"`
	CreateTime string `json:"createTime,omitempty"`
	State      string `json:"state"`
}

// payloadJSON carries the secret bytes; encoding/json renders []byte as the
// std-base64 string the wire expects.
type payloadJSON struct {
	Data []byte `json:"data"`
}

type createSecretRequest struct {
	Labels map[string]string `json:"labels"`
}

type addVersionRequest struct {
	Payload payloadJSON `json:"payload"`
}

type accessResponse struct {
	Name    string      `json:"name"`
	Payload payloadJSON `json:"payload"`
}

type listSecretsResponse struct {
	Secrets   []secretJSON `json:"secrets"`
	TotalSize int          `json:"totalSize"`
}

type listVersionsResponse struct {
	Versions  []versionResourceJSON `json:"versions"`
	TotalSize int                   `json:"totalSize"`
}

// secretName builds the canonical "projects/{p}/secrets/{id}" resource name,
// echoing the project from the request URL.
func secretName(project, id string) string {
	return "projects/" + project + "/" + secretsSeg + "/" + id
}

func versionName(project, id, version string) string {
	return secretName(project, id) + "/" + versionsSeg + "/" + version
}

// driverVersion maps the URL version segment to the driver's version key —
// "latest" resolves to the current version (empty key).
func driverVersion(v string) string {
	if v == latestAlias {
		return ""
	}

	return v
}

func toSecretJSON(project string, info *secretsdriver.SecretInfo) secretJSON {
	return secretJSON{
		Name:        secretName(project, info.Name),
		Replication: replicationJSON{Automatic: &automaticJSON{}},
		CreateTime:  info.CreatedAt,
		Labels:      info.Tags,
	}
}

func toVersionJSON(project, id string, ver *secretsdriver.SecretVersion) versionResourceJSON {
	return versionResourceJSON{
		Name:       versionName(project, id, ver.VersionID),
		CreateTime: ver.CreatedAt,
		State:      stateEnabled,
	}
}
