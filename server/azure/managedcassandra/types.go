package managedcassandra

import (
	mcdriver "github.com/stackshy/cloudemu/v2/services/managedcassandra/driver"
)

const (
	providerName         = "Microsoft.DocumentDB"
	resourceType         = "cassandraClusters"
	subResourceDCs       = "dataCenters"
	actionDeallocate     = "deallocate"
	actionStart          = "start"
	actionInvokeCommand  = "invokeCommand"
	actionStatus         = "status"
	resourceLocations    = "locations"
	subOperationStatuses = "operationStatuses"

	clusterResourceType = providerName + "/" + resourceType
	dcResourceType      = clusterResourceType + "/" + subResourceDCs
)

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

type seedNode struct {
	IPAddress string `json:"ipAddress,omitempty"`
}

type certificate struct {
	Pem string `json:"pem,omitempty"`
}

// clusterResource is the ARM JSON shape for a managed Cassandra cluster.
type clusterResource struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties *clusterProperties `json:"properties,omitempty"`
}

type clusterProperties struct {
	ProvisioningState             string        `json:"provisioningState,omitempty"`
	CassandraVersion              string        `json:"cassandraVersion,omitempty"`
	ClusterNameOverride           string        `json:"clusterNameOverride,omitempty"`
	DelegatedManagementSubnetID   string        `json:"delegatedManagementSubnetId,omitempty"`
	AuthenticationMethod          string        `json:"authenticationMethod,omitempty"`
	InitialCassandraAdminPassword string        `json:"initialCassandraAdminPassword,omitempty"`
	HoursBetweenBackups           int           `json:"hoursBetweenBackups,omitempty"`
	RepairEnabled                 *bool         `json:"repairEnabled,omitempty"`
	CassandraAuditLoggingEnabled  *bool         `json:"cassandraAuditLoggingEnabled,omitempty"`
	Deallocated                   *bool         `json:"deallocated,omitempty"`
	ExternalSeedNodes             []seedNode    `json:"externalSeedNodes,omitempty"`
	SeedNodes                     []seedNode    `json:"seedNodes,omitempty"`
	ClientCertificates            []certificate `json:"clientCertificates,omitempty"`
	ExternalGossipCertificates    []certificate `json:"externalGossipCertificates,omitempty"`
	GossipCertificates            []certificate `json:"gossipCertificates,omitempty"`
}

// dataCenterResource is the ARM JSON shape for a datacenter.
type dataCenterResource struct {
	ID         string        `json:"id,omitempty"`
	Name       string        `json:"name,omitempty"`
	Type       string        `json:"type,omitempty"`
	Properties *dcProperties `json:"properties,omitempty"`
}

type dcProperties struct {
	ProvisioningState                  string     `json:"provisioningState,omitempty"`
	DataCenterLocation                 string     `json:"dataCenterLocation,omitempty"`
	DelegatedSubnetID                  string     `json:"delegatedSubnetId,omitempty"`
	NodeCount                          int        `json:"nodeCount,omitempty"`
	DiskCapacity                       int        `json:"diskCapacity,omitempty"`
	SKU                                string     `json:"sku,omitempty"`
	DiskSKU                            string     `json:"diskSku,omitempty"`
	AvailabilityZone                   *bool      `json:"availabilityZone,omitempty"`
	Base64EncodedCassandraYamlFragment string     `json:"base64EncodedCassandraYamlFragment,omitempty"`
	BackupStorageCustomerKeyURI        string     `json:"backupStorageCustomerKeyUri,omitempty"`
	ManagedDiskCustomerKeyURI          string     `json:"managedDiskCustomerKeyUri,omitempty"`
	SeedNodes                          []seedNode `json:"seedNodes,omitempty"`
	Deallocated                        *bool      `json:"deallocated,omitempty"`
}

// commandOutput is the InvokeCommand response body.
type commandOutput struct {
	CommandOutput string `json:"commandOutput,omitempty"`
}

// commandPostBody is the InvokeCommand request body.
type commandPostBody struct {
	Command string `json:"command,omitempty"`
	Host    string `json:"host,omitempty"`
}

// clusterStatus is the ARM public-status response body.
type clusterStatus struct {
	ETag         string             `json:"eTag,omitempty"`
	ReaperStatus *reaperStatus      `json:"reaperStatus,omitempty"`
	DataCenters  []statusDataCenter `json:"dataCenters,omitempty"`
}

type reaperStatus struct {
	Healthy *bool `json:"healthy,omitempty"`
}

type statusDataCenter struct {
	Name  string       `json:"name,omitempty"`
	Nodes []statusNode `json:"nodes,omitempty"`
}

type statusNode struct {
	Address string `json:"address,omitempty"`
	State   string `json:"state,omitempty"`
	Rack    string `json:"rack,omitempty"`
	Load    string `json:"load,omitempty"`
}

func seeds(addrs []string) []seedNode {
	if len(addrs) == 0 {
		return nil
	}

	out := make([]seedNode, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, seedNode{IPAddress: a})
	}

	return out
}

func certs(pems []string) []certificate {
	if len(pems) == 0 {
		return nil
	}

	out := make([]certificate, 0, len(pems))
	for _, p := range pems {
		out = append(out, certificate{Pem: p})
	}

	return out
}

func boolPtr(b bool) *bool { return &b }

// toARMCluster converts a driver Cluster to ARM JSON.
func toARMCluster(c *mcdriver.Cluster, id string) clusterResource {
	return clusterResource{
		ID:       id,
		Name:     c.Name,
		Type:     clusterResourceType,
		Location: c.Location,
		Tags:     c.Tags,
		Properties: &clusterProperties{
			ProvisioningState:            c.ProvisioningState,
			CassandraVersion:             c.CassandraVersion,
			ClusterNameOverride:          c.ClusterNameOverride,
			DelegatedManagementSubnetID:  c.DelegatedManagementSubnetID,
			AuthenticationMethod:         c.AuthenticationMethod,
			HoursBetweenBackups:          c.HoursBetweenBackups,
			RepairEnabled:                boolPtr(c.RepairEnabled),
			CassandraAuditLoggingEnabled: boolPtr(c.CassandraAuditLoggingEnabled),
			Deallocated:                  boolPtr(c.Deallocated),
			ExternalSeedNodes:            seeds(c.ExternalSeedNodes),
			SeedNodes:                    seeds(c.SeedNodes),
			ClientCertificates:           certs(c.ClientCertificates),
			ExternalGossipCertificates:   certs(c.ExternalGossipCertificates),
			GossipCertificates:           certs(c.GossipCertificates),
		},
	}
}

// toARMDataCenter converts a driver DataCenter to ARM JSON.
func toARMDataCenter(dc *mcdriver.DataCenter, id string) dataCenterResource {
	return dataCenterResource{
		ID:   id,
		Name: dc.Name,
		Type: dcResourceType,
		Properties: &dcProperties{
			ProvisioningState:                  dc.ProvisioningState,
			DataCenterLocation:                 dc.DataCenterLocation,
			DelegatedSubnetID:                  dc.DelegatedSubnetID,
			NodeCount:                          dc.NodeCount,
			DiskCapacity:                       dc.DiskCapacity,
			SKU:                                dc.SKU,
			DiskSKU:                            dc.DiskSKU,
			AvailabilityZone:                   boolPtr(dc.AvailabilityZone),
			Base64EncodedCassandraYamlFragment: dc.Base64EncodedCassandraYamlFragment,
			BackupStorageCustomerKeyURI:        dc.BackupStorageCustomerKeyURI,
			ManagedDiskCustomerKeyURI:          dc.ManagedDiskCustomerKeyURI,
			SeedNodes:                          seeds(dc.SeedNodes),
			Deallocated:                        boolPtr(dc.Deallocated),
		},
	}
}

func ipAddrs(nodes []seedNode) []string {
	if len(nodes) == 0 {
		return nil
	}

	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.IPAddress)
	}

	return out
}

func pems(cs []certificate) []string {
	if len(cs) == 0 {
		return nil
	}

	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Pem)
	}

	return out
}
