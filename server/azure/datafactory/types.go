package datafactory

import dfdriver "github.com/stackshy/cloudemu/v2/services/datafactory/driver"

// Azure ARM JSON wire structures for Microsoft.DataFactory/factories
// (api-version 2018-06-01). Field names match what the real armdatafactory
// client emits and expects.
//
// identity is TOP-LEVEL (outside the properties object the server-wide echo
// reaches); publicNetworkAccess is an explicit enum; provisioningState,
// createTime and version are computed read-only outputs. globalParameters is
// modeled; every other property under "properties" round-trips verbatim through
// the generic OtherProps map so deferred sub-surfaces stay round-trip-safe.

const (
	providerName  = "Microsoft.DataFactory"
	factoriesType = "factories"

	factoryResourceType = "Microsoft.DataFactory/factories"

	provisioningStateKey = "provisioningState"
	createTimeKey        = "createTime"
	versionKey           = "version"
	publicNetworkKey     = "publicNetworkAccess"
	globalParametersKey  = "globalParameters"
)

// factoryJSON is the ARM Factory wire body. Properties is generic so the modeled
// publicNetworkAccess/globalParameters and every deferred property (repo,
// purview, encryption) coexist in one object.
type factoryJSON struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Type       string            `json:"type,omitempty"`
	Location   string            `json:"location,omitempty"`
	ETag       string            `json:"eTag,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
	Identity   *identityJSON     `json:"identity,omitempty"`
	Properties map[string]any    `json:"properties,omitempty"`
}

// identityJSON is the top-level FactoryIdentity envelope. principalId/tenantId
// are computed (populated for a system-assigned identity).
type identityJSON struct {
	Type                   string                       `json:"type,omitempty"`
	PrincipalID            string                       `json:"principalId,omitempty"`
	TenantID               string                       `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]*userAssignedJSON `json:"userAssignedIdentities,omitempty"`
}

type userAssignedJSON struct {
	PrincipalID string `json:"principalId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// globalParameterJSON is one properties.globalParameters entry.
type globalParameterJSON struct {
	Type  string `json:"type,omitempty"`
	Value any    `json:"value,omitempty"`
}

// factoryListResult is the ARM list envelope.
type factoryListResult struct {
	Value    []factoryJSON `json:"value"`
	NextLink string        `json:"nextLink,omitempty"`
}

// toIdentityJSON converts a stored identity to the wire shape.
func toIdentityJSON(id *dfdriver.ManagedIdentity) *identityJSON {
	if id == nil {
		return nil
	}

	out := &identityJSON{
		Type:        id.Type,
		PrincipalID: id.PrincipalID,
		TenantID:    id.TenantID,
	}

	if len(id.UserAssigned) > 0 {
		out.UserAssignedIdentities = make(map[string]*userAssignedJSON, len(id.UserAssigned))
		for _, u := range id.UserAssigned {
			out.UserAssignedIdentities[u] = &userAssignedJSON{}
		}
	}

	return out
}

// fromIdentityJSON converts an inbound wire identity to the driver shape.
func fromIdentityJSON(id *identityJSON) *dfdriver.ManagedIdentity {
	if id == nil {
		return nil
	}

	out := &dfdriver.ManagedIdentity{Type: id.Type}
	for k := range id.UserAssignedIdentities {
		out.UserAssigned = append(out.UserAssigned, k)
	}

	return out
}
