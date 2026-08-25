package keyvault

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// keyvaultSnapshot is the full serialized state of the Key Vault mock: every
// vault keyed by name, each with its secrets and keys. The defaultVault's
// stores alias the shared Secrets-driver stores (m.secrets/m.keys); Restore
// re-populates them through m.vault(name), which preserves that aliasing.
type keyvaultSnapshot struct {
	Vaults map[string]*vaultSnapshot `json:"vaults,omitempty"`
}

type vaultSnapshot struct {
	Secrets map[string]*secretDataSnapshot `json:"secrets,omitempty"`
	Keys    map[string]*keyDataSnapshot    `json:"keys,omitempty"`
}

type secretDataSnapshot struct {
	Info           driver.SecretInfo       `json:"info"`
	Versions       []secretVersionSnapshot `json:"versions,omitempty"`
	DeletedAt      time.Time               `json:"deletedAt,omitempty"`
	ScheduledPurge time.Time               `json:"scheduledPurge,omitempty"`
}

// secretVersionSnapshot mirrors the unexported secretVersion, promoting its
// fields so they survive JSON.
type secretVersionSnapshot struct {
	VersionID   string            `json:"versionId"`
	Value       []byte            `json:"value,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Enabled     bool              `json:"enabled,omitempty"`
	Expires     int64             `json:"expires,omitempty"`
	NotBefore   int64             `json:"notBefore,omitempty"`
	Created     time.Time         `json:"created,omitempty"`
	Updated     time.Time         `json:"updated,omitempty"`
	Current     bool              `json:"current,omitempty"`
}

type keyDataSnapshot struct {
	Name           string                   `json:"name"`
	Versions       []keyVersionSnapshot     `json:"versions,omitempty"`
	DeletedAt      time.Time                `json:"deletedAt,omitempty"`
	ScheduledPurge time.Time                `json:"scheduledPurge,omitempty"`
	RotationPolicy *driver.KVRotationPolicy `json:"rotationPolicy,omitempty"`
}

// keyVersionSnapshot mirrors the unexported keyVersion. The real private key
// material is serialized in a portable DER form: RSA as PKCS#1, EC as SEC1,
// oct as raw bytes.
type keyVersionSnapshot struct {
	VersionID string            `json:"versionId"`
	Kty       string            `json:"kty,omitempty"`
	Curve     string            `json:"curve,omitempty"`
	KeyOps    []string          `json:"keyOps,omitempty"`
	RSAKey    []byte            `json:"rsaKey,omitempty"`
	ECKey     []byte            `json:"ecKey,omitempty"`
	OctKey    []byte            `json:"octKey,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	Enabled   bool              `json:"enabled,omitempty"`
	Expires   int64             `json:"expires,omitempty"`
	NotBefore int64             `json:"notBefore,omitempty"`
	Created   time.Time         `json:"created,omitempty"`
	Updated   time.Time         `json:"updated,omitempty"`
	Current   bool              `json:"current,omitempty"`
	Managed   bool              `json:"managed,omitempty"`
}

// Snapshot captures every vault's secrets and keys as JSON. includeAssets is
// unused — a secret or key without its material cannot be restored usefully, so
// it is always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := keyvaultSnapshot{Vaults: make(map[string]*vaultSnapshot, m.vaults.Len())}

	for name, vd := range m.vaults.All() {
		vs := &vaultSnapshot{
			Secrets: make(map[string]*secretDataSnapshot, vd.secrets.Len()),
			Keys:    make(map[string]*keyDataSnapshot, vd.keys.Len()),
		}

		for sName, sd := range vd.secrets.All() {
			vs.Secrets[sName] = snapshotSecret(sd)
		}

		for kName, kd := range vd.keys.All() {
			ks, err := snapshotKey(kd)
			if err != nil {
				return nil, err
			}

			vs.Keys[kName] = ks
		}

		snap.Vaults[name] = vs
	}

	return json.Marshal(snap)
}

func snapshotSecret(sd *secretData) *secretDataSnapshot {
	sd.mu.RLock()
	defer sd.mu.RUnlock()

	ss := &secretDataSnapshot{
		Info: sd.info, DeletedAt: sd.deletedAt, ScheduledPurge: sd.scheduledPurge,
	}

	for i := range sd.versions {
		v := &sd.versions[i]
		ss.Versions = append(ss.Versions, secretVersionSnapshot{
			VersionID: v.versionID, Value: copyBytes(v.value), ContentType: v.contentType,
			Tags: copyTags(v.tags), Enabled: v.enabled, Expires: v.expires, NotBefore: v.notBefore,
			Created: v.created, Updated: v.updated, Current: v.current,
		})
	}

	return ss
}

func snapshotKey(kd *keyData) (*keyDataSnapshot, error) {
	kd.mu.RLock()
	defer kd.mu.RUnlock()

	ks := &keyDataSnapshot{
		Name: kd.name, DeletedAt: kd.deletedAt, ScheduledPurge: kd.scheduledPurge,
		RotationPolicy: kd.rotationPolicy,
	}

	for i := range kd.versions {
		v := &kd.versions[i]

		kvs := keyVersionSnapshot{
			VersionID: v.versionID, Kty: v.kty, Curve: v.curve, KeyOps: v.keyOps,
			OctKey: copyBytes(v.octKey), Tags: copyTags(v.tags), Enabled: v.enabled,
			Expires: v.expires, NotBefore: v.notBefore, Created: v.created, Updated: v.updated,
			Current: v.current, Managed: v.managed,
		}

		if v.rsaKey != nil {
			kvs.RSAKey = x509.MarshalPKCS1PrivateKey(v.rsaKey)
		}

		if v.ecKey != nil {
			der, err := x509.MarshalECPrivateKey(v.ecKey)
			if err != nil {
				return nil, fmt.Errorf("keyvault: marshal EC key %q: %w", kd.name, err)
			}

			kvs.ECKey = der
		}

		ks.Versions = append(ks.Versions, kvs)
	}

	return ks, nil
}

// Restore rebuilds every vault's secrets and keys under their original names and
// version ids. The default vault is repopulated through m.vault(defaultVault),
// which returns the aliased shared stores so the portable Secrets driver sees
// the restored secrets.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap keyvaultSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("keyvault: parse snapshot: %w", err)
	}

	for vaultName, vs := range snap.Vaults {
		vd := m.vault(vaultName)

		for name, ss := range vs.Secrets {
			vd.secrets.Set(name, restoreSecret(ss))
		}

		for name, ks := range vs.Keys {
			kd, err := restoreKey(ks)
			if err != nil {
				return err
			}

			vd.keys.Set(name, kd)
		}
	}

	return nil
}

func restoreSecret(ss *secretDataSnapshot) *secretData {
	sd := &secretData{
		info: ss.Info, deletedAt: ss.DeletedAt, scheduledPurge: ss.ScheduledPurge,
	}

	for i := range ss.Versions {
		v := &ss.Versions[i]
		sd.versions = append(sd.versions, secretVersion{
			versionID: v.VersionID, value: copyBytes(v.Value), contentType: v.ContentType,
			tags: copyTags(v.Tags), enabled: v.Enabled, expires: v.Expires, notBefore: v.NotBefore,
			created: v.Created, updated: v.Updated, current: v.Current,
		})
	}

	return sd
}

func restoreKey(ks *keyDataSnapshot) (*keyData, error) {
	kd := &keyData{
		name: ks.Name, deletedAt: ks.DeletedAt, scheduledPurge: ks.ScheduledPurge,
		rotationPolicy: ks.RotationPolicy,
	}

	for i := range ks.Versions {
		v := &ks.Versions[i]

		kv := keyVersion{
			versionID: v.VersionID, kty: v.Kty, curve: v.Curve, keyOps: v.KeyOps,
			octKey: copyBytes(v.OctKey), tags: copyTags(v.Tags), enabled: v.Enabled,
			expires: v.Expires, notBefore: v.NotBefore, created: v.Created, updated: v.Updated,
			current: v.Current, managed: v.Managed,
		}

		if len(v.RSAKey) > 0 {
			key, err := x509.ParsePKCS1PrivateKey(v.RSAKey)
			if err != nil {
				return nil, fmt.Errorf("keyvault: parse RSA key %q: %w", ks.Name, err)
			}

			kv.rsaKey = key
		}

		if len(v.ECKey) > 0 {
			key, err := x509.ParseECPrivateKey(v.ECKey)
			if err != nil {
				return nil, fmt.Errorf("keyvault: parse EC key %q: %w", ks.Name, err)
			}

			kv.ecKey = key
		}

		kd.versions = append(kd.versions, kv)
	}

	return kd, nil
}
