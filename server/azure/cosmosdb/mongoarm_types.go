package cosmosdb

// ARM (Microsoft.DocumentDB) Mongo-API control-plane wire shapes, as driven by
// the armcosmos MongoDBResourcesClient. These mirror the SDK's JSON field names
// so a real client's Begin*/Get* round-trips against the handler. The database
// plane reuses the shared armDatabase* shapes (a Mongo database carries the same
// minimal body as a SQL one); only the collection shape (shard key + indexes +
// analytical TTL, instead of a SQL container's partition key + TTL/unique keys)
// is Mongo-specific and lives here.

// Mongo collection index shapes ----------------------------------------------

// armMongoIndexKeys is the field list one index covers (e.g. {"keys":["_id"]}).
type armMongoIndexKeys struct {
	Keys []string `json:"keys,omitempty"`
}

// armMongoIndexOptions carries an index's options: a TTL (expireAfterSeconds)
// and/or a uniqueness constraint.
type armMongoIndexOptions struct {
	ExpireAfterSeconds *int32 `json:"expireAfterSeconds,omitempty"`
	Unique             *bool  `json:"unique,omitempty"`
}

// armMongoIndex is one Mongo collection index (keys + options).
type armMongoIndex struct {
	Key     *armMongoIndexKeys    `json:"key,omitempty"`
	Options *armMongoIndexOptions `json:"options,omitempty"`
}

// Mongo collection -----------------------------------------------------------

type armMongoCollectionCreateParams struct {
	Properties *armMongoCollectionCreateProps `json:"properties"`
	Location   string                         `json:"location,omitempty"`
	Tags       map[string]string              `json:"tags,omitempty"`
}

type armMongoCollectionCreateProps struct {
	Resource *armMongoCollectionResource `json:"resource"`
	Options  *armCreateUpdateOptions     `json:"options,omitempty"`
}

// armMongoCollectionResource is the create body's resource block: the collection
// id, an optional single-field shard key ({field: "Hash"}), its indexes, and an
// optional analytical-store TTL.
type armMongoCollectionResource struct {
	ID                   string            `json:"id"`
	ShardKey             map[string]string `json:"shardKey,omitempty"`
	Indexes              []armMongoIndex   `json:"indexes,omitempty"`
	AnalyticalStorageTTL *int32            `json:"analyticalStorageTtl,omitempty"`
}

type armMongoCollectionGetResults struct {
	ID         string                      `json:"id,omitempty"`
	Name       string                      `json:"name,omitempty"`
	Type       string                      `json:"type,omitempty"`
	Location   string                      `json:"location,omitempty"`
	Tags       map[string]string           `json:"tags,omitempty"`
	Properties *armMongoCollectionGetProps `json:"properties,omitempty"`
}

type armMongoCollectionGetProps struct {
	Resource *armMongoCollectionGetResource `json:"resource,omitempty"`
}

type armMongoCollectionGetResource struct {
	ID                   string            `json:"id"`
	ShardKey             map[string]string `json:"shardKey,omitempty"`
	Indexes              []armMongoIndex   `json:"indexes,omitempty"`
	AnalyticalStorageTTL *int32            `json:"analyticalStorageTtl,omitempty"`
	RID                  string            `json:"_rid,omitempty"`
	TS                   int64             `json:"_ts,omitempty"`
	ETag                 string            `json:"_etag,omitempty"`
}
