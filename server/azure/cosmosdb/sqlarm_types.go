package cosmosdb

// ARM (Microsoft.DocumentDB) SQL-API control-plane wire shapes, as driven by the
// armcosmos SQLResourcesClient. These mirror the SDK's JSON field names
// (camelCase) so a real client's Begin*/Get* round-trips against the handler.
//
// Databases, containers and their throughput share the very state the Cosmos
// data plane uses (the handler's databases set, driver tables and offers map),
// so a resource created through this control plane is immediately visible to the
// data plane and vice versa.

// armAutoscaleSettings is the request-side autoscale block: a single
// maxThroughput ceiling. Present on a create's options or a throughput update.
type armAutoscaleSettings struct {
	MaxThroughput *int32 `json:"maxThroughput,omitempty"`
}

// armAutoscaleSettingsResource is the response-side autoscale block.
type armAutoscaleSettingsResource struct {
	MaxThroughput *int32 `json:"maxThroughput,omitempty"`
}

// armCreateUpdateOptions carries the throughput a database/container is created
// with: either manual (Throughput) or autoscale (AutoscaleSettings), never both.
type armCreateUpdateOptions struct {
	Throughput        *int32                `json:"throughput,omitempty"`
	AutoscaleSettings *armAutoscaleSettings `json:"autoscaleSettings,omitempty"`
}

// SQL database ---------------------------------------------------------------

type armSQLDatabaseCreateParams struct {
	Properties *armSQLDatabaseCreateProps `json:"properties"`
	Location   string                     `json:"location,omitempty"`
	Tags       map[string]string          `json:"tags,omitempty"`
}

type armSQLDatabaseCreateProps struct {
	Resource *armSQLDatabaseResource `json:"resource"`
	Options  *armCreateUpdateOptions `json:"options,omitempty"`
}

type armSQLDatabaseResource struct {
	ID string `json:"id"`
}

type armSQLDatabaseGetResults struct {
	ID         string                  `json:"id,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Location   string                  `json:"location,omitempty"`
	Tags       map[string]string       `json:"tags,omitempty"`
	Properties *armSQLDatabaseGetProps `json:"properties,omitempty"`
}

type armSQLDatabaseGetProps struct {
	Resource *armSQLDatabaseGetResource `json:"resource,omitempty"`
}

type armSQLDatabaseGetResource struct {
	ID    string `json:"id"`
	RID   string `json:"_rid,omitempty"`
	TS    int64  `json:"_ts,omitempty"`
	ETag  string `json:"_etag,omitempty"`
	Colls string `json:"_colls,omitempty"`
	Users string `json:"_users,omitempty"`
}

type armSQLDatabaseList struct {
	Value []armSQLDatabaseGetResults `json:"value"`
}

// SQL container --------------------------------------------------------------

type armSQLContainerCreateParams struct {
	Properties *armSQLContainerCreateProps `json:"properties"`
	Location   string                      `json:"location,omitempty"`
	Tags       map[string]string           `json:"tags,omitempty"`
}

type armSQLContainerCreateProps struct {
	Resource *armSQLContainerResource `json:"resource"`
	Options  *armCreateUpdateOptions  `json:"options,omitempty"`
}

// armSQLContainerResource reuses the data plane's partitionKeyDef and
// uniqueKeyPolicy shapes so the two planes agree on container structure.
type armSQLContainerResource struct {
	ID              string           `json:"id"`
	PartitionKey    *partitionKeyDef `json:"partitionKey,omitempty"`
	DefaultTTL      *int32           `json:"defaultTtl,omitempty"`
	UniqueKeyPolicy *uniqueKeyPolicy `json:"uniqueKeyPolicy,omitempty"`
	IndexingPolicy  map[string]any   `json:"indexingPolicy,omitempty"`
}

type armSQLContainerGetResults struct {
	ID         string                   `json:"id,omitempty"`
	Name       string                   `json:"name,omitempty"`
	Type       string                   `json:"type,omitempty"`
	Location   string                   `json:"location,omitempty"`
	Tags       map[string]string        `json:"tags,omitempty"`
	Properties *armSQLContainerGetProps `json:"properties,omitempty"`
}

type armSQLContainerGetProps struct {
	Resource *armSQLContainerGetResource `json:"resource,omitempty"`
}

type armSQLContainerGetResource struct {
	ID              string           `json:"id"`
	PartitionKey    *partitionKeyDef `json:"partitionKey,omitempty"`
	DefaultTTL      *int32           `json:"defaultTtl,omitempty"`
	UniqueKeyPolicy *uniqueKeyPolicy `json:"uniqueKeyPolicy,omitempty"`
	IndexingPolicy  map[string]any   `json:"indexingPolicy,omitempty"`
	RID             string           `json:"_rid,omitempty"`
	TS              int64            `json:"_ts,omitempty"`
	ETag            string           `json:"_etag,omitempty"`
}

type armSQLContainerList struct {
	Value []armSQLContainerGetResults `json:"value"`
}

// Throughput -----------------------------------------------------------------

type armThroughputUpdateParams struct {
	Properties *armThroughputUpdateProps `json:"properties"`
}

type armThroughputUpdateProps struct {
	Resource *armThroughputResource `json:"resource"`
}

type armThroughputResource struct {
	Throughput        *int32                `json:"throughput,omitempty"`
	AutoscaleSettings *armAutoscaleSettings `json:"autoscaleSettings,omitempty"`
}

type armThroughputGetResults struct {
	ID         string                 `json:"id,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Type       string                 `json:"type,omitempty"`
	Properties *armThroughputGetProps `json:"properties,omitempty"`
}

type armThroughputGetProps struct {
	Resource *armThroughputGetResource `json:"resource,omitempty"`
}

type armThroughputGetResource struct {
	Throughput        *int32                        `json:"throughput,omitempty"`
	AutoscaleSettings *armAutoscaleSettingsResource `json:"autoscaleSettings,omitempty"`
	MinimumThroughput string                        `json:"minimumThroughput,omitempty"`
	RID               string                        `json:"_rid,omitempty"`
	TS                int64                         `json:"_ts,omitempty"`
	ETag              string                        `json:"_etag,omitempty"`
}
