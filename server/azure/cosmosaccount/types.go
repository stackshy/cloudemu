package cosmosaccount

// armAccountCreate is the subset of the ARM databaseAccounts create body the
// emulator reads. armcosmos's DatabaseAccountCreateUpdateParameters marshals to
// these JSON field names.
type armAccountCreate struct {
	Location   string                 `json:"location,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Properties *armAccountCreateProps `json:"properties,omitempty"`
}

type armAccountCreateProps struct {
	DatabaseAccountOfferType     string                `json:"databaseAccountOfferType,omitempty"`
	EnableFreeTier               bool                  `json:"enableFreeTier,omitempty"`
	Capabilities                 []armCapability       `json:"capabilities,omitempty"`
	Locations                    []armLocation         `json:"locations,omitempty"`
	EnableMultipleWriteLocations bool                  `json:"enableMultipleWriteLocations,omitempty"`
	ConsistencyPolicy            *armConsistencyPolicy `json:"consistencyPolicy,omitempty"`
}

// armConsistencyPolicy is the ARM Cosmos consistencyPolicy shape. The staleness
// bounds are meaningful only for the BoundedStaleness level.
type armConsistencyPolicy struct {
	DefaultConsistencyLevel string `json:"defaultConsistencyLevel,omitempty"`
	MaxIntervalInSeconds    int32  `json:"maxIntervalInSeconds,omitempty"`
	MaxStalenessPrefix      int64  `json:"maxStalenessPrefix,omitempty"`
}

// armCapability is the ARM capability shape ([{name}]).
type armCapability struct {
	Name string `json:"name,omitempty"`
}

// armAccount is the ARM databaseAccounts wire shape returned on create/get.
type armAccount struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *armAccountProps  `json:"properties,omitempty"`
}

type armAccountProps struct {
	DatabaseAccountOfferType string                `json:"databaseAccountOfferType,omitempty"`
	EnableFreeTier           bool                  `json:"enableFreeTier,omitempty"`
	Capabilities             []armCapability       `json:"capabilities,omitempty"`
	ProvisioningState        string                `json:"provisioningState,omitempty"`
	DocumentEndpoint         string                `json:"documentEndpoint,omitempty"`
	Locations                []armLocation         `json:"locations,omitempty"`
	ReadLocations            []armLocation         `json:"readLocations,omitempty"`
	WriteLocations           []armLocation         `json:"writeLocations,omitempty"`
	FailoverPolicies         []armFailover         `json:"failoverPolicies,omitempty"`
	ConsistencyPolicy        *armConsistencyPolicy `json:"consistencyPolicy,omitempty"`
}

// armLocation is a region entry in a database account's location arrays.
type armLocation struct {
	ID                string `json:"id,omitempty"`
	LocationName      string `json:"locationName,omitempty"`
	DocumentEndpoint  string `json:"documentEndpoint,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
	FailoverPriority  int32  `json:"failoverPriority"`
	IsZoneRedundant   bool   `json:"isZoneRedundant"`
}

// armFailover is an entry in the account failoverPolicies array.
type armFailover struct {
	ID               string `json:"id,omitempty"`
	LocationName     string `json:"locationName,omitempty"`
	FailoverPriority int32  `json:"failoverPriority"`
}

// armAccountList is the {value:[...]} envelope for List / ListByResourceGroup.
type armAccountList struct {
	Value []armAccount `json:"value"`
}

// armListKeysResult is the DatabaseAccountListKeysResult wire shape.
type armListKeysResult struct {
	PrimaryMasterKey           string `json:"primaryMasterKey"`
	SecondaryMasterKey         string `json:"secondaryMasterKey"`
	PrimaryReadonlyMasterKey   string `json:"primaryReadonlyMasterKey"`
	SecondaryReadonlyMasterKey string `json:"secondaryReadonlyMasterKey"`
}

// armReadOnlyKeysResult is the DatabaseAccountListReadOnlyKeysResult shape.
type armReadOnlyKeysResult struct {
	PrimaryReadonlyMasterKey   string `json:"primaryReadonlyMasterKey"`
	SecondaryReadonlyMasterKey string `json:"secondaryReadonlyMasterKey"`
}

// armConnectionString is one entry in a ListConnectionStrings result.
type armConnectionString struct {
	ConnectionString string `json:"connectionString"`
	Description      string `json:"description"`
	KeyKind          string `json:"keyKind"`
	Type             string `json:"type"`
}

// armConnectionStringsResult is the DatabaseAccountListConnectionStringsResult
// wire shape.
type armConnectionStringsResult struct {
	ConnectionStrings []armConnectionString `json:"connectionStrings"`
}

// armRegenerateKey is the DatabaseAccountRegenerateKeyParameters request body.
type armRegenerateKey struct {
	KeyKind string `json:"keyKind"`
}

// armFailoverPolicies is the FailoverPriorityChange request body: the new
// ordering of regions by failover priority.
type armFailoverPolicies struct {
	FailoverPolicies []armFailover `json:"failoverPolicies"`
}
