package postgresflex

import (
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// Postgres Flex ServerState enum values used in ARM responses. Real Azure
// exposes Disabled/Dropping/Ready/Starting/Stopped/Stopping/Updating; the
// mock emits the four reachable from the relationaldb lifecycle.
const (
	stateReady    = "Ready"
	stateStopped  = "Stopped"
	stateStarting = "Starting"
	stateStopping = "Stopping"
	stateUpdating = "Updating"
	stateDropping = "Dropping"
)

// armServer is the JSON shape Azure ARM expects for
// Microsoft.DBforPostgreSQL/flexibleServers.
type armServer struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	SKU        *armSKU           `json:"sku,omitempty"`
	Properties *armServerProps   `json:"properties,omitempty"`
}

type armSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

type armServerProps struct {
	AdministratorLogin         string      `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword string      `json:"administratorLoginPassword,omitempty"`
	Version                    string      `json:"version,omitempty"`
	State                      string      `json:"state,omitempty"`
	FullyQualifiedDomainName   string      `json:"fullyQualifiedDomainName,omitempty"`
	Storage                    *armStorage `json:"storage,omitempty"`
	AvailabilityZone           string      `json:"availabilityZone,omitempty"`
	CreateMode                 string      `json:"createMode,omitempty"`
	SourceServerResourceID     string      `json:"sourceServerResourceId,omitempty"`
}

type armStorage struct {
	StorageSizeGB int `json:"storageSizeGB,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMServer converts a portable Instance to ARM JSON.
func toARMServer(inst *rdsdriver.Instance, subscription, resourceGroup string) armServer {
	props := &armServerProps{
		AdministratorLogin:       inst.MasterUsername,
		Version:                  inst.EngineVersion,
		State:                    serverState(inst.State),
		FullyQualifiedDomainName: inst.Endpoint,
		AvailabilityZone:         inst.AvailabilityZone,
	}

	if inst.AllocatedStorage > 0 {
		props.Storage = &armStorage{StorageSizeGB: inst.AllocatedStorage}
	}

	return armServer{
		ID:       azurearm.BuildResourceID(subscription, resourceGroup, providerName, resourceFlexibleServers, inst.ID),
		Name:     inst.ID,
		Type:     providerName + "/" + resourceFlexibleServers,
		Location: inst.AvailabilityZone,
		Tags:     inst.Tags,
		SKU: &armSKU{
			Name: inst.InstanceClass,
		},
		Properties: props,
	}
}

// serverState maps the portable lifecycle to the Azure Postgres Flex
// ServerState enum (Ready, Stopped, Starting, Stopping, Updating, Dropping).
func serverState(s string) string {
	switch s {
	case rdsdriver.StateAvailable:
		return stateReady
	case rdsdriver.StateStopped:
		return stateStopped
	case rdsdriver.StateStarting:
		return stateStarting
	case rdsdriver.StateStopping:
		return stateStopping
	case rdsdriver.StateModifying, rdsdriver.StateRebooting, rdsdriver.StateCreating:
		return stateUpdating
	case rdsdriver.StateDeleting:
		return stateDropping
	default:
		return stateReady
	}
}
