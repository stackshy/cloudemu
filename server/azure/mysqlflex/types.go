package mysqlflex

import (
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
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
	AdministratorLogin         string               `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword string               `json:"administratorLoginPassword,omitempty"`
	Version                    string               `json:"version,omitempty"`
	State                      string               `json:"state,omitempty"`
	FullyQualifiedDomainName   string               `json:"fullyQualifiedDomainName,omitempty"`
	Storage                    *armStorage          `json:"storage,omitempty"`
	AvailabilityZone           string               `json:"availabilityZone,omitempty"`
	HighAvailability           *armHighAvailability `json:"highAvailability,omitempty"`
}

// armHighAvailability mirrors properties.highAvailability on a MySQL Flexible
// Server. Mode is Disabled/SameZone/ZoneRedundant; State is computed by the
// service (Healthy when a standby is running, NotEnabled otherwise).
type armHighAvailability struct {
	Mode                    string `json:"mode,omitempty"`
	StandbyAvailabilityZone string `json:"standbyAvailabilityZone,omitempty"`
	State                   string `json:"state,omitempty"`
}

// armServerRestartParameter is the ServerRestartParameter request body for
// POST .../restart. RestartWithFailover is the EnableStatusEnum
// "Enabled"/"Disabled"; MaxFailoverSeconds bounds how long the SDK's poller is
// willing to wait for that failover — the mock's failover is synchronous, so
// it is accepted but not otherwise consulted.
type armServerRestartParameter struct {
	RestartWithFailover string `json:"restartWithFailover,omitempty"`
	MaxFailoverSeconds  int    `json:"maxFailoverSeconds,omitempty"`
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
		Location: inst.Location,
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
			HighAvailability:         highAvailability(inst),
			Storage: &armStorage{
				StorageSizeGB: inst.AllocatedStorage,
				StorageSKU:    inst.StorageType,
			},
		},
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
