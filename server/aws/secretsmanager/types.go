package secretsmanager

import (
	"strings"
	"time"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Version stage labels, matching real Secrets Manager staging semantics: the
// current version carries AWSCURRENT, superseded versions AWSPREVIOUS.
const (
	stageCurrent  = "AWSCURRENT"
	stagePrevious = "AWSPREVIOUS"
)

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type secretListEntryJSON struct {
	ARN             string    `json:"ARN"`
	Name            string    `json:"Name"`
	Description     string    `json:"Description,omitempty"`
	Tags            []tagJSON `json:"Tags,omitempty"`
	CreatedDate     float64   `json:"CreatedDate,omitempty"`
	LastChangedDate float64   `json:"LastChangedDate,omitempty"`
}

type versionJSON struct {
	VersionID     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages,omitempty"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

// --- request envelopes ---

type createSecretRequest struct {
	Name         string    `json:"Name"`
	Description  string    `json:"Description"`
	SecretString string    `json:"SecretString"`
	SecretBinary []byte    `json:"SecretBinary"`
	Tags         []tagJSON `json:"Tags"`
}

type secretIDRequest struct {
	SecretID string `json:"SecretId"`
}

type getSecretValueRequest struct {
	SecretID  string `json:"SecretId"`
	VersionID string `json:"VersionId"`
}

type putSecretValueRequest struct {
	SecretID     string `json:"SecretId"`
	SecretString string `json:"SecretString"`
	SecretBinary []byte `json:"SecretBinary"`
}

// --- response envelopes ---

type createSecretResponse struct {
	ARN       string `json:"ARN"`
	Name      string `json:"Name"`
	VersionID string `json:"VersionId,omitempty"`
}

type deleteSecretResponse struct {
	ARN  string `json:"ARN"`
	Name string `json:"Name"`
}

type listSecretsResponse struct {
	SecretList []secretListEntryJSON `json:"SecretList"`
}

type getSecretValueResponse struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionID     string   `json:"VersionId"`
	SecretString  string   `json:"SecretString,omitempty"`
	VersionStages []string `json:"VersionStages,omitempty"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

type putSecretValueResponse struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionID     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages,omitempty"`
}

type listSecretVersionIDsResponse struct {
	ARN      string        `json:"ARN"`
	Name     string        `json:"Name"`
	Versions []versionJSON `json:"Versions"`
}

// epochSeconds converts an RFC3339 timestamp to Unix epoch seconds, the form
// the AWS JSON protocol uses for timestamp fields. Returns 0 on parse failure.
func epochSeconds(iso string) float64 {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return 0
	}

	return float64(t.Unix())
}

// resolveSecretID accepts either a plain secret name or a full ARN
// ("arn:aws:secretsmanager:<region>:<account>:secret:<name>") — real Secrets
// Manager accepts both forms for SecretId — and returns the bare name the
// driver keys on.
func resolveSecretID(id string) string {
	const marker = ":secret:"

	if !strings.HasPrefix(id, "arn:") {
		return id
	}

	if i := strings.LastIndex(id, marker); i >= 0 {
		return id[i+len(marker):]
	}

	return id
}

// secretValue picks the string payload if present, else the binary one — the
// driver stores raw bytes either way.
func secretValue(secretString string, secretBinary []byte) []byte {
	if secretString != "" {
		return []byte(secretString)
	}

	return secretBinary
}

func stagesFor(current bool) []string {
	if current {
		return []string{stageCurrent}
	}

	return []string{stagePrevious}
}

func mapToTags(m map[string]string) []tagJSON {
	if len(m) == 0 {
		return nil
	}

	out := make([]tagJSON, 0, len(m))
	for k, v := range m {
		out = append(out, tagJSON{Key: k, Value: v})
	}

	return out
}

func tagsToMap(tags []tagJSON) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func toSecretListEntry(info *secretsdriver.SecretInfo) secretListEntryJSON {
	return secretListEntryJSON{
		ARN:             info.ResourceID,
		Name:            info.Name,
		Description:     info.Description,
		Tags:            mapToTags(info.Tags),
		CreatedDate:     epochSeconds(info.CreatedAt),
		LastChangedDate: epochSeconds(info.UpdatedAt),
	}
}
