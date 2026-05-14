package mysqlflex

import (
	rdsdriver "github.com/stackshy/cloudemu/relationaldb/driver"
)

// MySQL Flexible Server state enum values surfaced in ARM responses.
const (
	stateReady    = "Ready"
	stateStopped  = "Stopped"
	stateStarting = "Starting"
	stateStopping = "Stopping"
	stateUpdating = "Updating"
	stateDropping = "Dropping"
)

// armServer is the JSON shape Azure ARM expects for
// Microsoft.DBforMySQL/flexibleServers.
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
}

type armStorage struct {
	StorageSizeGB int    `json:"storageSizeGB,omitempty"`
	StorageSKU    string `json:"storageSku,omitempty"`
	AutoGrow      string `json:"autoGrow,omitempty"`
	Iops          int    `json:"iops,omitempty"`
}

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// toARMServer converts a portable Instance to ARM JSON.
func toARMServer(inst *rdsdriver.Instance, subscription, resourceGroup string) armServer {
	return armServer{
		ID:       armServerID(subscription, resourceGroup, inst.ID),
		Name:     inst.ID,
		Type:     providerName + "/" + resourceFlexServers,
		Location: inst.AvailabilityZone,
		Tags:     inst.Tags,
		SKU: &armSKU{
			Name: inst.InstanceClass,
		},
		Properties: &armServerProps{
			AdministratorLogin:       inst.MasterUsername,
			Version:                  inst.EngineVersion,
			State:                    serverState(inst.State),
			FullyQualifiedDomainName: inst.Endpoint,
			AvailabilityZone:         inst.AvailabilityZone,
			Storage: &armStorage{
				StorageSizeGB: inst.AllocatedStorage,
				StorageSKU:    inst.StorageType,
			},
		},
	}
}

func armServerID(subscription, resourceGroup, server string) string {
	return "/subscriptions/" + subscription +
		"/resourceGroups/" + resourceGroup +
		"/providers/" + providerName +
		"/" + resourceFlexServers + "/" + server
}

// serverState maps the portable lifecycle to the Azure MySQL Flex
// ServerState enum.
func serverState(state string) string {
	switch state {
	case rdsdriver.StateAvailable:
		return stateReady
	case rdsdriver.StateStopped:
		return stateStopped
	case rdsdriver.StateStarting:
		return stateStarting
	case rdsdriver.StateStopping:
		return stateStopping
	case rdsdriver.StateModifying, rdsdriver.StateRebooting:
		return stateUpdating
	case rdsdriver.StateDeleting:
		return stateDropping
	default:
		return stateReady
	}
}
