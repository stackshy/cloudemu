package secretmanager

import (
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// customerManagedEncryptionJSON is the CMEK config on a replica / automatic
// replication policy.
type customerManagedEncryptionJSON struct {
	KmsKeyName string `json:"kmsKeyName,omitempty"`
}

type automaticJSON struct {
	CustomerManagedEncryption *customerManagedEncryptionJSON `json:"customerManagedEncryption,omitempty"`
}

// replicaJSON is one user-managed replica location.
type replicaJSON struct {
	Location                  string                         `json:"location,omitempty"`
	CustomerManagedEncryption *customerManagedEncryptionJSON `json:"customerManagedEncryption,omitempty"`
}

type userManagedJSON struct {
	Replicas []replicaJSON `json:"replicas,omitempty"`
}

type replicationJSON struct {
	Automatic   *automaticJSON   `json:"automatic,omitempty"`
	UserManaged *userManagedJSON `json:"userManaged,omitempty"`
}

// replicaStatusJSON mirrors a replica location on a version's status.
type replicaStatusJSON struct {
	Location string `json:"location,omitempty"`
}

type userManagedStatusJSON struct {
	Replicas []replicaStatusJSON `json:"replicas,omitempty"`
}

// replicationStatusJSON mirrors the replication shape on a version.
type replicationStatusJSON struct {
	Automatic   *struct{}              `json:"automatic,omitempty"`
	UserManaged *userManagedStatusJSON `json:"userManaged,omitempty"`
}

type rotationJSON struct {
	RotationPeriod   string `json:"rotationPeriod,omitempty"`
	NextRotationTime string `json:"nextRotationTime,omitempty"`
}

type topicJSON struct {
	Name string `json:"name,omitempty"`
}

type secretJSON struct {
	Name           string            `json:"name"`
	Replication    replicationJSON   `json:"replication"`
	CreateTime     string            `json:"createTime,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	ExpireTime     string            `json:"expireTime,omitempty"`
	Rotation       *rotationJSON     `json:"rotation,omitempty"`
	Topics         []topicJSON       `json:"topics,omitempty"`
	VersionAliases map[string]string `json:"versionAliases,omitempty"`
	Etag           string            `json:"etag,omitempty"`
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
// std-base64 string the wire expects. DataCrc32c is the Castagnoli CRC32C of
// the payload, encoded as a string integer to match the SDK.
type payloadJSON struct {
	Data       []byte `json:"data"`
	DataCrc32c int64  `json:"dataCrc32c,omitempty,string"`
}

// createSecretRequest is the secrets.create body (the Secret resource).
type createSecretRequest struct {
	Replication    *replicationJSON  `json:"replication"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	TTL            string            `json:"ttl"`
	ExpireTime     string            `json:"expireTime"`
	Rotation       *rotationJSON     `json:"rotation"`
	Topics         []topicJSON       `json:"topics"`
	VersionAliases map[string]string `json:"versionAliases"`
}

// patchSecretRequest is the body of secrets.patch; the update mask names which
// fields to apply.
type patchSecretRequest struct {
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	TTL            string            `json:"ttl"`
	ExpireTime     string            `json:"expireTime"`
	Rotation       *rotationJSON     `json:"rotation"`
	Topics         []topicJSON       `json:"topics"`
	VersionAliases map[string]string `json:"versionAliases"`
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

// replicationToJSON renders a driver replication policy on the wire, defaulting
// to automatic when unset (a secret always carries a replication policy).
func replicationToJSON(rep *secretsdriver.GCPReplication) replicationJSON {
	if rep == nil || (!rep.Automatic && len(rep.UserManaged) == 0) {
		return replicationJSON{Automatic: &automaticJSON{}}
	}

	if len(rep.UserManaged) > 0 {
		replicas := make([]replicaJSON, 0, len(rep.UserManaged))

		for _, r := range rep.UserManaged {
			rj := replicaJSON{Location: r.Location}
			if r.KMSKeyName != "" {
				rj.CustomerManagedEncryption = &customerManagedEncryptionJSON{KmsKeyName: r.KMSKeyName}
			}

			replicas = append(replicas, rj)
		}

		return replicationJSON{UserManaged: &userManagedJSON{Replicas: replicas}}
	}

	auto := &automaticJSON{}
	if rep.AutomaticKMSKeyName != "" {
		auto.CustomerManagedEncryption = &customerManagedEncryptionJSON{KmsKeyName: rep.AutomaticKMSKeyName}
	}

	return replicationJSON{Automatic: auto}
}

// replicationFromJSON decodes a wire replication policy into the driver model.
// Returns nil when neither branch is present (caller enforces required-ness).
func replicationFromJSON(rep *replicationJSON) *secretsdriver.GCPReplication {
	if rep == nil {
		return nil
	}

	if rep.UserManaged != nil {
		replicas := make([]secretsdriver.GCPReplica, 0, len(rep.UserManaged.Replicas))

		for _, r := range rep.UserManaged.Replicas {
			rep := secretsdriver.GCPReplica{Location: r.Location}
			if r.CustomerManagedEncryption != nil {
				rep.KMSKeyName = r.CustomerManagedEncryption.KmsKeyName
			}

			replicas = append(replicas, rep)
		}

		return &secretsdriver.GCPReplication{UserManaged: replicas}
	}

	if rep.Automatic != nil {
		out := &secretsdriver.GCPReplication{Automatic: true}
		if rep.Automatic.CustomerManagedEncryption != nil {
			out.AutomaticKMSKeyName = rep.Automatic.CustomerManagedEncryption.KmsKeyName
		}

		return out
	}

	return nil
}

// replicationStatusFor renders a version's replicationStatus mirroring the
// parent secret's replication policy.
func replicationStatusFor(rep *secretsdriver.GCPReplication) *replicationStatusJSON {
	if rep != nil && len(rep.UserManaged) > 0 {
		replicas := make([]replicaStatusJSON, 0, len(rep.UserManaged))
		for _, r := range rep.UserManaged {
			replicas = append(replicas, replicaStatusJSON{Location: r.Location})
		}

		return &replicationStatusJSON{UserManaged: &userManagedStatusJSON{Replicas: replicas}}
	}

	return &replicationStatusJSON{Automatic: &struct{}{}}
}

func rotationToJSON(rot *secretsdriver.GCPRotation) *rotationJSON {
	if rot == nil {
		return nil
	}

	return &rotationJSON{RotationPeriod: rot.RotationPeriod, NextRotationTime: rot.NextRotationTime}
}

func topicsToJSON(topics []string) []topicJSON {
	if len(topics) == 0 {
		return nil
	}

	out := make([]topicJSON, 0, len(topics))
	for _, t := range topics {
		out = append(out, topicJSON{Name: t})
	}

	return out
}

func toSecretJSON(project string, info *secretsdriver.SecretInfo) secretJSON {
	return secretJSON{
		Name:           secretName(project, info.Name),
		Replication:    replicationToJSON(info.Replication),
		CreateTime:     info.CreatedAt,
		Labels:         info.Tags,
		Annotations:    info.Annotations,
		ExpireTime:     info.ExpireTime,
		Rotation:       rotationToJSON(info.Rotation),
		Topics:         topicsToJSON(info.Topics),
		VersionAliases: info.VersionAliases,
		Etag:           info.Etag,
	}
}

func toVersionJSON(project, id string, ver *secretsdriver.SecretVersion, rep *secretsdriver.GCPReplication) versionResourceJSON {
	state := ver.State
	if state == "" {
		state = secretsdriver.VersionEnabled
	}

	return versionResourceJSON{
		Name:              versionName(project, id, ver.VersionID),
		CreateTime:        ver.CreatedAt,
		DestroyTime:       ver.DestroyTime,
		State:             state,
		ReplicationStatus: replicationStatusFor(rep),
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
