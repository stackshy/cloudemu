package secretmanager

import (
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

type automaticJSON struct{}

type replicationJSON struct {
	Automatic *automaticJSON `json:"automatic,omitempty"`
}

// replicationStatusJSON mirrors the automatic replication shape on a version.
type replicationStatusJSON struct {
	Automatic *automaticJSON `json:"automatic,omitempty"`
}

type secretJSON struct {
	Name        string            `json:"name"`
	Replication replicationJSON   `json:"replication"`
	CreateTime  string            `json:"createTime,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Etag        string            `json:"etag,omitempty"`
}

type versionResourceJSON struct {
	Name              string                 `json:"name"`
	CreateTime        string                 `json:"createTime,omitempty"`
	DestroyTime       string                 `json:"destroyTime,omitempty"`
	State             string                 `json:"state"`
	ReplicationStatus *replicationStatusJSON `json:"replicationStatus,omitempty"`
	Etag              string                 `json:"etag,omitempty"`
}

// payloadJSON carries the secret bytes; encoding/json renders []byte as the
// std-base64 string the wire expects.
type payloadJSON struct {
	Data []byte `json:"data"`
}

type createSecretRequest struct {
	Labels map[string]string `json:"labels"`
}

// patchSecretRequest is the body of secrets.patch; only labels are modeled.
type patchSecretRequest struct {
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
	Secrets       []secretJSON `json:"secrets"`
	TotalSize     int          `json:"totalSize"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

type listVersionsResponse struct {
	Versions      []versionResourceJSON `json:"versions"`
	TotalSize     int                   `json:"totalSize"`
	NextPageToken string                `json:"nextPageToken,omitempty"`
}

// iamPolicyJSON is the GCP IAM Policy resource returned by getIamPolicy /
// setIamPolicy.
type iamPolicyJSON struct {
	Version  int              `json:"version,omitempty"`
	Bindings []iamBindingJSON `json:"bindings,omitempty"`
	Etag     string           `json:"etag,omitempty"`
}

type iamBindingJSON struct {
	Role    string   `json:"role"`
	Members []string `json:"members,omitempty"`
}

type setIamPolicyRequest struct {
	Policy iamPolicyJSON `json:"policy"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
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
		Etag:        info.Etag,
	}
}

func toVersionJSON(project, id string, ver *secretsdriver.SecretVersion) versionResourceJSON {
	state := ver.State
	if state == "" {
		state = secretsdriver.VersionEnabled
	}

	return versionResourceJSON{
		Name:              versionName(project, id, ver.VersionID),
		CreateTime:        ver.CreatedAt,
		DestroyTime:       ver.DestroyTime,
		State:             state,
		ReplicationStatus: &replicationStatusJSON{Automatic: &automaticJSON{}},
		Etag:              ver.Etag,
	}
}

func toPolicyJSON(pol *secretsdriver.GCPIAMPolicy) iamPolicyJSON {
	out := iamPolicyJSON{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, iamBindingJSON{Role: b.Role, Members: b.Members})
	}

	return out
}

func fromPolicyJSON(pol iamPolicyJSON) secretsdriver.GCPIAMPolicy {
	out := secretsdriver.GCPIAMPolicy{Version: pol.Version, Etag: pol.Etag}
	for _, b := range pol.Bindings {
		out.Bindings = append(out.Bindings, secretsdriver.GCPIAMBinding{Role: b.Role, Members: b.Members})
	}

	return out
}
