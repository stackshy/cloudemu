package cosmospostgresql

import (
	"fmt"

	cpgdriver "github.com/stackshy/cloudemu/v2/services/cosmospostgresql/driver"
)

const (
	providerName = "Microsoft.DBforPostgreSQL"
	resourceType = "serverGroupsv2"

	subFirewallRules   = "firewallRules"
	subRoles           = "roles"
	subServers         = "servers"
	subConfigurations  = "configurations"
	subCoordinatorCfgs = "coordinatorConfigurations"
	subNodeCfgs        = "nodeConfigurations"
	subPrivateEPs      = "privateEndpointConnections"
	subPrivateLinks    = "privateLinkResources"

	actionRestart = "restart"
	actionStart   = "start"
	actionStop    = "stop"
	actionPromote = "promote"

	resourceCheckName    = "checkNameAvailability"
	resourceLocations    = "locations"
	subOperationStatuses = "operationStatuses"

	clusterResourceType = providerName + "/" + resourceType
)

// armList is the ARM list-response envelope.
type armList[T any] struct {
	Value    []T    `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

type maintenanceWindow struct {
	CustomWindow string `json:"customWindow,omitempty"`
	DayOfWeek    *int   `json:"dayOfWeek,omitempty"`
	StartHour    *int   `json:"startHour,omitempty"`
	StartMinute  *int   `json:"startMinute,omitempty"`
}

type serverNameItem struct {
	Name                     string `json:"name,omitempty"`
	FullyQualifiedDomainName string `json:"fullyQualifiedDomainName,omitempty"`
}

// clusterResource is the ARM JSON shape for a server-group cluster.
type clusterResource struct {
	ID         string             `json:"id,omitempty"`
	Name       string             `json:"name,omitempty"`
	Type       string             `json:"type,omitempty"`
	Location   string             `json:"location,omitempty"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties *clusterProperties `json:"properties,omitempty"`
}

type clusterProperties struct {
	AdministratorLogin              string             `json:"administratorLogin,omitempty"`
	AdministratorLoginPassword      string             `json:"administratorLoginPassword,omitempty"`
	CitusVersion                    string             `json:"citusVersion,omitempty"`
	PostgresqlVersion               string             `json:"postgresqlVersion,omitempty"`
	ProvisioningState               string             `json:"provisioningState,omitempty"`
	State                           string             `json:"state,omitempty"`
	CoordinatorServerEdition        string             `json:"coordinatorServerEdition,omitempty"`
	CoordinatorVCores               *int               `json:"coordinatorVCores,omitempty"`
	CoordinatorStorageQuotaInMb     *int               `json:"coordinatorStorageQuotaInMb,omitempty"`
	CoordinatorEnablePublicIPAccess *bool              `json:"coordinatorEnablePublicIpAccess,omitempty"`
	EnableShardsOnCoordinator       *bool              `json:"enableShardsOnCoordinator,omitempty"`
	NodeServerEdition               string             `json:"nodeServerEdition,omitempty"`
	NodeCount                       *int               `json:"nodeCount,omitempty"`
	NodeVCores                      *int               `json:"nodeVCores,omitempty"`
	NodeStorageQuotaInMb            *int               `json:"nodeStorageQuotaInMb,omitempty"`
	NodeEnablePublicIPAccess        *bool              `json:"nodeEnablePublicIpAccess,omitempty"`
	EnableHa                        *bool              `json:"enableHa,omitempty"`
	PreferredPrimaryZone            string             `json:"preferredPrimaryZone,omitempty"`
	MaintenanceWindow               *maintenanceWindow `json:"maintenanceWindow,omitempty"`
	SourceResourceID                string             `json:"sourceResourceId,omitempty"`
	SourceLocation                  string             `json:"sourceLocation,omitempty"`
	ReadReplicas                    []string           `json:"readReplicas,omitempty"`
	ServerNames                     []serverNameItem   `json:"serverNames,omitempty"`
}

type firewallRuleResource struct {
	ID         string                  `json:"id,omitempty"`
	Name       string                  `json:"name,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Properties *firewallRuleProperties `json:"properties,omitempty"`
}

type firewallRuleProperties struct {
	ProvisioningState string `json:"provisioningState,omitempty"`
	StartIPAddress    string `json:"startIpAddress,omitempty"`
	EndIPAddress      string `json:"endIpAddress,omitempty"`
}

type roleResource struct {
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type,omitempty"`
	Properties *roleProperties `json:"properties,omitempty"`
}

type roleProperties struct {
	Password          string `json:"password,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

type serverResource struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Properties *serverProperties `json:"properties,omitempty"`
}

type serverProperties struct {
	AdministratorLogin       string `json:"administratorLogin,omitempty"`
	Role                     string `json:"role,omitempty"`
	State                    string `json:"state,omitempty"`
	HaState                  string `json:"haState,omitempty"`
	FullyQualifiedDomainName string `json:"fullyQualifiedDomainName,omitempty"`
	ServerEdition            string `json:"serverEdition,omitempty"`
	VCores                   int    `json:"vCores,omitempty"`
	StorageQuotaInMb         int    `json:"storageQuotaInMb,omitempty"`
	CitusVersion             string `json:"citusVersion,omitempty"`
	PostgresqlVersion        string `json:"postgresqlVersion,omitempty"`
	EnableHa                 *bool  `json:"enableHa,omitempty"`
	EnablePublicIPAccess     *bool  `json:"enablePublicIpAccess,omitempty"`
	IsReadOnly               *bool  `json:"isReadOnly,omitempty"`
}

type configurationResource struct {
	ID         string                   `json:"id,omitempty"`
	Name       string                   `json:"name,omitempty"`
	Type       string                   `json:"type,omitempty"`
	Properties *configurationProperties `json:"properties,omitempty"`
}

type configurationProperties struct {
	ProvisioningState             string            `json:"provisioningState,omitempty"`
	Description                   string            `json:"description,omitempty"`
	DataType                      string            `json:"dataType,omitempty"`
	AllowedValues                 string            `json:"allowedValues,omitempty"`
	RequiresRestart               *bool             `json:"requiresRestart,omitempty"`
	ServerRoleGroupConfigurations []roleGroupConfig `json:"serverRoleGroupConfigurations,omitempty"`
}

type roleGroupConfig struct {
	Role         string `json:"role,omitempty"`
	Value        string `json:"value,omitempty"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Source       string `json:"source,omitempty"`
}

type serverConfigurationResource struct {
	ID         string                         `json:"id,omitempty"`
	Name       string                         `json:"name,omitempty"`
	Type       string                         `json:"type,omitempty"`
	Properties *serverConfigurationProperties `json:"properties,omitempty"`
}

type serverConfigurationProperties struct {
	Value             string `json:"value,omitempty"`
	DefaultValue      string `json:"defaultValue,omitempty"`
	Description       string `json:"description,omitempty"`
	DataType          string `json:"dataType,omitempty"`
	AllowedValues     string `json:"allowedValues,omitempty"`
	Source            string `json:"source,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
	RequiresRestart   *bool  `json:"requiresRestart,omitempty"`
}

type privateEndpointConnectionResource struct {
	ID         string                    `json:"id,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Type       string                    `json:"type,omitempty"`
	Properties *privateEndpointConnProps `json:"properties,omitempty"`
}

type privateEndpointConnProps struct {
	ProvisioningState                 string                `json:"provisioningState,omitempty"`
	GroupIDs                          []string              `json:"groupIds,omitempty"`
	PrivateEndpoint                   *privateEndpointRef   `json:"privateEndpoint,omitempty"`
	PrivateLinkServiceConnectionState *linkServiceConnState `json:"privateLinkServiceConnectionState,omitempty"`
}

type privateEndpointRef struct {
	ID string `json:"id,omitempty"`
}

type linkServiceConnState struct {
	Status          string `json:"status,omitempty"`
	Description     string `json:"description,omitempty"`
	ActionsRequired string `json:"actionsRequired,omitempty"`
}

type privateLinkResource struct {
	ID         string                    `json:"id,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Type       string                    `json:"type,omitempty"`
	Properties *privateLinkResourceProps `json:"properties,omitempty"`
}

type privateLinkResourceProps struct {
	GroupID           string   `json:"groupId,omitempty"`
	RequiredMembers   []string `json:"requiredMembers,omitempty"`
	RequiredZoneNames []string `json:"requiredZoneNames,omitempty"`
}

type nameAvailabilityRequest struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

type nameAvailabilityResult struct {
	Name          string `json:"name,omitempty"`
	Type          string `json:"type,omitempty"`
	NameAvailable *bool  `json:"nameAvailable,omitempty"`
	Message       string `json:"message,omitempty"`
}

func boolPtr(b bool) *bool { return &b }

func intPtr(i int) *int { return &i }

func cloneMW(in *cpgdriver.MaintenanceWindow) *maintenanceWindow {
	if in == nil {
		return nil
	}

	return &maintenanceWindow{
		CustomWindow: in.CustomWindow, DayOfWeek: intPtr(in.DayOfWeek),
		StartHour: intPtr(in.StartHour), StartMinute: intPtr(in.StartMinute),
	}
}

// nodeFQDN builds a node's fully-qualified domain name, matching the servers
// sub-resource: <node>.<location>.postgres.cosmos.azure.com.
func nodeFQDN(node, location string) string {
	if location == "" {
		location = "eastus"
	}

	return node + "." + location + ".postgres.cosmos.azure.com"
}

// serverNames enumerates the cluster's nodes (coordinator + workers), matching
// the servers sub-resource in both the set and the FQDN.
func serverNames(c *cpgdriver.Cluster) []serverNameItem {
	coord := c.Name + "-c"
	items := []serverNameItem{{Name: coord, FullyQualifiedDomainName: nodeFQDN(coord, c.Location)}}

	for i := 0; i < c.NodeCount; i++ {
		w := fmt.Sprintf("%s-w%d", c.Name, i)
		items = append(items, serverNameItem{Name: w, FullyQualifiedDomainName: nodeFQDN(w, c.Location)})
	}

	return items
}

// toARMCluster converts a driver Cluster to ARM JSON.
func toARMCluster(c *cpgdriver.Cluster, id string) clusterResource {
	return clusterResource{
		ID:       id,
		Name:     c.Name,
		Type:     clusterResourceType,
		Location: c.Location,
		Tags:     c.Tags,
		Properties: &clusterProperties{
			AdministratorLogin:              c.AdministratorLogin,
			CitusVersion:                    c.CitusVersion,
			PostgresqlVersion:               c.PostgresqlVersion,
			ProvisioningState:               c.ProvisioningState,
			State:                           c.State,
			CoordinatorServerEdition:        c.CoordinatorServerEdition,
			CoordinatorVCores:               intPtr(c.CoordinatorVCores),
			CoordinatorStorageQuotaInMb:     intPtr(c.CoordinatorStorageQuotaInMb),
			CoordinatorEnablePublicIPAccess: boolPtr(c.CoordinatorEnablePublicIPAccess),
			EnableShardsOnCoordinator:       boolPtr(c.EnableShardsOnCoordinator),
			NodeServerEdition:               c.NodeServerEdition,
			NodeCount:                       intPtr(c.NodeCount),
			NodeVCores:                      intPtr(c.NodeVCores),
			NodeStorageQuotaInMb:            intPtr(c.NodeStorageQuotaInMb),
			NodeEnablePublicIPAccess:        boolPtr(c.NodeEnablePublicIPAccess),
			EnableHa:                        boolPtr(c.EnableHa),
			PreferredPrimaryZone:            c.PreferredPrimaryZone,
			MaintenanceWindow:               cloneMW(c.MaintenanceWindow),
			SourceResourceID:                c.SourceResourceID,
			SourceLocation:                  c.SourceLocation,
			ReadReplicas:                    c.ReadReplicas,
			ServerNames:                     serverNames(c),
		},
	}
}

func toARMFirewallRule(fr *cpgdriver.FirewallRule, id string) firewallRuleResource {
	return firewallRuleResource{
		ID:   id,
		Name: fr.Name,
		Type: clusterResourceType + "/" + subFirewallRules,
		Properties: &firewallRuleProperties{
			ProvisioningState: fr.ProvisioningState,
			StartIPAddress:    fr.StartIPAddress,
			EndIPAddress:      fr.EndIPAddress,
		},
	}
}

func toARMRole(role *cpgdriver.Role, id string) roleResource {
	return roleResource{
		ID:         id,
		Name:       role.Name,
		Type:       clusterResourceType + "/" + subRoles,
		Properties: &roleProperties{ProvisioningState: role.ProvisioningState},
	}
}

func toARMServer(s *cpgdriver.Server, id string) serverResource {
	return serverResource{
		ID:   id,
		Name: s.Name,
		Type: clusterResourceType + "/" + subServers,
		Properties: &serverProperties{
			AdministratorLogin:       s.AdministratorLogin,
			Role:                     s.Role,
			State:                    s.State,
			HaState:                  s.HaState,
			FullyQualifiedDomainName: s.FullyQualifiedDomainName,
			ServerEdition:            s.ServerEdition,
			VCores:                   s.VCores,
			StorageQuotaInMb:         s.StorageQuotaInMb,
			CitusVersion:             s.CitusVersion,
			PostgresqlVersion:        s.PostgresqlVersion,
			EnableHa:                 boolPtr(s.EnableHa),
			EnablePublicIPAccess:     boolPtr(s.EnablePublicIPAccess),
			IsReadOnly:               boolPtr(s.IsReadOnly),
		},
	}
}

func toARMConfiguration(c *cpgdriver.Configuration, id string) configurationResource {
	groups := make([]roleGroupConfig, 0, len(c.RoleGroups))

	for i := range c.RoleGroups {
		g := &c.RoleGroups[i]
		groups = append(groups, roleGroupConfig{
			Role: g.Role, Value: g.Value, DefaultValue: g.DefaultValue, Source: g.Source,
		})
	}

	return configurationResource{
		ID:   id,
		Name: c.Name,
		Type: clusterResourceType + "/" + subConfigurations,
		Properties: &configurationProperties{
			ProvisioningState:             c.ProvisioningState,
			Description:                   c.Description,
			DataType:                      c.DataType,
			AllowedValues:                 c.AllowedValues,
			RequiresRestart:               boolPtr(c.RequiresRestart),
			ServerRoleGroupConfigurations: groups,
		},
	}
}

func toARMServerConfiguration(sc *cpgdriver.ServerConfiguration, id string) serverConfigurationResource {
	return serverConfigurationResource{
		ID:   id,
		Name: sc.Name,
		Type: clusterResourceType + "/" + subConfigurations,
		Properties: &serverConfigurationProperties{
			Value:             sc.Value,
			DefaultValue:      sc.DefaultValue,
			Description:       sc.Description,
			DataType:          sc.DataType,
			AllowedValues:     sc.AllowedValues,
			Source:            sc.Source,
			ProvisioningState: sc.ProvisioningState,
			RequiresRestart:   boolPtr(sc.RequiresRestart),
		},
	}
}

func toARMPrivateEndpointConnection(pec *cpgdriver.PrivateEndpointConnection, id string) privateEndpointConnectionResource {
	return privateEndpointConnectionResource{
		ID:   id,
		Name: pec.Name,
		Type: clusterResourceType + "/" + subPrivateEPs,
		Properties: &privateEndpointConnProps{
			ProvisioningState: pec.ProvisioningState,
			GroupIDs:          pec.GroupIDs,
			PrivateEndpoint:   &privateEndpointRef{ID: pec.PrivateEndpointID},
			PrivateLinkServiceConnectionState: &linkServiceConnState{
				Status: pec.ConnectionStatus, Description: pec.ConnectionDesc, ActionsRequired: pec.ActionsRequired,
			},
		},
	}
}

func toARMPrivateLinkResource(plr *cpgdriver.PrivateLinkResource, id string) privateLinkResource {
	return privateLinkResource{
		ID:   id,
		Name: plr.Name,
		Type: clusterResourceType + "/" + subPrivateLinks,
		Properties: &privateLinkResourceProps{
			GroupID:           plr.GroupID,
			RequiredMembers:   plr.RequiredMembers,
			RequiredZoneNames: plr.RequiredZoneNames,
		},
	}
}
