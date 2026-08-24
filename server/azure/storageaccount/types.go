package storageaccount

// armAccountCreate is the subset of the ARM storage-account create body the
// emulator reads. armstorage's AccountCreateParameters marshals to these JSON
// field names.
type armAccountCreate struct {
	Location   string                 `json:"location,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Tags       map[string]string      `json:"tags,omitempty"`
	SKU        *armSKU                `json:"sku,omitempty"`
	Properties *armAccountCreateProps `json:"properties,omitempty"`
}

type armAccountCreateProps struct {
	AccessTier string `json:"accessTier,omitempty"`
}

// armSKU is the ARM sku shape (sku.name / sku.tier).
type armSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

// armAccount is the ARM storage-account wire shape returned on create/get.
type armAccount struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *armSKU           `json:"sku,omitempty"`
	Properties *armAccountProps  `json:"properties,omitempty"`
}

// armAccountList is the {"value":[…]} envelope a storage-account list returns.
type armAccountList struct {
	Value []armAccount `json:"value"`
}

type armAccountProps struct {
	AccessTier        string         `json:"accessTier,omitempty"`
	ProvisioningState string         `json:"provisioningState,omitempty"`
	PrimaryEndpoints  *armEndpoints  `json:"primaryEndpoints,omitempty"`
	PrimaryLocation   string         `json:"primaryLocation,omitempty"`
	StatusOfPrimary   string         `json:"statusOfPrimary,omitempty"`
	CreationTime      string         `json:"creationTime,omitempty"`
	Encryption        *armEncryption `json:"encryption,omitempty"`
}

// armEndpoints is the primaryEndpoints object (blob/queue/table/file service
// URLs) on a storage account's properties.
type armEndpoints struct {
	Blob  string `json:"blob,omitempty"`
	Queue string `json:"queue,omitempty"`
	Table string `json:"table,omitempty"`
	File  string `json:"file,omitempty"`
}

// armEncryption is the account encryption block: service-side encryption is
// always on for Azure storage, keyed by Microsoft.Storage.
type armEncryption struct {
	Services  *armEncryptionServices `json:"services,omitempty"`
	KeySource string                 `json:"keySource,omitempty"`
}

type armEncryptionServices struct {
	Blob *armEncryptionService `json:"blob,omitempty"`
	File *armEncryptionService `json:"file,omitempty"`
}

type armEncryptionService struct {
	Enabled bool   `json:"enabled"`
	KeyType string `json:"keyType,omitempty"`
}

// armKeyList is the StorageAccountListKeysResult returned by listKeys /
// regenerateKey: {"keys":[{keyName,value,permissions}]}.
type armKeyList struct {
	Keys []armKey `json:"keys"`
}

type armKey struct {
	KeyName      string `json:"keyName"`
	Value        string `json:"value"`
	Permissions  string `json:"permissions"`
	CreationTime string `json:"creationTime,omitempty"`
}

// armRegenerateKey is the regenerateKey request body.
type armRegenerateKey struct {
	KeyName string `json:"keyName"`
}
