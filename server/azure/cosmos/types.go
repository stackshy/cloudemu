package cosmos

// Cosmos DB SQL API JSON wire shapes. We model the minimum surface needed for
// the azcosmos SDK to drive databases, containers, and items end-to-end.

// resource is the common envelope every Cosmos resource carries.
type resource struct {
	ID    string `json:"id"`
	RID   string `json:"_rid,omitempty"`
	Self  string `json:"_self,omitempty"`
	ETag  string `json:"_etag,omitempty"`
	TS    int64  `json:"_ts,omitempty"`
	Attac string `json:"_attachments,omitempty"`
}

type databaseResource struct {
	resource
	Colls string `json:"_colls,omitempty"`
	Users string `json:"_users,omitempty"`
}

type databasesList struct {
	RID       string             `json:"_rid"`
	Databases []databaseResource `json:"Databases"`
	Count     int                `json:"_count"`
}

type containerResource struct {
	resource
	Docs            string           `json:"_docs,omitempty"`
	Sprocs          string           `json:"_sprocs,omitempty"`
	Triggers        string           `json:"_triggers,omitempty"`
	UDFs            string           `json:"_udfs,omitempty"`
	Conflicts       string           `json:"_conflicts,omitempty"`
	PartitionKey    *partitionKeyDef `json:"partitionKey,omitempty"`
	IndexingPolicy  map[string]any   `json:"indexingPolicy,omitempty"`
	UniqueKeyPolicy map[string]any   `json:"uniqueKeyPolicy,omitempty"`
}

type partitionKeyDef struct {
	Paths   []string `json:"paths"`
	Kind    string   `json:"kind"`
	Version int      `json:"version,omitempty"`
}

type containersList struct {
	RID                 string              `json:"_rid"`
	DocumentCollections []containerResource `json:"DocumentCollections"`
	Count               int                 `json:"_count"`
}

type documentsList struct {
	RID       string           `json:"_rid"`
	Documents []map[string]any `json:"Documents"`
	Count     int              `json:"_count"`
}

type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
