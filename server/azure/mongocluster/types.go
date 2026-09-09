package mongocluster

import (
	"github.com/stackshy/cloudemu/v2/providers/azure/mongocluster"
)

// clusterRequest is the ARM mongo-cluster PUT/PATCH body. mongoClusters is a
// TrackedResource: only location, tags and properties are writable — there is no
// top-level sku or identity.
type clusterRequest struct {
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Properties *clusterPropertiesRequest `json:"properties,omitempty"`
}

// administratorRequest is the writable administrator block. password is a
// write-only secret: it is accepted here but never echoed on a response.
type administratorRequest struct {
	UserName *string `json:"userName,omitempty"`
	Password *string `json:"password,omitempty"`
}

// computeWire is the compute tier block, shared by request and response.
type computeWire struct {
	Tier string `json:"tier,omitempty"`
}

// storageWire is the storage allocation block, shared by request and response.
type storageWire struct {
	SizeGb *int64 `json:"sizeGb,omitempty"`
}

// shardingWire is the shard-layout block, shared by request and response.
type shardingWire struct {
	ShardCount *int32 `json:"shardCount,omitempty"`
}

// highAvailabilityWire is the HA block, shared by request and response.
type highAvailabilityWire struct {
	TargetMode string `json:"targetMode,omitempty"`
}

// clusterPropertiesRequest is the writable subset of mongo-cluster properties.
// Pointer fields let a PATCH overlay only what it names.
type clusterPropertiesRequest struct {
	CreateMode          *string               `json:"createMode,omitempty"`
	Administrator       *administratorRequest `json:"administrator,omitempty"`
	ServerVersion       *string               `json:"serverVersion,omitempty"`
	PublicNetworkAccess *string               `json:"publicNetworkAccess,omitempty"`
	Compute             *computeWire          `json:"compute,omitempty"`
	Storage             *storageWire          `json:"storage,omitempty"`
	Sharding            *shardingWire         `json:"sharding,omitempty"`
	HighAvailability    *highAvailabilityWire `json:"highAvailability,omitempty"`
	PreviewFeatures     []string              `json:"previewFeatures,omitempty"`
}

// administratorResponse is the read projection of the administrator block — only
// userName is echoed; the password secret is dropped.
type administratorResponse struct {
	UserName string `json:"userName,omitempty"`
}

// clusterResponse is the ARM representation of a mongo cluster.
type clusterResponse struct {
	ID         string                    `json:"id"`
	Name       string                    `json:"name"`
	Type       string                    `json:"type"`
	Location   string                    `json:"location"`
	Tags       map[string]string         `json:"tags,omitempty"`
	Properties clusterPropertiesResponse `json:"properties"`
}

// clusterPropertiesResponse is the mongo-cluster properties block. The computed
// fields (provisioningState, clusterStatus, connectionString) are stable across
// reads; administrator carries only the userName.
type clusterPropertiesResponse struct {
	ProvisioningState   string                 `json:"provisioningState"`
	ClusterStatus       string                 `json:"clusterStatus"`
	Administrator       *administratorResponse `json:"administrator,omitempty"`
	ServerVersion       string                 `json:"serverVersion,omitempty"`
	CreateMode          string                 `json:"createMode,omitempty"`
	PublicNetworkAccess string                 `json:"publicNetworkAccess,omitempty"`
	Compute             *computeWire           `json:"compute,omitempty"`
	Storage             *storageWire           `json:"storage,omitempty"`
	Sharding            *shardingWire          `json:"sharding,omitempty"`
	HighAvailability    *highAvailabilityWire  `json:"highAvailability,omitempty"`
	PreviewFeatures     []string               `json:"previewFeatures,omitempty"`
	ConnectionString    string                 `json:"connectionString,omitempty"`
}

// clusterListResponse is the ARM cluster list envelope. nextLink is omitted — the
// emulator returns a single page.
type clusterListResponse struct {
	Value []clusterResponse `json:"value"`
}

// connectionStringWire is one entry of the listConnectionStrings result.
type connectionStringWire struct {
	ConnectionString string `json:"connectionString"`
	Description      string `json:"description,omitempty"`
	Name             string `json:"name,omitempty"`
}

// listConnectionStringsResponse is the ARM listConnectionStrings action body.
type listConnectionStringsResponse struct {
	ConnectionStrings []connectionStringWire `json:"connectionStrings"`
}

// clusterInputFromRequest builds a cluster create/update Input from a request
// body. Pointer fields are carried through so an absent field falls back to the
// stored (or default) value in the driver, which makes a PATCH body merge on its
// own.
func clusterInputFromRequest(req *clusterRequest) mongocluster.ClusterInput {
	in := mongocluster.ClusterInput{Tags: req.Tags}

	p := req.Properties
	if p == nil {
		return in
	}

	in.ServerVersion = p.ServerVersion
	in.CreateMode = p.CreateMode
	in.PublicNetworkAccess = p.PublicNetworkAccess
	in.PreviewFeatures = p.PreviewFeatures

	if p.Administrator != nil {
		in.AdministratorUserName = p.Administrator.UserName
		in.AdministratorPassword = p.Administrator.Password
	}

	if p.Compute != nil {
		in.Compute = &mongocluster.Compute{Tier: p.Compute.Tier}
	}

	if p.Storage != nil {
		in.Storage = &mongocluster.Storage{SizeGb: p.Storage.SizeGb}
	}

	if p.Sharding != nil {
		in.Sharding = &mongocluster.Sharding{ShardCount: p.Sharding.ShardCount}
	}

	if p.HighAvailability != nil {
		in.HighAvailability = &mongocluster.HighAvailability{TargetMode: p.HighAvailability.TargetMode}
	}

	return in
}

// toClusterResponse projects a stored cluster onto its ARM wire representation. The
// administrator password is never included — only the userName round-trips.
func toClusterResponse(c *mongocluster.Cluster) clusterResponse {
	out := clusterResponse{
		ID:       c.ARMID(),
		Name:     c.Name,
		Type:     clusterArmType,
		Location: c.Location,
		Tags:     c.Tags,
		Properties: clusterPropertiesResponse{
			ProvisioningState:   c.ProvisioningState,
			ClusterStatus:       c.ClusterStatus,
			ServerVersion:       c.ServerVersion,
			CreateMode:          c.CreateMode,
			PublicNetworkAccess: c.PublicNetworkAccess,
			PreviewFeatures:     c.PreviewFeatures,
			ConnectionString:    c.ConnectionString,
		},
	}

	if c.AdministratorUserName != "" {
		out.Properties.Administrator = &administratorResponse{UserName: c.AdministratorUserName}
	}

	if c.Compute != nil {
		out.Properties.Compute = &computeWire{Tier: c.Compute.Tier}
	}

	if c.Storage != nil {
		out.Properties.Storage = &storageWire{SizeGb: c.Storage.SizeGb}
	}

	if c.Sharding != nil {
		out.Properties.Sharding = &shardingWire{ShardCount: c.Sharding.ShardCount}
	}

	if c.HighAvailability != nil {
		out.Properties.HighAvailability = &highAvailabilityWire{TargetMode: c.HighAvailability.TargetMode}
	}

	return out
}

// toConnectionStringsResponse projects a cluster's connection strings onto the
// listConnectionStrings action body.
func toConnectionStringsResponse(items []mongocluster.ConnectionStringEntry) listConnectionStringsResponse {
	out := listConnectionStringsResponse{ConnectionStrings: make([]connectionStringWire, 0, len(items))}
	for i := range items {
		out.ConnectionStrings = append(out.ConnectionStrings, connectionStringWire{
			ConnectionString: items[i].ConnectionString,
			Description:      items[i].Description,
			Name:             items[i].Name,
		})
	}

	return out
}
