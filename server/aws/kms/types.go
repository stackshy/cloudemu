package kms

import (
	"time"

	kmsdriver "github.com/stackshy/cloudemu/v2/services/kms/driver"
)

// tag is the KMS wire shape for a tag ({TagKey, TagValue}).
type tag struct {
	TagKey   string `json:"TagKey"`
	TagValue string `json:"TagValue"`
}

func tagsToMap(tags []tag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.TagKey] = t.TagValue
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{TagKey: k, TagValue: v})
	}

	return out
}

// epochOrNil renders a time as KMS's epoch-seconds number, or nil when zero so
// the field is omitted.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// mrkKeyRef is a {Arn, Region} entry in MultiRegionConfiguration.
type mrkKeyRef struct {
	Arn    string `json:"Arn,omitempty"`
	Region string `json:"Region,omitempty"`
}

// multiRegionConfigJSON is the KMS MultiRegionConfiguration wire shape.
type multiRegionConfigJSON struct {
	MultiRegionKeyType string      `json:"MultiRegionKeyType,omitempty"`
	PrimaryKey         *mrkKeyRef  `json:"PrimaryKey,omitempty"`
	ReplicaKeys        []mrkKeyRef `json:"ReplicaKeys,omitempty"`
}

// keyMetadataJSON is the KMS KeyMetadata wire shape.
type keyMetadataJSON struct {
	KeyID                    string                 `json:"KeyId"`
	Arn                      string                 `json:"Arn"`
	AWSAccountID             string                 `json:"AWSAccountId"`
	Description              string                 `json:"Description,omitempty"`
	Enabled                  bool                   `json:"Enabled"`
	KeyUsage                 string                 `json:"KeyUsage"`
	KeyState                 string                 `json:"KeyState"`
	KeySpec                  string                 `json:"KeySpec"`
	CustomerMasterKeySpec    string                 `json:"CustomerMasterKeySpec"`
	Origin                   string                 `json:"Origin"`
	KeyManager               string                 `json:"KeyManager"`
	MultiRegion              bool                   `json:"MultiRegion"`
	CreationDate             *float64               `json:"CreationDate,omitempty"`
	DeletionDate             *float64               `json:"DeletionDate,omitempty"`
	ValidTo                  *float64               `json:"ValidTo,omitempty"`
	EncryptionAlgorithms     []string               `json:"EncryptionAlgorithms,omitempty"`
	MultiRegionConfiguration *multiRegionConfigJSON `json:"MultiRegionConfiguration,omitempty"`
}

func metadataJSON(md *kmsdriver.KeyMetadata) keyMetadataJSON {
	out := keyMetadataJSON{
		KeyID:                 md.KeyID,
		Arn:                   md.ARN,
		AWSAccountID:          md.AWSAccountID,
		Description:           md.Description,
		Enabled:               md.Enabled,
		KeyUsage:              md.KeyUsage,
		KeyState:              md.KeyState,
		KeySpec:               md.KeySpec,
		CustomerMasterKeySpec: md.KeySpec,
		Origin:                md.Origin,
		KeyManager:            md.KeyManager,
		MultiRegion:           md.MultiRegion,
		CreationDate:          epochOrNil(md.CreationDate),
		DeletionDate:          epochOrNil(md.DeletionDate),
		ValidTo:               epochOrNil(md.ValidTo),
	}

	// A symmetric ENCRYPT_DECRYPT key advertises the single SYMMETRIC_DEFAULT
	// encryption algorithm, matching real KMS KeyMetadata.
	if md.KeySpec == kmsdriver.SpecSymmetricDefault && md.KeyUsage == kmsdriver.UsageEncryptDecrypt {
		out.EncryptionAlgorithms = []string{kmsdriver.SpecSymmetricDefault}
	}

	if md.MultiRegion {
		cfg := &multiRegionConfigJSON{MultiRegionKeyType: md.MultiRegionKeyType}
		if md.PrimaryRegion != "" {
			cfg.PrimaryKey = &mrkKeyRef{Region: md.PrimaryRegion}
		}

		for _, rr := range md.ReplicaRegions {
			cfg.ReplicaKeys = append(cfg.ReplicaKeys, mrkKeyRef{Region: rr})
		}

		out.MultiRegionConfiguration = cfg
	}

	return out
}

// --- request shapes ---

type createKeyRequest struct {
	Description           string `json:"Description"`
	KeyUsage              string `json:"KeyUsage"`
	KeySpec               string `json:"KeySpec"`
	CustomerMasterKeySpec string `json:"CustomerMasterKeySpec"`
	Origin                string `json:"Origin"`
	MultiRegion           bool   `json:"MultiRegion"`
	Policy                string `json:"Policy"`
	Tags                  []tag  `json:"Tags"`
}

type keyIDRequest struct {
	KeyID string `json:"KeyId"`
}

type updateKeyDescriptionRequest struct {
	KeyID       string `json:"KeyId"`
	Description string `json:"Description"`
}

type scheduleKeyDeletionRequest struct {
	KeyID             string `json:"KeyId"`
	PendingWindowDays int32  `json:"PendingWindowInDays"`
}

type aliasRequest struct {
	AliasName   string `json:"AliasName"`
	TargetKeyID string `json:"TargetKeyId"`
}

type listKeysRequest struct {
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type listAliasesRequest struct {
	KeyID  string `json:"KeyId"`
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type listResourceTagsRequest struct {
	KeyID  string `json:"KeyId"`
	Limit  int32  `json:"Limit"`
	Marker string `json:"Marker"`
}

type tagResourceRequest struct {
	KeyID string `json:"KeyId"`
	Tags  []tag  `json:"Tags"`
}

type untagResourceRequest struct {
	KeyID   string   `json:"KeyId"`
	TagKeys []string `json:"TagKeys"`
}

// --- response shapes ---

type keyMetadataResponse struct {
	KeyMetadata keyMetadataJSON `json:"KeyMetadata"`
}

type keyListEntry struct {
	KeyID  string `json:"KeyId"`
	KeyArn string `json:"KeyArn"`
}

type listKeysResponse struct {
	Keys       []keyListEntry `json:"Keys"`
	NextMarker string         `json:"NextMarker,omitempty"`
	Truncated  bool           `json:"Truncated"`
}

type scheduleKeyDeletionResponse struct {
	KeyID        string   `json:"KeyId"`
	DeletionDate *float64 `json:"DeletionDate,omitempty"`
	KeyState     string   `json:"KeyState"`
}

type cancelKeyDeletionResponse struct {
	KeyID string `json:"KeyId"`
}

type aliasListEntry struct {
	AliasName       string   `json:"AliasName"`
	AliasArn        string   `json:"AliasArn"`
	TargetKeyID     string   `json:"TargetKeyId"`
	CreationDate    *float64 `json:"CreationDate,omitempty"`
	LastUpdatedDate *float64 `json:"LastUpdatedDate,omitempty"`
}

type listAliasesResponse struct {
	Aliases    []aliasListEntry `json:"Aliases"`
	NextMarker string           `json:"NextMarker,omitempty"`
	Truncated  bool             `json:"Truncated"`
}

type listResourceTagsResponse struct {
	Tags       []tag  `json:"Tags"`
	NextMarker string `json:"NextMarker,omitempty"`
	Truncated  bool   `json:"Truncated"`
}
