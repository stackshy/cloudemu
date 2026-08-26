package keyvault

import (
	"net/http"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// recoverableDays and recoveryLevel describe the soft-delete retention Key
// Vault advertises on every secret's read-only attributes.
const (
	recoverableDays = 90
	recoveryLevel   = "Recoverable+Purgeable"
)

// attributesJSON is the Key Vault SecretAttributes shape. Timestamps are Unix
// epoch seconds; exp and nbf are omitted when unset.
type attributesJSON struct {
	Enabled         bool   `json:"enabled"`
	Created         int64  `json:"created,omitempty"`
	Updated         int64  `json:"updated,omitempty"`
	Expires         int64  `json:"exp,omitempty"`
	NotBefore       int64  `json:"nbf,omitempty"`
	RecoverableDays int    `json:"recoverableDays,omitempty"`
	RecoveryLevel   string `json:"recoveryLevel,omitempty"`
}

// setSecretAttributesJSON is the attributes sub-object of a SetSecret /
// UpdateSecretProperties request. Pointers distinguish "absent" from "zero".
type setSecretAttributesJSON struct {
	Enabled   *bool  `json:"enabled"`
	Expires   *int64 `json:"exp"`
	NotBefore *int64 `json:"nbf"`
}

// secretBundleJSON is a full Key Vault secret bundle: value, id and attributes.
type secretBundleJSON struct {
	Value       string            `json:"value"`
	ID          string            `json:"id"`
	ContentType string            `json:"contentType,omitempty"`
	Attributes  attributesJSON    `json:"attributes"`
	Tags        map[string]string `json:"tags,omitempty"`
	Managed     *bool             `json:"managed,omitempty"`
}

// secretItemJSON is a list entry: identifier, attributes and metadata, no value.
type secretItemJSON struct {
	ID          string            `json:"id"`
	ContentType string            `json:"contentType,omitempty"`
	Attributes  attributesJSON    `json:"attributes"`
	Tags        map[string]string `json:"tags,omitempty"`
	Managed     *bool             `json:"managed,omitempty"`
}

type listResponseJSON struct {
	Value    []secretItemJSON `json:"value"`
	NextLink *string          `json:"nextLink"`
}

// deletedSecretBundleJSON extends a bundle with soft-delete scheduling.
type deletedSecretBundleJSON struct {
	secretBundleJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

// deletedSecretItemJSON is a deleted-list entry.
type deletedSecretItemJSON struct {
	secretItemJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

type deletedListResponseJSON struct {
	Value    []deletedSecretItemJSON `json:"value"`
	NextLink *string                 `json:"nextLink"`
}

type backupResultJSON struct {
	Value string `json:"value"`
}

type restoreRequest struct {
	Value string `json:"value"`
}

type setSecretRequest struct {
	Value            string                   `json:"value"`
	ContentType      string                   `json:"contentType"`
	Tags             map[string]string        `json:"tags"`
	SecretAttributes *setSecretAttributesJSON `json:"attributes"`
}

type updateSecretRequest struct {
	ContentType      *string                  `json:"contentType"`
	Tags             map[string]string        `json:"tags"`
	SecretAttributes *setSecretAttributesJSON `json:"attributes"`
}

// vaultBaseURL reconstructs the vault base URL from the request so identifiers
// point back at this server.
func vaultBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}

// secretID builds "{vault}/secrets/{name}[/{version}]".
func secretID(r *http.Request, name, version string) string {
	id := vaultBaseURL(r) + pathPrefix + "/" + name
	if version != "" {
		id += "/" + version
	}

	return id
}

func attributesOf(kv *secretsdriver.KVSecret) attributesJSON {
	return attributesJSON{
		Enabled:         kv.Enabled,
		Created:         kv.Created,
		Updated:         kv.Updated,
		Expires:         kv.Expires,
		NotBefore:       kv.NotBefore,
		RecoverableDays: recoverableDays,
		RecoveryLevel:   recoveryLevel,
	}
}

func toBundle(r *http.Request, kv *secretsdriver.KVSecret) secretBundleJSON {
	return secretBundleJSON{
		Value:       string(kv.Value),
		ID:          secretID(r, kv.Name, kv.Version),
		ContentType: kv.ContentType,
		Attributes:  attributesOf(kv),
		Tags:        kv.Tags,
		Managed:     managedPtr(kv.Managed),
	}
}

func toItem(r *http.Request, kv *secretsdriver.KVSecret) secretItemJSON {
	return secretItemJSON{
		ID:          secretID(r, kv.Name, kv.Version),
		ContentType: kv.ContentType,
		Attributes:  attributesOf(kv),
		Tags:        kv.Tags,
		Managed:     managedPtr(kv.Managed),
	}
}

// managedPtr returns a pointer to true when managed, nil otherwise, so the
// "managed" field is emitted only for objects Key Vault manages (the addressable
// secret and key created alongside a certificate).
func managedPtr(managed bool) *bool {
	if !managed {
		return nil
	}

	t := true

	return &t
}

func toDeletedBundle(r *http.Request, d *secretsdriver.KVDeletedSecret) deletedSecretBundleJSON {
	return deletedSecretBundleJSON{
		secretBundleJSON:   toBundle(r, &d.KVSecret),
		RecoveryID:         deletedID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}

func toDeletedItem(r *http.Request, d *secretsdriver.KVDeletedSecret) deletedSecretItemJSON {
	return deletedSecretItemJSON{
		secretItemJSON:     toItem(r, &d.KVSecret),
		RecoveryID:         deletedID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}

func deletedID(r *http.Request, name string) string {
	return vaultBaseURL(r) + deletedPrefix + "/" + name
}
