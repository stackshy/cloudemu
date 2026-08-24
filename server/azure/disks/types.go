package disks

// ARM JSON shapes for Microsoft.Compute/disks. Modeled on the real schema
// (https://learn.microsoft.com/en-us/rest/api/compute/disks) trimmed to the
// fields our mock exercises.

type diskRequest struct {
	Location   string            `json:"location"`
	SKU        *diskSKU          `json:"sku,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties diskRequestProps  `json:"properties"`
}

type diskRequestProps struct {
	CreationData      *creationData `json:"creationData,omitempty"`
	DiskSizeGB        int           `json:"diskSizeGB,omitempty"`
	DiskIOPSReadWrite int           `json:"diskIOPSReadWrite"`
	DiskMBpsReadWrite int           `json:"diskMBpsReadWrite"`
	Tier              string        `json:"tier"`
}

type creationData struct {
	CreateOption     string `json:"createOption,omitempty"`
	SourceURI        string `json:"sourceUri,omitempty"`
	SourceResourceID string `json:"sourceResourceId,omitempty"`
}

type diskSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type diskResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	SKU        *diskSKU          `json:"sku,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties diskResponseProps `json:"properties"`
}

type diskResponseProps struct {
	ProvisioningState string        `json:"provisioningState"`
	DiskSizeGB        int           `json:"diskSizeGB"`
	DiskState         string        `json:"diskState"`
	CreationData      *creationData `json:"creationData,omitempty"`
	DiskIOPSReadWrite int           `json:"diskIOPSReadWrite,omitempty"`
	DiskMBpsReadWrite int           `json:"diskMBpsReadWrite,omitempty"`
	Tier              string        `json:"tier,omitempty"`
	TimeCreated       string        `json:"timeCreated,omitempty"`
	UniqueID          string        `json:"uniqueId,omitempty"`
}

type diskListResponse struct {
	Value []diskResponse `json:"value"`
}

// grantAccessData is the request body for POST .../disks/{name}/beginGetAccess
// (armcompute.GrantAccessData): the requested access level and the SAS lifetime.
type grantAccessData struct {
	Access            string `json:"access,omitempty"`
	DurationInSeconds int    `json:"durationInSeconds,omitempty"`
	FileFormat        string `json:"fileFormat,omitempty"`
}

// accessURIResponse is the 200 body for beginGetAccess (armcompute.AccessURI):
// the SAS URI a client exports/imports the disk contents through.
type accessURIResponse struct {
	AccessSAS string `json:"accessSAS"`
}
