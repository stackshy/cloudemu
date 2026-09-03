package postgresflex

import (
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
	AdministratorLogin         string               `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword string               `json:"administratorLoginPassword,omitempty"`
	Version                    string               `json:"version,omitempty"`
	State                      string               `json:"state,omitempty"`
	FullyQualifiedDomainName   string               `json:"fullyQualifiedDomainName,omitempty"`
	Storage                    *armStorage          `json:"storage,omitempty"`
	AvailabilityZone           string               `json:"availabilityZone,omitempty"`
	HighAvailability           *armHighAvailability `json:"highAvailability,omitempty"`
	CreateMode                 string               `json:"createMode,omitempty"`
	SourceServerResourceID     string               `json:"sourceServerResourceId,omitempty"`
}

// armHighAvailability mirrors properties.highAvailability on a PostgreSQL
// Flexible Server. Mode is Disabled/SameZone/ZoneRedundant; State is computed
// by the service (Healthy when a standby is running, NotEnabled otherwise).
type armHighAvailability struct {
	Mode                    string `json:"mode,omitempty"`
	StandbyAvailabilityZone string `json:"standbyAvailabilityZone,omitempty"`
	State                   string `json:"state,omitempty"`
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
		HighAvailability:         highAvailability(inst),
	}

	if inst.AllocatedStorage > 0 {
		props.Storage = &armStorage{StorageSizeGB: inst.AllocatedStorage}
	}

	return armServer{
		ID:       azurearm.BuildResourceID(subscription, resourceGroup, providerName, resourceFlexibleServers, inst.ID),
		Name:     inst.ID,
		Type:     providerName + "/" + resourceFlexibleServers,
		Location: inst.Location,
		Tags:     inst.Tags,
		SKU: &armSKU{
			Name: inst.InstanceClass,
		},
		Properties: props,
	}
}

// highAvailability renders the server's HA configuration for an ARM response.
// The mode defaults to Disabled so GET always reports HA faithfully; State is
// Healthy while a standby is active (the mock provisions synchronously) and
// NotEnabled otherwise.
func highAvailability(inst *rdsdriver.Instance) *armHighAvailability {
	mode := inst.HighAvailabilityMode
	if mode == "" {
		mode = rdsdriver.HAModeDisabled
	}

	state := "NotEnabled"
	if rdsdriver.HAEnabled(mode) {
		state = "Healthy"
	}

	return &armHighAvailability{
		Mode:                    mode,
		StandbyAvailabilityZone: inst.StandbyAvailabilityZone,
		State:                   state,
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
	// The ARM ServerState enum has no "Creating"; a provisioning server reports
	// Starting until it settles to Ready (async lifecycle, AsyncSettle only).
	case rdsdriver.StateCreating, rdsdriver.StateStarting:
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
