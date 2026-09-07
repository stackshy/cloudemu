package redisenterprise

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/redisenterprise"
)

// clusterRequest is the ARM cluster PUT/PATCH body. location, tags, sku, zones and
// identity are top-level; minimumTlsVersion lives under properties.
type clusterRequest struct {
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Sku        *skuWire                  `json:"sku,omitempty"`
	Zones      []string                  `json:"zones,omitempty"`
	Identity   *identityRequest          `json:"identity,omitempty"`
	Properties *clusterPropertiesRequest `json:"properties,omitempty"`
}

// skuWire is the cluster sku block, top-level on both request and response. name
// encodes the tier+size (e.g. Enterprise_E10); capacity is the unit count.
type skuWire struct {
	Name     string `json:"name"`
	Capacity *int   `json:"capacity,omitempty"`
}

// identityRequest is the writable half of the identity block; the minted ids are
// read-only. userAssignedIdentities is a map of ARM ids to (empty) objects on
// input.
type identityRequest struct {
	Type         string                     `json:"type"`
	UserAssigned map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// clusterPropertiesRequest is the writable subset of cluster properties.
type clusterPropertiesRequest struct {
	MinimumTLSVersion *string `json:"minimumTlsVersion,omitempty"`
}

// clusterResponse is the ARM representation of a redisEnterprise cluster.
type clusterResponse struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Sku        *skuWire                  `json:"sku,omitempty"`
	Zones      []string                  `json:"zones,omitempty"`
	Identity   *identityResponse         `json:"identity,omitempty"`
	Properties clusterPropertiesResponse `json:"properties"`
}

// identityResponse carries the identity block, including the service-minted ids.
type identityResponse struct {
	Type         string                         `json:"type"`
	PrincipalID  string                         `json:"principalId,omitempty"`
	TenantID     string                         `json:"tenantId,omitempty"`
	UserAssigned map[string]userAssignedWireVal `json:"userAssignedIdentities,omitempty"`
}

// userAssignedWireVal is the minted id pair for a user-assigned identity.
type userAssignedWireVal struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// clusterPropertiesResponse is the cluster properties block. The computed fields
// (hostName, provisioningState, resourceState, redisVersion) are stable across
// reads.
type clusterPropertiesResponse struct {
	ProvisioningState string `json:"provisioningState"`
	ResourceState     string `json:"resourceState"`
	//nolint:tagliatelle // ARM wire name is "hostName"
	HostName          string `json:"hostName"`
	RedisVersion      string `json:"redisVersion"`
	MinimumTLSVersion string `json:"minimumTlsVersion,omitempty"`
}

// databaseRequest is the ARM database PUT/PATCH body — all writable fields live
// under properties.
type databaseRequest struct {
	Properties *databasePropertiesRequest `json:"properties,omitempty"`
}

// databasePropertiesRequest is the writable subset of database properties. Pointer
// fields let a PATCH overlay only what it names.
type databasePropertiesRequest struct {
	ClientProtocol   *string         `json:"clientProtocol,omitempty"`
	ClusteringPolicy *string         `json:"clusteringPolicy,omitempty"`
	EvictionPolicy   *string         `json:"evictionPolicy,omitempty"`
	Port             *int            `json:"port,omitempty"`
	Modules          []moduleWire    `json:"modules,omitempty"`
	GeoReplication   *geoReplication `json:"geoReplication,omitempty"`
}

// geoReplication carries the linked-database group nickname. The linked-databases
// array itself is a deferred feature; only the nickname round-trips.
type geoReplication struct {
	GroupNickname string `json:"groupNickname,omitempty"`
}

// moduleWire is one Redis module on the wire. name and args are writable; version
// is service-computed and read-only.
type moduleWire struct {
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Version string `json:"version,omitempty"`
}

// databaseResponse is the ARM representation of a redisEnterprise database. Its
// name is "<cluster>/<database>", matching real Azure.
type databaseResponse struct {
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	Type       string                     `json:"type"`
	Properties databasePropertiesResponse `json:"properties"`
}

// databasePropertiesResponse is the database properties block. The computed fields
// (provisioningState, resourceState) and each module's version are stable.
type databasePropertiesResponse struct {
	ProvisioningState string          `json:"provisioningState"`
	ResourceState     string          `json:"resourceState"`
	ClientProtocol    string          `json:"clientProtocol"`
	ClusteringPolicy  string          `json:"clusteringPolicy"`
	EvictionPolicy    string          `json:"evictionPolicy"`
	Port              *int            `json:"port,omitempty"`
	Modules           []moduleWire    `json:"modules,omitempty"`
	GeoReplication    *geoReplication `json:"geoReplication,omitempty"`
}

// keysResponse is the ARM listKeys/regenerateKey action body.
type keysResponse struct {
	PrimaryKey   string `json:"primaryKey"`
	SecondaryKey string `json:"secondaryKey"`
}

// clusterListResponse is the ARM cluster list envelope. nextLink is omitted — the
// emulator returns a single page.
type clusterListResponse struct {
	Value []clusterResponse `json:"value"`
}

// databaseListResponse is the ARM database list envelope.
type databaseListResponse struct {
	Value []databaseResponse `json:"value"`
}

// toClusterResponse projects a stored cluster onto its ARM wire representation.
func toClusterResponse(c *redisenterprise.Cluster) clusterResponse {
	out := clusterResponse{
		ID:       c.ARMID(),
		Name:     c.Name,
		Type:     clusterArmType,
		Location: c.Location,
		Tags:     c.Tags,
		Zones:    c.Zones,
		Identity: toIdentityResponse(c.Identity),
		Properties: clusterPropertiesResponse{
			ProvisioningState: c.ProvisioningState,
			ResourceState:     c.ResourceState,
			HostName:          c.HostName,
			RedisVersion:      c.RedisVersion,
			MinimumTLSVersion: c.MinimumTLSVersion,
		},
	}

	if c.Sku != nil {
		out.Sku = &skuWire{Name: c.Sku.Name, Capacity: c.Sku.Capacity}
	}

	return out
}

// toDatabaseResponse projects a stored database onto its ARM wire representation.
func toDatabaseResponse(d *redisenterprise.Database) databaseResponse {
	out := databaseResponse{
		ID:   d.ARMID(),
		Name: d.ClusterName + "/" + d.Name,
		Type: databaseArmType,
		Properties: databasePropertiesResponse{
			ProvisioningState: d.ProvisioningState,
			ResourceState:     d.ResourceState,
			ClientProtocol:    d.ClientProtocol,
			ClusteringPolicy:  d.ClusteringPolicy,
			EvictionPolicy:    d.EvictionPolicy,
			Port:              d.Port,
			Modules:           toModulesWire(d.Modules),
		},
	}

	if d.GroupNickname != "" {
		out.Properties.GeoReplication = &geoReplication{GroupNickname: d.GroupNickname}
	}

	return out
}

// toKeysResponse projects a database's stable keys onto the listKeys body.
func toKeysResponse(d *redisenterprise.Database) keysResponse {
	return keysResponse{PrimaryKey: d.PrimaryKey, SecondaryKey: d.SecondaryKey}
}

func toIdentityResponse(in *redisenterprise.Identity) *identityResponse {
	if in == nil {
		return nil
	}

	out := &identityResponse{Type: in.Type, PrincipalID: in.PrincipalID, TenantID: in.TenantID}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]userAssignedWireVal, len(in.UserAssigned))
		for id, v := range in.UserAssigned {
			out.UserAssigned[id] = userAssignedWireVal{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
		}
	}

	return out
}

func toModulesWire(in []redisenterprise.Module) []moduleWire {
	if len(in) == 0 {
		return nil
	}

	out := make([]moduleWire, 0, len(in))
	for i := range in {
		out = append(out, moduleWire{Name: in[i].Name, Args: in[i].Args, Version: in[i].Version})
	}

	return out
}

// toDriverSku maps a wire sku onto the driver sku.
func toDriverSku(in *skuWire) *redisenterprise.Sku {
	if in == nil {
		return nil
	}

	return &redisenterprise.Sku{Name: in.Name, Capacity: in.Capacity}
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only the
// type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *redisenterprise.Identity {
	if in == nil {
		return nil
	}

	out := &redisenterprise.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]redisenterprise.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = redisenterprise.UserAssignedValue{}
		}
	}

	return out
}

// toDriverModules maps wire modules onto the driver modules.
func toDriverModules(in []moduleWire) []redisenterprise.Module {
	if in == nil {
		return nil
	}

	out := make([]redisenterprise.Module, 0, len(in))
	for i := range in {
		out = append(out, redisenterprise.Module{Name: in[i].Name, Args: in[i].Args, Version: in[i].Version})
	}

	return out
}
