package vault

import (
	vaultprovider "github.com/stackshy/cloudemu/v2/providers/oci/vault"
)

// VaultManagement is the KMS vault surface.
//
// container above a secret at all, so every vault operation lives here.
//
//nolint:revive // Management alone would not distinguish it from the key and secret surfaces. The portable secrets driver has no
type VaultManagement interface {
	CreateVault(spec *vaultprovider.VaultSpec) (*vaultprovider.VaultInfo, error)
	GetVault(id string) (*vaultprovider.VaultInfo, error)
	ListVaults(compartmentID string) ([]vaultprovider.VaultInfo, error)
	UpdateVault(id string, upd vaultprovider.Update) (*vaultprovider.VaultInfo, error)
	ScheduleVaultDeletion(id, at string) (*vaultprovider.VaultInfo, error)
	CancelVaultDeletion(id string) (*vaultprovider.VaultInfo, error)
	ChangeVaultCompartment(id, compartmentID string) error
	VaultCompartment(id string) string
}

// KeyManagement is the master encryption key surface. OCI Vault carries key
// management alongside secret storage; no other cloud in this repo puts the
// two behind one service, so the portable driver models neither keys nor the
// rotation that minting a key version performs.
type KeyManagement interface {
	CreateKey(spec *vaultprovider.KeySpec) (*vaultprovider.KeyInfo, error)
	GetKey(id string) (*vaultprovider.KeyInfo, error)
	ListKeys(compartmentID, vaultID string) ([]vaultprovider.KeyInfo, error)
	UpdateKey(id string, upd vaultprovider.Update) (*vaultprovider.KeyInfo, error)
	ScheduleKeyDeletion(id, at string) (*vaultprovider.KeyInfo, error)
	CancelKeyDeletion(id string) (*vaultprovider.KeyInfo, error)
	ChangeKeyCompartment(id, compartmentID string) error
	KeyCompartment(id string) string

	CreateKeyVersion(keyID string) (*vaultprovider.KeyVersionInfo, error)
	GetKeyVersion(keyID, versionID string) (*vaultprovider.KeyVersionInfo, error)
	ListKeyVersions(keyID string) ([]vaultprovider.KeyVersionInfo, error)
}

// SecretManagement is the OCI-shaped secret surface. The portable driver keys
// secrets by name, lists them unscoped, deletes them outright and gives a
// version nothing but an identifier; OCI addresses secrets by OCID, scopes
// them to a compartment and a vault, only ever schedules a deletion, and
// stages each version CURRENT, PENDING, PREVIOUS or DEPRECATED.
type SecretManagement interface {
	CreateOCISecret(spec *vaultprovider.SecretSpec) (*vaultprovider.SecretInfo, error)
	GetOCISecret(id string) (*vaultprovider.SecretInfo, error)
	GetOCISecretByName(vaultID, name string) (*vaultprovider.SecretInfo, error)
	ListOCISecrets(compartmentID, vaultID, name string) ([]vaultprovider.SecretInfo, error)
	UpdateOCISecret(id string, upd *vaultprovider.SecretUpdate) (*vaultprovider.SecretInfo, error)
	ScheduleOCISecretDeletion(id, at string) (*vaultprovider.SecretInfo, error)
	CancelOCISecretDeletion(id string) (*vaultprovider.SecretInfo, error)
	ChangeSecretCompartment(id, compartmentID string) error
	SecretCompartment(id string) string

	ListOCISecretVersions(secretID string) ([]vaultprovider.SecretVersionInfo, error)
	GetOCISecretVersion(secretID string, number int64) (*vaultprovider.SecretVersionInfo, error)
	ScheduleSecretVersionDeletion(secretID string, number int64, at string) (*vaultprovider.SecretVersionInfo, error)
	CancelSecretVersionDeletion(secretID string, number int64) (*vaultprovider.SecretVersionInfo, error)

	GetSecretBundle(secretID string, sel vaultprovider.BundleSelector) (*vaultprovider.SecretBundle, error)
	GetSecretBundleByName(vaultID, name string, sel vaultprovider.BundleSelector) (*vaultprovider.SecretBundle, error)
	ListSecretBundleVersions(secretID string) ([]vaultprovider.SecretVersionInfo, error)
}

// Extras is everything OCI Vault does that the portable secrets driver's seven
// operations cannot express. *providers/oci/vault.Mock satisfies it; any
// driver that does not is served 501 for every path this handler claims.
type Extras interface {
	VaultManagement
	KeyManagement
	SecretManagement
}
