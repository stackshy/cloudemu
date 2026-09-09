package storageaccount

// armAccountCreate is the subset of the ARM storage-account create body the
// emulator reads. armstorage's AccountCreateParameters marshals to these JSON
// field names.
type armAccountCreate struct {
	Location   string              `json:"location,omitempty"`
	Kind       string              `json:"kind,omitempty"`
	Tags       map[string]string   `json:"tags,omitempty"`
	SKU        *armSKU             `json:"sku,omitempty"`
	Identity   *armIdentity        `json:"identity,omitempty"`
	Properties *armAccountPropsReq `json:"properties,omitempty"`
}

// armAccountUpdate is the subset of the ARM storage-account PATCH body the
// emulator reads. armstorage's AccountUpdateParameters marshals to these same
// JSON field names (minus location, which a PATCH cannot change).
type armAccountUpdate struct {
	Kind       string              `json:"kind,omitempty"`
	Tags       map[string]string   `json:"tags,omitempty"`
	SKU        *armSKU             `json:"sku,omitempty"`
	Identity   *armIdentity        `json:"identity,omitempty"`
	Properties *armAccountPropsReq `json:"properties,omitempty"`
}

// armIdentity is the ARM managed-identity block — a top-level sibling of
// properties/sku/kind on a storage account (armstorage.Identity). On a request
// only Type and the userAssignedIdentities keys are meaningful; the response
// synthesizes the system-assigned principal/tenant ids and each user-assigned
// identity's principal/client pair, mirroring real Azure.
type armIdentity struct {
	Type                   string                              `json:"type,omitempty"`
	PrincipalID            string                              `json:"principalId,omitempty"`
	TenantID               string                              `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]*armUserAssignedIdentity `json:"userAssignedIdentities,omitempty"`
}

// armUserAssignedIdentity is one entry of identity.userAssignedIdentities: the
// principal/client pair Azure returns for an attached user-assigned identity.
// On a request the value is an empty object; the response synthesizes the pair.
type armUserAssignedIdentity struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// armAccountPropsReq is the settable subset of properties on a create or
// update request body — shared because AccountPropertiesCreateParameters and
// AccountPropertiesUpdateParameters marshal accessTier/encryption identically.
type armAccountPropsReq struct {
	AccessTier string            `json:"accessTier,omitempty"`
	Encryption *armEncryptionReq `json:"encryption,omitempty"`
	// MinimumTLSVersion / PublicNetworkAccess are string toggles; empty means the
	// request omitted them.
	MinimumTLSVersion   string `json:"minimumTlsVersion,omitempty"`
	PublicNetworkAccess string `json:"publicNetworkAccess,omitempty"`
	// SupportsHTTPSTrafficOnly / AllowBlobPublicAccess / AllowSharedKeyAccess are
	// pointers so a PATCH can tell "omitted" (nil) from "explicitly false" — the
	// distinction the echo-properties overlay drops for zero-valued scalars, which
	// is why these must be modeled here rather than left to the overlay.
	SupportsHTTPSTrafficOnly *bool `json:"supportsHttpsTrafficOnly,omitempty"`
	AllowBlobPublicAccess    *bool `json:"allowBlobPublicAccess,omitempty"`
	AllowSharedKeyAccess     *bool `json:"allowSharedKeyAccess,omitempty"`
}

// armEncryptionReq is the request-side encryption block: keySource selects
// Microsoft.Storage (platform-managed, the default) or Microsoft.Keyvault
// (customer-managed key), in which case keyvaultproperties identifies the key.
type armEncryptionReq struct {
	KeySource          string               `json:"keySource,omitempty"`
	KeyVaultProperties *armKeyVaultPropsReq `json:"keyvaultproperties,omitempty"`
}

type armKeyVaultPropsReq struct {
	KeyVaultURI string `json:"keyvaulturi,omitempty"`
	KeyName     string `json:"keyname,omitempty"`
	KeyVersion  string `json:"keyversion,omitempty"`
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
	Identity   *armIdentity      `json:"identity,omitempty"`
	Properties *armAccountProps  `json:"properties,omitempty"`
}

// armAccountList is the {"value":[…]} envelope a storage-account list returns.
type armAccountList struct {
	Value []armAccount `json:"value"`
}

type armAccountProps struct {
	AccessTier        string        `json:"accessTier,omitempty"`
	ProvisioningState string        `json:"provisioningState,omitempty"`
	PrimaryEndpoints  *armEndpoints `json:"primaryEndpoints,omitempty"`
	PrimaryLocation   string        `json:"primaryLocation,omitempty"`
	StatusOfPrimary   string        `json:"statusOfPrimary,omitempty"`
	// The account security toggles are always rendered (with real-Azure defaults
	// when unset) so a create that omits them still reads back the values real
	// Azure reports, and an explicit "false" is never dropped. The bools carry no
	// omitempty — false is a meaningful, must-be-serialized value here.
	MinimumTLSVersion        string `json:"minimumTlsVersion,omitempty"`
	PublicNetworkAccess      string `json:"publicNetworkAccess,omitempty"`
	SupportsHTTPSTrafficOnly bool   `json:"supportsHttpsTrafficOnly"`
	AllowBlobPublicAccess    bool   `json:"allowBlobPublicAccess"`
	AllowSharedKeyAccess     bool   `json:"allowSharedKeyAccess"`
	// SecondaryLocation/StatusOfSecondary are populated for GRS/RA-GRS/GZRS/
	// RA-GZRS SKUs; SecondaryEndpoints only for the read-access (RA-*) variants
	// — matching real Azure (see armstorage AccountProperties doc comments).
	SecondaryLocation  string         `json:"secondaryLocation,omitempty"`
	StatusOfSecondary  string         `json:"statusOfSecondary,omitempty"`
	SecondaryEndpoints *armEndpoints  `json:"secondaryEndpoints,omitempty"`
	CreationTime       string         `json:"creationTime,omitempty"`
	Encryption         *armEncryption `json:"encryption,omitempty"`
}

// armEndpoints is the primaryEndpoints object (blob/queue/table/file service
// URLs) on a storage account's properties.
type armEndpoints struct {
	Blob  string `json:"blob,omitempty"`
	Queue string `json:"queue,omitempty"`
	Table string `json:"table,omitempty"`
	File  string `json:"file,omitempty"`
}

// armEncryption is the account encryption response block: service-side
// encryption is always on for Azure storage, keyed by either the
// Microsoft.Storage platform default or a Microsoft.Keyvault customer-managed
// key (in which case KeyVaultProperties identifies it).
type armEncryption struct {
	Services           *armEncryptionServices `json:"services,omitempty"`
	KeySource          string                 `json:"keySource,omitempty"`
	KeyVaultProperties *armKeyVaultProperties `json:"keyvaultproperties,omitempty"`
}

// armKeyVaultProperties identifies the customer-managed key on a
// Microsoft.Keyvault-encrypted account.
type armKeyVaultProperties struct {
	KeyVaultURI string `json:"keyvaulturi,omitempty"`
	KeyName     string `json:"keyname,omitempty"`
	KeyVersion  string `json:"keyversion,omitempty"`
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

// armCheckNameAvailabilityReq is the checkNameAvailability request body:
// armstorage's AccountCheckNameAvailabilityParameters marshals to these JSON
// field names ("type" is always "Microsoft.Storage/storageAccounts" and
// unused here).
type armCheckNameAvailabilityReq struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// armCheckNameAvailabilityResult is the checkNameAvailability response:
// armstorage's CheckNameAvailabilityResult shape.
type armCheckNameAvailabilityResult struct {
	Message       string `json:"message,omitempty"`
	NameAvailable bool   `json:"nameAvailable"`
	Reason        string `json:"reason,omitempty"`
}

// armBlobServiceProperties is the ARM wire shape for the storage-account Blob
// service properties sub-resource (…/blobServices/default), returned by both
// BlobServicesClient.SetServiceProperties and .GetServiceProperties.
type armBlobServiceProperties struct {
	ID         string                         `json:"id,omitempty"`
	Name       string                         `json:"name,omitempty"`
	Type       string                         `json:"type,omitempty"`
	Properties *armBlobServicePropertiesProps `json:"properties,omitempty"`
}

type armBlobServicePropertiesProps struct {
	IsVersioningEnabled   *bool               `json:"isVersioningEnabled,omitempty"`
	ChangeFeed            *armChangeFeed      `json:"changeFeed,omitempty"`
	DeleteRetentionPolicy *armDeleteRetention `json:"deleteRetentionPolicy,omitempty"`
	Cors                  *armCorsRules       `json:"cors,omitempty"`
}

type armChangeFeed struct {
	Enabled         *bool `json:"enabled,omitempty"`
	RetentionInDays *int  `json:"retentionInDays,omitempty"`
}

type armDeleteRetention struct {
	Enabled *bool `json:"enabled,omitempty"`
	Days    *int  `json:"days,omitempty"`
}

type armCorsRules struct {
	CorsRules []armCorsRule `json:"corsRules,omitempty"`
}

type armCorsRule struct {
	AllowedOrigins  []string `json:"allowedOrigins,omitempty"`
	AllowedMethods  []string `json:"allowedMethods,omitempty"`
	AllowedHeaders  []string `json:"allowedHeaders,omitempty"`
	ExposedHeaders  []string `json:"exposedHeaders,omitempty"`
	MaxAgeInSeconds int      `json:"maxAgeInSeconds,omitempty"`
}
