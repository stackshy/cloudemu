package healthlake

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/healthlake/driver"
)

// tagJSON is the wire shape of a HealthLake tag: an object with PascalCase
// Key/Value members.
type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToWire(tags []driver.Tag) []tagJSON {
	if tags == nil {
		return nil
	}

	out := make([]tagJSON, len(tags))
	for i, t := range tags {
		out[i] = tagJSON{Key: t.Key, Value: t.Value}
	}

	return out
}

func tagsFromWire(tags []tagJSON) []driver.Tag {
	if tags == nil {
		return nil
	}

	out := make([]driver.Tag, len(tags))
	for i, t := range tags {
		out[i] = driver.Tag{Key: t.Key, Value: t.Value}
	}

	return out
}

// epochSeconds renders a driver timestamp as the epoch-seconds number
// HealthLake's wire uses for CreatedAt.
func epochSeconds(t time.Time) int64 {
	return t.Unix()
}

// kmsEncryptionConfigJSON is the wire shape of a KMS encryption config.
type kmsEncryptionConfigJSON struct {
	CmkType  string `json:"CmkType"`
	KmsKeyID string `json:"KmsKeyId,omitempty"`
}

// sseConfigurationJSON is the wire shape of a server-side encryption config.
type sseConfigurationJSON struct {
	KmsEncryptionConfig *kmsEncryptionConfigJSON `json:"KmsEncryptionConfig,omitempty"`
}

func sseToWire(s *driver.SseConfiguration) *sseConfigurationJSON {
	if s == nil {
		return nil
	}

	out := sseConfigurationJSON{}
	if s.KmsEncryptionConfig != nil {
		out.KmsEncryptionConfig = &kmsEncryptionConfigJSON{
			CmkType:  s.KmsEncryptionConfig.CmkType,
			KmsKeyID: s.KmsEncryptionConfig.KmsKeyID,
		}
	}

	return &out
}

func sseFromWire(s *sseConfigurationJSON) *driver.SseConfiguration {
	if s == nil {
		return nil
	}

	out := driver.SseConfiguration{}
	if s.KmsEncryptionConfig != nil {
		out.KmsEncryptionConfig = &driver.KmsEncryptionConfig{
			CmkType:  s.KmsEncryptionConfig.CmkType,
			KmsKeyID: s.KmsEncryptionConfig.KmsKeyID,
		}
	}

	return &out
}

// preloadDataConfigJSON is the wire shape of a preload-data config.
type preloadDataConfigJSON struct {
	PreloadDataType string `json:"PreloadDataType"`
}

func preloadToWire(p *driver.PreloadDataConfig) *preloadDataConfigJSON {
	if p == nil {
		return nil
	}

	return &preloadDataConfigJSON{PreloadDataType: p.PreloadDataType}
}

func preloadFromWire(p *preloadDataConfigJSON) *driver.PreloadDataConfig {
	if p == nil {
		return nil
	}

	return &driver.PreloadDataConfig{PreloadDataType: p.PreloadDataType}
}

// identityProviderConfigJSON is the wire shape of an identity-provider config.
type identityProviderConfigJSON struct {
	AuthorizationStrategy           string `json:"AuthorizationStrategy"`
	FineGrainedAuthorizationEnabled bool   `json:"FineGrainedAuthorizationEnabled,omitempty"`
	IdpLambdaArn                    string `json:"IdpLambdaArn,omitempty"`
	Metadata                        string `json:"Metadata,omitempty"`
}

func identityProviderToWire(i *driver.IdentityProviderConfiguration) *identityProviderConfigJSON {
	if i == nil {
		return nil
	}

	return &identityProviderConfigJSON{
		AuthorizationStrategy:           i.AuthorizationStrategy,
		FineGrainedAuthorizationEnabled: i.FineGrainedAuthorizationEnabled,
		IdpLambdaArn:                    i.IdpLambdaArn,
		Metadata:                        i.Metadata,
	}
}

func identityProviderFromWire(i *identityProviderConfigJSON) *driver.IdentityProviderConfiguration {
	if i == nil {
		return nil
	}

	return &driver.IdentityProviderConfiguration{
		AuthorizationStrategy:           i.AuthorizationStrategy,
		FineGrainedAuthorizationEnabled: i.FineGrainedAuthorizationEnabled,
		IdpLambdaArn:                    i.IdpLambdaArn,
		Metadata:                        i.Metadata,
	}
}

// datastorePropertiesJSON is the wire shape of a DatastoreProperties, shared by
// the describe and list operations.
type datastorePropertiesJSON struct {
	DatastoreID                   string                      `json:"DatastoreId"`
	DatastoreArn                  string                      `json:"DatastoreArn"`
	DatastoreName                 string                      `json:"DatastoreName,omitempty"`
	DatastoreStatus               string                      `json:"DatastoreStatus"`
	DatastoreTypeVersion          string                      `json:"DatastoreTypeVersion"`
	DatastoreEndpoint             string                      `json:"DatastoreEndpoint"`
	CreatedAt                     int64                       `json:"CreatedAt"`
	SseConfiguration              *sseConfigurationJSON       `json:"SseConfiguration,omitempty"`
	PreloadDataConfig             *preloadDataConfigJSON      `json:"PreloadDataConfig,omitempty"`
	IdentityProviderConfiguration *identityProviderConfigJSON `json:"IdentityProviderConfiguration,omitempty"`
}

func toDatastoreProperties(d *driver.Datastore) datastorePropertiesJSON {
	return datastorePropertiesJSON{
		DatastoreID:                   d.DatastoreID,
		DatastoreArn:                  d.DatastoreArn,
		DatastoreName:                 d.DatastoreName,
		DatastoreStatus:               d.DatastoreStatus,
		DatastoreTypeVersion:          d.DatastoreTypeVersion,
		DatastoreEndpoint:             d.DatastoreEndpoint,
		CreatedAt:                     epochSeconds(d.CreatedAt),
		SseConfiguration:              sseToWire(d.SseConfiguration),
		PreloadDataConfig:             preloadToWire(d.PreloadDataConfig),
		IdentityProviderConfiguration: identityProviderToWire(d.IdentityProviderConfiguration),
	}
}
