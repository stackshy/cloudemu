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
	// DeletedDate is set only for a secret scheduled for deletion: the date the
	// secret is scheduled to be removed (delete request + RecoveryWindowInDays).
	DeletedDate float64 `json:"DeletedDate,omitempty"`
	// KmsKeyId is the customer KMS key the secret is encrypted with, echoed on
	// DescribeSecret. Omitted when the default aws/secretsmanager key is used.
	KmsKeyID string `json:"KmsKeyId,omitempty"`

	VersionIDsToStages map[string][]string `json:"VersionIdsToStages,omitempty"`

	// Rotation* mirror RotateSecret/CancelRotateSecret's configuration, echoed
	// on both DescribeSecret and ListSecrets (real Secrets Manager exposes them
	// on both).
	RotationEnabled   bool               `json:"RotationEnabled,omitempty"`
	RotationLambdaARN string             `json:"RotationLambdaARN,omitempty"`
	RotationRules     *rotationRulesJSON `json:"RotationRules,omitempty"`
	LastRotatedDate   float64            `json:"LastRotatedDate,omitempty"`
	NextRotationDate  float64            `json:"NextRotationDate,omitempty"`
}

// rotationRulesJSON is the RotateSecret/DescribeSecret RotationRules shape.
type rotationRulesJSON struct {
	AutomaticallyAfterDays int64  `json:"AutomaticallyAfterDays,omitempty"`
	Duration               string `json:"Duration,omitempty"`
	ScheduleExpression     string `json:"ScheduleExpression,omitempty"`
}

// toDriver converts the wire rules to the driver's rotation-rules type. The
// zero value round-trips to the driver's zero value, which RotateSecret reads
// as "no rules supplied in this request".
func (r rotationRulesJSON) toDriver() secretsdriver.SecretRotationRules {
	return secretsdriver.SecretRotationRules{
		AutomaticallyAfterDays: r.AutomaticallyAfterDays,
		Duration:               r.Duration,
		ScheduleExpression:     r.ScheduleExpression,
	}
}

// applyRotationInfo copies a secret's rotation configuration onto a
// DescribeSecret/ListSecrets entry.
func applyRotationInfo(entry *secretListEntryJSON, info *secretsdriver.SecretRotationInfo) {
	entry.RotationEnabled = info.Enabled
	entry.RotationLambdaARN = info.LambdaARN

	if info.Rules != (secretsdriver.SecretRotationRules{}) {
		entry.RotationRules = &rotationRulesJSON{
			AutomaticallyAfterDays: info.Rules.AutomaticallyAfterDays,
			Duration:               info.Rules.Duration,
			ScheduleExpression:     info.Rules.ScheduleExpression,
		}
	}

	entry.LastRotatedDate = epochSeconds(info.LastRotatedDate)
	entry.NextRotationDate = epochSeconds(info.NextRotationDate)
}

type versionJSON struct {
	VersionID     string   `json:"VersionId"`
	VersionStages []string `json:"VersionStages,omitempty"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

// --- request envelopes ---

type createSecretRequest struct {
	Name               string    `json:"Name"`
	Description        string    `json:"Description"`
	SecretString       string    `json:"SecretString"`
	SecretBinary       []byte    `json:"SecretBinary"`
	Tags               []tagJSON `json:"Tags"`
	KmsKeyID           string    `json:"KmsKeyId"`
	ClientRequestToken string    `json:"ClientRequestToken"`
}

type secretIDRequest struct {
	SecretID string `json:"SecretId"`
}

type listSecretVersionIDsRequest struct {
	SecretID string `json:"SecretId"`
	// IncludeDeprecated, when true, also returns versions that carry no staging
	// labels (deprecated versions). By default AWS omits them.
	IncludeDeprecated bool   `json:"IncludeDeprecated"`
	MaxResults        int32  `json:"MaxResults"`
	NextToken         string `json:"NextToken"`
}

type deleteSecretRequest struct {
	SecretID string `json:"SecretId"`
	// RecoveryWindowInDays is a pointer so an absent field (nil) is
	// distinguishable from an explicit 0 — the latter is an invalid value AWS
	// rejects, while nil applies the default recovery window.
	RecoveryWindowInDays       *int64 `json:"RecoveryWindowInDays"`
	ForceDeleteWithoutRecovery bool   `json:"ForceDeleteWithoutRecovery"`
}

type getSecretValueRequest struct {
	SecretID     string `json:"SecretId"`
	VersionID    string `json:"VersionId"`
	VersionStage string `json:"VersionStage"`
}

type getRandomPasswordRequest struct {
	PasswordLength          int64  `json:"PasswordLength"`
	ExcludeCharacters       string `json:"ExcludeCharacters"`
	ExcludeNumbers          bool   `json:"ExcludeNumbers"`
	ExcludePunctuation      bool   `json:"ExcludePunctuation"`
	ExcludeUppercase        bool   `json:"ExcludeUppercase"`
	ExcludeLowercase        bool   `json:"ExcludeLowercase"`
	IncludeSpace            bool   `json:"IncludeSpace"`
	RequireEachIncludedType bool   `json:"RequireEachIncludedType"`
}

type secretFilter struct {
	Key    string   `json:"Key"`
	Values []string `json:"Values"`
}

type listSecretsRequest struct {
	MaxResults int32          `json:"MaxResults"`
	NextToken  string         `json:"NextToken"`
	Filters    []secretFilter `json:"Filters"`
}

type putSecretValueRequest struct {
	SecretID           string   `json:"SecretId"`
	SecretString       string   `json:"SecretString"`
	SecretBinary       []byte   `json:"SecretBinary"`
	ClientRequestToken string   `json:"ClientRequestToken"`
	VersionStages      []string `json:"VersionStages"`
}

type updateSecretVersionStageRequest struct {
	SecretID            string `json:"SecretId"`
	VersionStage        string `json:"VersionStage"`
	RemoveFromVersionID string `json:"RemoveFromVersionId"`
	MoveToVersionID     string `json:"MoveToVersionId"`
}

type updateSecretRequest struct {
	SecretID     string `json:"SecretId"`
	Description  string `json:"Description"`
	SecretString string `json:"SecretString"`
	SecretBinary []byte `json:"SecretBinary"`
}

type tagResourceRequest struct {
	SecretID string    `json:"SecretId"`
	Tags     []tagJSON `json:"Tags"`
}

type untagResourceRequest struct {
	SecretID string   `json:"SecretId"`
	TagKeys  []string `json:"TagKeys"`
}

type rotateSecretRequest struct {
	SecretID           string            `json:"SecretId"`
	ClientRequestToken string            `json:"ClientRequestToken"`
	RotationLambdaARN  string            `json:"RotationLambdaARN"`
	RotationRules      rotationRulesJSON `json:"RotationRules"`
	// RotateImmediately is a pointer so an absent field (nil, defaults to true)
	// is distinguishable from an explicit false, which configures rotation
	// without running it now.
	RotateImmediately *bool `json:"RotateImmediately"`
}

type updateSecretResponse struct {
	ARN       string `json:"ARN"`
	Name      string `json:"Name"`
	VersionID string `json:"VersionId,omitempty"`
}

type updateSecretVersionStageResponse struct {
	ARN  string `json:"ARN"`
	Name string `json:"Name"`
}

// --- response envelopes ---

type createSecretResponse struct {
	ARN       string `json:"ARN"`
	Name      string `json:"Name"`
	VersionID string `json:"VersionId,omitempty"`
}

type deleteSecretResponse struct {
	ARN          string  `json:"ARN"`
	Name         string  `json:"Name"`
	DeletionDate float64 `json:"DeletionDate,omitempty"`
}

// secretRefResponse is the {ARN, Name} envelope returned by the operations that
// only echo the secret reference: RestoreSecret and Put/DeleteResourcePolicy.
type secretRefResponse struct {
	ARN  string `json:"ARN"`
	Name string `json:"Name"`
}

type rotateSecretResponse struct {
	ARN       string `json:"ARN"`
	Name      string `json:"Name"`
	VersionID string `json:"VersionId"`
}

type getRandomPasswordResponse struct {
	RandomPassword string `json:"RandomPassword"`
}

type listSecretsResponse struct {
	SecretList []secretListEntryJSON `json:"SecretList"`
	NextToken  string                `json:"NextToken,omitempty"`
}

type getSecretValueResponse struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionID     string   `json:"VersionId"`
	SecretString  string   `json:"SecretString,omitempty"`
	SecretBinary  []byte   `json:"SecretBinary,omitempty"`
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
	ARN       string        `json:"ARN"`
	Name      string        `json:"Name"`
	Versions  []versionJSON `json:"Versions"`
	NextToken string        `json:"NextToken,omitempty"`
}

type putResourcePolicyRequest struct {
	SecretID          string `json:"SecretId"`
	ResourcePolicy    string `json:"ResourcePolicy"`
	BlockPublicPolicy bool   `json:"BlockPublicPolicy"`
}

// getResourcePolicyResponse omits ResourcePolicy when none is set, which the SDK
// reads as "no policy".
type getResourcePolicyResponse struct {
	ARN            string `json:"ARN"`
	Name           string `json:"Name"`
	ResourcePolicy string `json:"ResourcePolicy,omitempty"`
}

type validateResourcePolicyRequest struct {
	SecretID       string `json:"SecretId"`
	ResourcePolicy string `json:"ResourcePolicy"`
}

type validationErrorEntry struct {
	CheckName    string `json:"CheckName"`
	ErrorMessage string `json:"ErrorMessage"`
}

type validateResourcePolicyResponse struct {
	PolicyValidationPassed bool                   `json:"PolicyValidationPassed"`
	ValidationErrors       []validationErrorEntry `json:"ValidationErrors"`
}

type batchGetSecretValueRequest struct {
	SecretIDList []string       `json:"SecretIdList"`
	Filters      []secretFilter `json:"Filters"`
	MaxResults   int32          `json:"MaxResults"`
	NextToken    string         `json:"NextToken"`
}

// secretValueEntry is one resolved secret in a BatchGetSecretValue response.
type secretValueEntry struct {
	ARN           string   `json:"ARN"`
	Name          string   `json:"Name"`
	VersionID     string   `json:"VersionId"`
	SecretString  string   `json:"SecretString,omitempty"`
	SecretBinary  []byte   `json:"SecretBinary,omitempty"`
	VersionStages []string `json:"VersionStages,omitempty"`
	CreatedDate   float64  `json:"CreatedDate,omitempty"`
}

// batchErrorEntry reports a per-secret failure (e.g. a missing secret) in a
// BatchGetSecretValue response, so one bad id does not fail the whole batch.
type batchErrorEntry struct {
	SecretID  string `json:"SecretId"`
	ErrorCode string `json:"ErrorCode"`
	Message   string `json:"Message"`
}

type batchGetSecretValueResponse struct {
	SecretValues []secretValueEntry `json:"SecretValues"`
	Errors       []batchErrorEntry  `json:"Errors"`
	NextToken    string             `json:"NextToken,omitempty"`
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
// ("arn:aws:secretsmanager:<region>:<account>:secret:<name>-<suffix>") — real
// Secrets Manager accepts both forms for SecretId — and returns the bare name
// the driver keys on. For an ARN, the trailing 6-char "-<suffix>" AWS appends is
// stripped so a lookup by the suffixed ARN resolves to the same secret as the
// friendly name. A plain name is returned untouched (its own hyphens are kept).
func resolveSecretID(id string) string {
	const marker = ":secret:"

	if !strings.HasPrefix(id, "arn:") {
		return id
	}

	i := strings.LastIndex(id, marker)
	if i < 0 {
		return id
	}

	return stripARNSuffix(id[i+len(marker):])
}

// arnSuffixLen is the length of the random suffix (excluding the hyphen) AWS
// appends to a Secrets Manager ARN's resource segment.
const arnSuffixLen = 6

// stripARNSuffix removes a trailing "-XXXXXX" (hyphen + 6 alphanumerics) from an
// ARN resource segment, recovering the friendly secret name.
func stripARNSuffix(seg string) string {
	if len(seg) < arnSuffixLen+1 {
		return seg
	}

	cut := len(seg) - arnSuffixLen - 1
	if seg[cut] != '-' {
		return seg
	}

	for _, c := range seg[cut+1:] {
		if !isAlphaNum(c) {
			return seg
		}
	}

	return seg[:cut]
}

func isAlphaNum(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
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
		KmsKeyID:        info.KMSKeyID,
	}
}
