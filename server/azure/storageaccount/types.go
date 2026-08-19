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
	AccessTier        string `json:"accessTier,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}
