package sql

import (
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// dbStatusOnline is the Azure SQL Database.status value echoed on read. Logical
// databases are always-on, so read responses report Online.
const dbStatusOnline = "Online"

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
	Status                      string  `json:"status,omitempty"`
	CreateMode                  string  `json:"createMode,omitempty"`
	SourceDatabaseID            string  `json:"sourceDatabaseId,omitempty"`
	RestorePointInTime          string  `json:"restorePointInTime,omitempty"`
	MaxSizeBytes                int64   `json:"maxSizeBytes,omitempty"`
	Collation                   string  `json:"collation,omitempty"`
	DatabaseID                  string  `json:"databaseId,omitempty"`
	CurrentServiceObjectiveName string  `json:"currentServiceObjectiveName,omitempty"`
	CurrentSKU                  *armSKU `json:"currentSku,omitempty"`
	ZoneRedundant               *bool   `json:"zoneRedundant,omitempty"`
	ElasticPoolID               string  `json:"elasticPoolId,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMServer converts a portable Cluster (logical server) to ARM JSON.
func toARMServer(cluster *rdsdriver.Cluster, subscription, resourceGroup string) armServer {
	return armServer{
		ID:       armServerID(subscription, resourceGroup, cluster.ID),
		Name:     cluster.ID,
		Type:     providerName + "/servers",
		Location: cluster.Location,
		Tags:     cluster.Tags,
		Properties: &armServerProps{
			AdministratorLogin:       cluster.MasterUsername,
			Version:                  cluster.EngineVersion,
			State:                    "Ready",
			FullyQualifiedDomainName: cluster.Endpoint,
		},
	}
}

// toARMDatabase converts a portable Database (Databases capability) to ARM JSON.
// SKU.name plus properties.currentSku / zoneRedundant are echoed so SKU/tier
// and HA are observable to both the armsql SDK and Resource Graph discovery.
//
// status is the transient ARM status (Creating / Scaling) reported while a
// create/update settle window is active; empty means the database has settled,
// so read responses report the terminal Online.
func toARMDatabase(db *rdsdriver.Database, rp *azurearm.ResourcePath, status string) armDatabase {
	zoneRedundant := db.ZoneRedundant

	if status == "" {
		status = dbStatusOnline
	}

	return armDatabase{
		ID:       armDatabaseID(rp.Subscription, rp.ResourceGroup, db.Server, db.Name),
		Name:     db.Name,
		Type:     providerName + "/servers/databases",
		Location: db.Location,
		Tags:     db.Tags,
		SKU:      &armSKU{Name: db.SKUName, Tier: db.SKUTier, Capacity: db.SKUCapacity},
		Properties: &armDatabaseProps{
			Status:                      status,
			Collation:                   db.Collation,
			DatabaseID:                  databaseGUID(db),
			CurrentServiceObjectiveName: db.SKUName,
			CurrentSKU:                  &armSKU{Name: db.SKUName, Tier: db.SKUTier, Capacity: db.SKUCapacity},
			ZoneRedundant:               &zoneRedundant,
			ElasticPoolID:               db.ElasticPoolID,
		},
	}
}

// databaseGUID derives the stable, GUID-shaped databaseId Azure reports for a
// database. Real Azure's databaseId is an intrinsic GUID, not a resource path,
// so it is seeded from the database's server/name (stable across reads,
// distinct per database) rather than reusing db.ARN, whose region-based ARN
// leaks us-east-1 into the value.
func databaseGUID(db *rdsdriver.Database) string {
	return idgen.SyntheticGUID(db.Server + "/" + db.Name)
}

func armServerID(subscription, resourceGroup, server string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + resourceGroup +
		"/providers/" + providerName + "/servers/" + server
}

func armDatabaseID(subscription, resourceGroup, server, database string) string {
	return armServerID(subscription, resourceGroup, server) + "/databases/" + database
}
