package kusto

import "time"

// clusterResource is the ARM JSON shape for Microsoft.Kusto/clusters.
type clusterResource struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *kustoSKU         `json:"sku,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	SystemData *systemData       `json:"systemData,omitempty"`
	Properties clusterProperties `json:"properties"`
}

type clusterProperties struct {
	ProvisioningState      string `json:"provisioningState,omitempty"`
	State                  string `json:"state,omitempty"`
	URI                    string `json:"uri,omitempty"`
	DataIngestionURI       string `json:"dataIngestionUri,omitempty"`
	StateReason            string `json:"stateReason,omitempty"`
	EngineType             string `json:"engineType,omitempty"`
	PublicNetworkAccess    string `json:"publicNetworkAccess,omitempty"`
	EnableStreamingIngest  *bool  `json:"enableStreamingIngest,omitempty"`
	EnableDiskEncryption   *bool  `json:"enableDiskEncryption,omitempty"`
	EnableDoubleEncryption *bool  `json:"enableDoubleEncryption,omitempty"`
	EnablePurge            *bool  `json:"enablePurge,omitempty"`
	EnableAutoStop         *bool  `json:"enableAutoStop,omitempty"`
}

type kustoSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int32 `json:"capacity,omitempty"`
}

type systemData struct {
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	CreatedByType  string     `json:"createdByType,omitempty"`
	LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`
}

// databaseResource is the ARM JSON shape for .../clusters/databases. The kind
// discriminator ("ReadWrite") lets the polymorphic armkusto SDK decode it into
// a ReadWriteDatabase.
type databaseResource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Kind       string             `json:"kind"`
	Location   string             `json:"location,omitempty"`
	Properties databaseProperties `json:"properties"`
}

type databaseProperties struct {
	SoftDeletePeriod  string `json:"softDeletePeriod,omitempty"`
	HotCachePeriod    string `json:"hotCachePeriod,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
	IsFollowed        *bool  `json:"isFollowed,omitempty"`
}

// listResponse is the {value: [...]} envelope ARM uses for collection responses.
type listResponse struct {
	Value    []any  `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// Request bodies decoded from PUT payloads.

type createClusterRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *kustoSKU         `json:"sku,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Properties clusterProperties `json:"properties"`
}

type createDatabaseRequest struct {
	Kind       string             `json:"kind"`
	Location   string             `json:"location"`
	Properties databaseProperties `json:"properties"`
}

// Request bodies decoded from PATCH payloads. Properties is a pointer on the
// cluster update so an omitted properties block is distinguishable from an
// explicit empty one and leaves the stored properties untouched.

type updateClusterRequest struct {
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	SKU        *kustoSKU          `json:"sku,omitempty"`
	Zones      []string           `json:"zones,omitempty"`
	Properties *clusterProperties `json:"properties,omitempty"`
}

type updateDatabaseRequest struct {
	// Kind is decoded but never applied: a database's kind is immutable in real
	// Azure, so a PATCH cannot change it. It is kept so the field round-trips.
	Kind       string             `json:"kind"`
	Location   string             `json:"location"`
	Properties databaseProperties `json:"properties"`
}
