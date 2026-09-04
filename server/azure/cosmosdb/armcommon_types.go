package cosmosdb

// Shared ARM database-plane wire shapes. A Cosmos database carries the same
// minimal ARM body (properties.resource.id, optional throughput options) whether
// it is a SQL or a Mongo database, so both control planes decode and render
// these one set of types (parameterized only by the "type" string in armAPISpec).

type armDatabaseCreateParams struct {
	Properties *armDatabaseCreateProps `json:"properties"`
	Location   string                  `json:"location,omitempty"`
	Tags       map[string]string       `json:"tags,omitempty"`
}

type armDatabaseCreateProps struct {
	Resource *armDatabaseIDResource  `json:"resource"`
	Options  *armCreateUpdateOptions `json:"options,omitempty"`
}

// armDatabaseIDResource is the create body's resource block: just the database
// id (Cosmos databases carry no other user-settable resource properties).
type armDatabaseIDResource struct {
	ID string `json:"id"`
}

type armDatabaseGetResults struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name,omitempty"`
	Type       string               `json:"type,omitempty"`
	Location   string               `json:"location,omitempty"`
	Tags       map[string]string    `json:"tags,omitempty"`
	Properties *armDatabaseGetProps `json:"properties,omitempty"`
}

type armDatabaseGetProps struct {
	Resource *armDatabaseGetResource `json:"resource,omitempty"`
}

type armDatabaseGetResource struct {
	ID   string `json:"id"`
	RID  string `json:"_rid,omitempty"`
	TS   int64  `json:"_ts,omitempty"`
	ETag string `json:"_etag,omitempty"`
	// Colls/Users are the SQL database's child-resource links (Cosmos
	// SQLDatabaseGetPropertiesResource carries _colls/_users). The Mongo database
	// resource has neither, so they stay empty (omitted) for the Mongo plane.
	Colls string `json:"_colls,omitempty"`
	Users string `json:"_users,omitempty"`
}

type armDatabaseList struct {
	Value []armDatabaseGetResults `json:"value"`
}
