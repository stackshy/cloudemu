package azuresql

import (
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

// Azure SQL Database.status enum values used in ARM responses.
const (
	dbStatusOnline   = "Online"
	dbStatusPaused   = "Paused"
	dbStatusCreating = "Creating"
	dbStatusDeleting = "Deleting"
)

// armServer is the JSON shape Azure ARM expects for Microsoft.Sql/servers.
type armServer struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties *armServerProps   `json:"properties,omitempty"`
}

type armServerProps struct {
	AdministratorLogin         string `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword string `json:"administratorLoginPassword,omitempty"`
	Version                    string `json:"version,omitempty"`
	State                      string `json:"state,omitempty"`
	FullyQualifiedDomainName   string `json:"fullyQualifiedDomainName,omitempty"`
}

// armDatabase is the JSON shape Azure ARM expects for
// Microsoft.Sql/servers/databases.
type armDatabase struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *armSKU           `json:"sku,omitempty"`
	Properties *armDatabaseProps `json:"properties,omitempty"`
}

type armSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type armDatabaseProps struct {
	Status                      string `json:"status,omitempty"`
	CreateMode                  string `json:"createMode,omitempty"`
	SourceDatabaseID            string `json:"sourceDatabaseId,omitempty"`
	RestorePointInTime          string `json:"restorePointInTime,omitempty"`
	MaxSizeBytes                int64  `json:"maxSizeBytes,omitempty"`
	Collation                   string `json:"collation,omitempty"`
	DatabaseID                  string `json:"databaseId,omitempty"`
	CurrentServiceObjectiveName string `json:"currentServiceObjectiveName,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMServer converts a portable Cluster (logical server) to ARM JSON.
func toARMServer(cluster *rdsdriver.Cluster, subscription, resourceGroup string) armServer {
	return armServer{
		ID:   armServerID(subscription, resourceGroup, cluster.ID),
		Name: cluster.ID,
		Type: providerName + "/servers",
		// Region is stashed in SubnetGroupName by the provider.
		Location: cluster.SubnetGroupName,
		Tags:     cluster.Tags,
		Properties: &armServerProps{
			AdministratorLogin:       cluster.MasterUsername,
			Version:                  cluster.EngineVersion,
			State:                    "Ready",
			FullyQualifiedDomainName: cluster.Endpoint,
		},
	}
}

// toARMDatabase converts a portable Instance (database) to ARM JSON.
func toARMDatabase(inst *rdsdriver.Instance, subscription, resourceGroup string) armDatabase {
	return armDatabase{
		ID:       armDatabaseID(subscription, resourceGroup, inst.ClusterID, inst.ID),
		Name:     inst.ID,
		Type:     providerName + "/servers/databases",
		Location: inst.AvailabilityZone,
		Tags:     inst.Tags,
		SKU: &armSKU{
			Name: inst.InstanceClass,
		},
		Properties: &armDatabaseProps{
			Status:                      databaseStatus(inst.State),
			MaxSizeBytes:                int64(inst.AllocatedStorage) * (1 << 30),
			Collation:                   "SQL_Latin1_General_CP1_CI_AS",
			DatabaseID:                  inst.ARN,
			CurrentServiceObjectiveName: inst.InstanceClass,
		},
	}
}

func armServerID(subscription, resourceGroup, server string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + resourceGroup +
		"/providers/" + providerName + "/servers/" + server
}

func armDatabaseID(subscription, resourceGroup, server, database string) string {
	return armServerID(subscription, resourceGroup, server) + "/databases/" + database
}

// databaseStatus maps the portable lifecycle to the Azure SQL Database.status
// enum (Online, Offline, Restoring, Creating, Disabled).
func databaseStatus(state string) string {
	switch state {
	case rdsdriver.StateAvailable:
		return dbStatusOnline
	case rdsdriver.StateStopped:
		return dbStatusPaused
	case rdsdriver.StateCreating:
		return dbStatusCreating
	case rdsdriver.StateDeleting:
		return dbStatusDeleting
	default:
		return dbStatusOnline
	}
}
