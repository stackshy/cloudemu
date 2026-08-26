package keyvault

import (
	"encoding/base64"
	"net/http"

	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// keyAttributesJSON is the Key Vault KeyAttributes shape. Timestamps are Unix
// epoch seconds; exp and nbf are omitted when unset.
type keyAttributesJSON struct {
	Enabled         bool   `json:"enabled"`
	Created         int64  `json:"created,omitempty"`
	Updated         int64  `json:"updated,omitempty"`
	Expires         int64  `json:"exp,omitempty"`
	NotBefore       int64  `json:"nbf,omitempty"`
	RecoverableDays int    `json:"recoverableDays,omitempty"`
	RecoveryLevel   string `json:"recoveryLevel,omitempty"`
}

// jsonWebKey is the JWK carried in a KeyBundle. Byte components are base64url
// (no padding) strings; empty components are omitted.
type jsonWebKey struct {
	KID    string   `json:"kid,omitempty"`
	Kty    string   `json:"kty"`
	KeyOps []string `json:"key_ops,omitempty"`
	Crv    string   `json:"crv,omitempty"`
	N      string   `json:"n,omitempty"`
	E      string   `json:"e,omitempty"`
	X      string   `json:"x,omitempty"`
	Y      string   `json:"y,omitempty"`
}

// keyBundleJSON is a full Key Vault key bundle.
type keyBundleJSON struct {
	Key        jsonWebKey        `json:"key"`
	Attributes keyAttributesJSON `json:"attributes"`
	Tags       map[string]string `json:"tags,omitempty"`
	Managed    *bool             `json:"managed,omitempty"`
}

// deletedKeyBundleJSON extends a bundle with soft-delete scheduling.
type deletedKeyBundleJSON struct {
	keyBundleJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

// keyItemJSON is a list entry: identifier, attributes and metadata, no JWK.
type keyItemJSON struct {
	KID        string            `json:"kid"`
	Attributes keyAttributesJSON `json:"attributes"`
	Tags       map[string]string `json:"tags,omitempty"`
	Managed    *bool             `json:"managed,omitempty"`
}

// deletedKeyItemJSON is a deleted-list entry.
type deletedKeyItemJSON struct {
	keyItemJSON

	RecoveryID         string `json:"recoveryId"`
	DeletedDate        int64  `json:"deletedDate,omitempty"`
	ScheduledPurgeDate int64  `json:"scheduledPurgeDate,omitempty"`
}

type keyListResponseJSON struct {
	Value    []keyItemJSON `json:"value"`
	NextLink *string       `json:"nextLink"`
}

type deletedKeyListResponseJSON struct {
	Value    []deletedKeyItemJSON `json:"value"`
	NextLink *string              `json:"nextLink"`
}

// setKeyAttributesJSON is the attributes sub-object of a create/import/update
// request. Pointers distinguish "absent" from "zero".
type setKeyAttributesJSON struct {
	Enabled   *bool  `json:"enabled"`
	Expires   *int64 `json:"exp"`
	NotBefore *int64 `json:"nbf"`
}

type createKeyRequest struct {
	Kty            string                `json:"kty"`
	KeySize        int                   `json:"key_size"`
	Crv            string                `json:"crv"`
	PublicExponent int                   `json:"public_exponent"`
	KeyOps         []string              `json:"key_ops"`
	Tags           map[string]string     `json:"tags"`
	KeyAttributes  *setKeyAttributesJSON `json:"attributes"`
}

type importKeyRequest struct {
	Key           jsonWebKeyImport      `json:"key"`
	HSM           bool                  `json:"Hsm"`
	Tags          map[string]string     `json:"tags"`
	KeyAttributes *setKeyAttributesJSON `json:"attributes"`
}

// jsonWebKeyImport is the inbound JWK for ImportKey; all byte components are
// base64url strings.
type jsonWebKeyImport struct {
	Kty    string   `json:"kty"`
	Crv    string   `json:"crv"`
	KeyOps []string `json:"key_ops"`
	N      string   `json:"n"`
	E      string   `json:"e"`
	D      string   `json:"d"`
	P      string   `json:"p"`
	Q      string   `json:"q"`
	DP     string   `json:"dp"`
	DQ     string   `json:"dq"`
	QI     string   `json:"qi"`
	X      string   `json:"x"`
	Y      string   `json:"y"`
	K      string   `json:"k"`
}

type updateKeyRequest struct {
	KeyOps        []string              `json:"key_ops"`
	Tags          map[string]string     `json:"tags"`
	KeyAttributes *setKeyAttributesJSON `json:"attributes"`
}

// keyOperationRequest is the encrypt/decrypt/wrapKey/unwrapKey/sign payload.
type keyOperationRequest struct {
	Alg   string `json:"alg"`
	Value string `json:"value"`
}

// verifyRequest is the verify payload.
type verifyRequest struct {
	Alg    string `json:"alg"`
	Digest string `json:"digest"`
	Value  string `json:"value"`
}

// keyOperationResultJSON is the encrypt/decrypt/wrap/unwrap/sign response.
type keyOperationResultJSON struct {
	KID   string `json:"kid"`
	Value string `json:"value"`
}

// keyVerifyResultJSON is the verify response.
type keyVerifyResultJSON struct {
	Value bool `json:"value"`
}

// keyRotationPolicyActionJSON is a lifetime action's action type ("Rotate" or
// "Notify").
type keyRotationPolicyActionJSON struct {
	Type string `json:"type"`
}

// keyRotationPolicyTriggerJSON is a lifetime action's trigger condition,
// exactly one of which is set: an ISO 8601 duration after key creation, or
// before the key's expiry.
type keyRotationPolicyTriggerJSON struct {
	TimeAfterCreate  string `json:"timeAfterCreate,omitempty"`
	TimeBeforeExpiry string `json:"timeBeforeExpiry,omitempty"`
}

// lifetimeActionJSON pairs a trigger with the action it fires.
type lifetimeActionJSON struct {
	Trigger keyRotationPolicyTriggerJSON `json:"trigger"`
	Action  keyRotationPolicyActionJSON  `json:"action"`
}

// keyRotationPolicyAttributesJSON is the rotation policy's attributes:
// the expiry ISO 8601 duration applied to new versions, plus the policy's own
// created/updated timestamps (Unix epoch seconds).
type keyRotationPolicyAttributesJSON struct {
	ExpiryTime string `json:"expiryTime,omitempty"`
	Created    int64  `json:"created,omitempty"`
	Updated    int64  `json:"updated,omitempty"`
}

// keyRotationPolicyJSON is the GET/PUT .../rotationpolicy request and
// response body.
type keyRotationPolicyJSON struct {
	ID              string                          `json:"id,omitempty"`
	LifetimeActions []lifetimeActionJSON            `json:"lifetimeActions,omitempty"`
	Attributes      keyRotationPolicyAttributesJSON `json:"attributes"`
}

// keyRotationPolicyID builds "{vault}/keys/{name}/rotationpolicy".
func keyRotationPolicyID(r *http.Request, name string) string {
	return vaultBaseURL(r) + keysPrefix + "/" + name + "/" + rotationPolicySeg
}

func toRotationPolicyJSON(r *http.Request, name string, p *secretsdriver.KVRotationPolicy) keyRotationPolicyJSON {
	out := keyRotationPolicyJSON{
		ID: keyRotationPolicyID(r, name),
		Attributes: keyRotationPolicyAttributesJSON{
			ExpiryTime: p.ExpiryTime,
			Created:    p.Created,
			Updated:    p.Updated,
		},
	}

	for _, la := range p.LifetimeActions {
		out.LifetimeActions = append(out.LifetimeActions, lifetimeActionJSON{
			Trigger: keyRotationPolicyTriggerJSON{
				TimeAfterCreate:  la.Trigger.TimeAfterCreate,
				TimeBeforeExpiry: la.Trigger.TimeBeforeExpiry,
			},
			Action: keyRotationPolicyActionJSON{Type: la.Action.Type},
		})
	}

	return out
}

func fromRotationPolicyJSON(in *keyRotationPolicyJSON) secretsdriver.KVRotationPolicy {
	out := secretsdriver.KVRotationPolicy{ExpiryTime: in.Attributes.ExpiryTime}

	for _, la := range in.LifetimeActions {
		out.LifetimeActions = append(out.LifetimeActions, secretsdriver.KVLifetimeAction{
			Trigger: secretsdriver.KVRotationPolicyTrigger{
				TimeAfterCreate:  la.Trigger.TimeAfterCreate,
				TimeBeforeExpiry: la.Trigger.TimeBeforeExpiry,
			},
			Action: secretsdriver.KVRotationPolicyAction{Type: la.Action.Type},
		})
	}

	return out
}

// keyID builds "{vault}/keys/{name}[/{version}]".
func keyID(r *http.Request, name, version string) string {
	id := vaultBaseURL(r) + keysPrefix + "/" + name
	if version != "" {
		id += "/" + version
	}

	return id
}

func deletedKeyID(r *http.Request, name string) string {
	return vaultBaseURL(r) + deletedKeysPrefix + "/" + name
}

func keyAttributesOf(k *secretsdriver.KVKey) keyAttributesJSON {
	return keyAttributesJSON{
		Enabled:         k.Enabled,
		Created:         k.Created,
		Updated:         k.Updated,
		Expires:         k.Expires,
		NotBefore:       k.NotBefore,
		RecoverableDays: recoverableDays,
		RecoveryLevel:   recoveryLevel,
	}
}

func toJWK(r *http.Request, k *secretsdriver.KVKey) jsonWebKey {
	jwk := jsonWebKey{
		KID:    keyID(r, k.Name, k.Version),
		Kty:    k.Kty,
		KeyOps: k.KeyOps,
		Crv:    k.Curve,
	}

	if len(k.N) > 0 {
		jwk.N = base64.RawURLEncoding.EncodeToString(k.N)
	}

	if len(k.E) > 0 {
		jwk.E = base64.RawURLEncoding.EncodeToString(k.E)
	}

	if len(k.X) > 0 {
		jwk.X = base64.RawURLEncoding.EncodeToString(k.X)
	}

	if len(k.Y) > 0 {
		jwk.Y = base64.RawURLEncoding.EncodeToString(k.Y)
	}

	return jwk
}

func toKeyBundle(r *http.Request, k *secretsdriver.KVKey) keyBundleJSON {
	return keyBundleJSON{
		Key:        toJWK(r, k),
		Attributes: keyAttributesOf(k),
		Tags:       k.Tags,
		Managed:    managedPtr(k.Managed),
	}
}

func toDeletedKeyBundle(r *http.Request, d *secretsdriver.KVDeletedKey) deletedKeyBundleJSON {
	return deletedKeyBundleJSON{
		keyBundleJSON:      toKeyBundle(r, &d.KVKey),
		RecoveryID:         deletedKeyID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}

func toKeyItem(r *http.Request, k *secretsdriver.KVKey) keyItemJSON {
	return keyItemJSON{
		KID:        keyID(r, k.Name, k.Version),
		Attributes: keyAttributesOf(k),
		Tags:       k.Tags,
		Managed:    managedPtr(k.Managed),
	}
}

func toDeletedKeyItem(r *http.Request, d *secretsdriver.KVDeletedKey) deletedKeyItemJSON {
	return deletedKeyItemJSON{
		keyItemJSON:        toKeyItem(r, &d.KVKey),
		RecoveryID:         deletedKeyID(r, d.Name),
		DeletedDate:        d.DeletedDate,
		ScheduledPurgeDate: d.ScheduledPurgeDate,
	}
}
