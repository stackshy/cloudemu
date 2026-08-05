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
	DatabaseAccountOfferType string          `json:"databaseAccountOfferType,omitempty"`
	EnableFreeTier           bool            `json:"enableFreeTier,omitempty"`
	Capabilities             []armCapability `json:"capabilities,omitempty"`
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
	DatabaseAccountOfferType string          `json:"databaseAccountOfferType,omitempty"`
	EnableFreeTier           bool            `json:"enableFreeTier,omitempty"`
	Capabilities             []armCapability `json:"capabilities,omitempty"`
	ProvisioningState        string          `json:"provisioningState,omitempty"`
}
