package appsync

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/services/appsync/driver"
)

// graphqlAPIBody is the modeled subset of a Create/UpdateGraphqlAPI request.
// Every other field is carried through Extra so it round-trips verbatim.
type graphqlAPIBody struct {
	Name               string            `json:"name"`
	AuthenticationType string            `json:"authenticationType"`
	Visibility         string            `json:"visibility"`
	APIType            string            `json:"apiType"`
	XrayEnabled        *bool             `json:"xrayEnabled"`
	Tags               map[string]string `json:"tags"`
}

// graphqlAPIModeledKeys are the request fields graphqlAPIBody owns; they are
// stripped from Extra so they are not emitted twice.
//
//nolint:gochecknoglobals // immutable key set, read-only after init.
var graphqlAPIModeledKeys = []string{
	"name", "authenticationType", "visibility", "apiType", "xrayEnabled", "tags",
}

// dataSourceBody is the modeled subset of a Create/UpdateDataSource request.
type dataSourceBody struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Description    string `json:"description"`
	ServiceRoleArn string `json:"serviceRoleArn"`
}

//nolint:gochecknoglobals // immutable key set, read-only after init.
var dataSourceModeledKeys = []string{"name", "type", "description", "serviceRoleArn"}

// apiKeyBody is the modeled subset of a Create/UpdateAPIKey request.
type apiKeyBody struct {
	Description *string `json:"description"`
	Expires     int64   `json:"expires"`
}

// extraFrom returns the raw request fields not owned by a modeled struct.
func extraFrom(raw map[string]json.RawMessage, modeled []string) map[string]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}

	out := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		out[k] = v
	}

	for _, k := range modeled {
		delete(out, k)
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// apiToWire renders a GraphqlAPI as its restJson1 object. Extra is emitted
// first so the computed fields always win.
func apiToWire(a *driver.GraphqlAPI) map[string]any {
	out := map[string]any{}
	for k, v := range a.Extra {
		out[k] = v
	}

	out["apiId"] = a.APIID
	out["name"] = a.Name
	out["authenticationType"] = a.AuthenticationType
	out["arn"] = a.ARN
	out["uris"] = a.URIs
	out["owner"] = a.Owner
	out["visibility"] = a.Visibility
	out["apiType"] = a.APIType
	out["xrayEnabled"] = a.XrayEnabled

	if a.Tags != nil {
		out["tags"] = a.Tags
	}

	return out
}

// dataSourceToWire renders a DataSource as its restJson1 object.
func dataSourceToWire(ds *driver.DataSource) map[string]any {
	out := map[string]any{}
	for k, v := range ds.Extra {
		out[k] = v
	}

	out["name"] = ds.Name
	out["type"] = ds.Type
	out["dataSourceArn"] = ds.DataSourceArn

	if ds.Description != "" {
		out["description"] = ds.Description
	}

	if ds.ServiceRoleArn != "" {
		out["serviceRoleArn"] = ds.ServiceRoleArn
	}

	return out
}

// apiKeyToWire renders an APIKey as its restJson1 object.
func apiKeyToWire(k *driver.APIKey) map[string]any {
	return map[string]any{
		"id":          k.ID,
		"description": k.Description,
		"expires":     k.Expires,
		"deletes":     k.Deletes,
	}
}
