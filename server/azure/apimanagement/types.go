package apimanagement

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/providers/azure/apimanagement"
)

// serviceRequest is the ARM service PUT/PATCH body. location, tags, zones, sku
// and identity are top-level; the writable service properties live under
// properties and round-trip verbatim.
type serviceRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Sku        *skuWire          `json:"sku,omitempty"`
	Identity   *identityWire     `json:"identity,omitempty"`
	Properties json.RawMessage   `json:"properties,omitempty"`
}

// skuWire is the service SKU block. Capacity is a pointer so a request that
// omits it is distinguishable from an explicit 0 (the Consumption tier).
type skuWire struct {
	Name     string `json:"name,omitempty"`
	Capacity *int32 `json:"capacity,omitempty"`
}

// skuResponse is the SKU block as returned: capacity is always present.
type skuResponse struct {
	Name     string `json:"name"`
	Capacity int32  `json:"capacity"`
}

// identityWire is the managed-identity request/response block. On a request
// only type and userAssignedIdentities are read; on a response principalId and
// tenantId are the computed, stable values.
type identityWire struct {
	Type                   string                     `json:"type,omitempty"`
	PrincipalID            string                     `json:"principalId,omitempty"`
	TenantID               string                     `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// serviceResponse is the ARM representation of an API Management service.
type serviceResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Zones      []string          `json:"zones,omitempty"`
	Sku        skuResponse       `json:"sku"`
	Identity   *identityWire     `json:"identity,omitempty"`
	Etag       string            `json:"etag"`
	Properties json.RawMessage   `json:"properties"`
}

// listResponse is the ARM/APIM list envelope; nextLink continues a paged list.
type listResponse[T any] struct {
	Value    []T    `json:"value"`
	Count    *int   `json:"count,omitempty"`
	NextLink string `json:"nextLink,omitempty"`
}

// deletedServiceResponse is a soft-deleted service
// (Microsoft.ApiManagement/deletedservices).
type deletedServiceResponse struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Type       string                   `json:"type"`
	Location   string                   `json:"location"`
	Properties deletedServiceProperties `json:"properties"`
}

type deletedServiceProperties struct {
	ServiceID          string `json:"serviceId"`
	DeletionDate       string `json:"deletionDate"`
	ScheduledPurgeDate string `json:"scheduledPurgeDate"`
}

// childResponse is a service child resource (api, product, policy, portal
// setting).
type childResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// childRequest is a child resource PUT/PATCH body.
type childRequest struct {
	Properties json.RawMessage `json:"properties"`
}

// policyRequest is the service policy PUT body.
type policyRequest struct {
	Properties struct {
		Value  string `json:"value"`
		Format string `json:"format"`
	} `json:"properties"`
}

// tenantAccessResponse is a tenant access entity as GET/PATCH return it (no
// keys).
type tenantAccessResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Properties tenantAccessProperties `json:"properties"`
}

type tenantAccessProperties struct {
	ID          string `json:"id"`
	PrincipalID string `json:"principalId"`
	Enabled     bool   `json:"enabled"`
}

// tenantAccessSecrets is the tenant access listSecrets body.
type tenantAccessSecrets struct {
	ID           string `json:"id"`
	PrincipalID  string `json:"principalId"`
	PrimaryKey   string `json:"primaryKey"`
	SecondaryKey string `json:"secondaryKey"`
	Enabled      bool   `json:"enabled"`
}

// tenantAccessRequest is the tenant access PATCH body.
type tenantAccessRequest struct {
	Properties struct {
		Enabled *bool `json:"enabled"`
	} `json:"properties"`
}

// nameAvailabilityRequest / nameAvailabilityResponse are the
// checkNameAvailability body and verdict.
type nameAvailabilityRequest struct {
	Name string `json:"name"`
}

type nameAvailabilityResponse struct {
	NameAvailable bool   `json:"nameAvailable"`
	Reason        string `json:"reason"`
	Message       string `json:"message,omitempty"`
}

// serviceInputFromRequest builds a service create/update Input from a request
// body and its If-Match header.
func serviceInputFromRequest(req *serviceRequest, ifMatch string) apimanagement.ServiceInput {
	in := apimanagement.ServiceInput{Tags: req.Tags, Zones: req.Zones, Properties: req.Properties, IfMatch: ifMatch}

	if req.Sku != nil {
		if req.Sku.Name != "" {
			name := req.Sku.Name
			in.SkuName = &name
		}

		in.SkuCapacity = req.Sku.Capacity
	}

	if req.Identity != nil {
		in.Identity = &apimanagement.ManagedIdentity{
			Type:            req.Identity.Type,
			UserAssignedIDs: userAssignedKeys(req.Identity.UserAssignedIdentities),
		}
	}

	return in
}

// userAssignedKeys extracts the user-assigned identity resource ids from the
// request map.
func userAssignedKeys(m map[string]json.RawMessage) []string {
	if len(m) == 0 {
		return nil
	}

	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// toServiceResponse projects a stored service onto the ARM wire
// representation. The properties block is the provider's, verbatim: it already
// carries the defaults and computed fields.
func toServiceResponse(s *apimanagement.Service) serviceResponse {
	return serviceResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       serviceArmType,
		Location:   s.Location,
		Tags:       s.Tags,
		Zones:      s.Zones,
		Sku:        skuResponse{Name: s.SkuName, Capacity: s.SkuCapacity},
		Identity:   toIdentityWire(s.Identity),
		Etag:       s.Etag,
		Properties: s.Properties,
	}
}

// toDeletedServiceResponse projects a soft-deleted service onto the wire.
func toDeletedServiceResponse(d *apimanagement.DeletedService) deletedServiceResponse {
	return deletedServiceResponse{
		ID:       d.ARMID(),
		Name:     d.Service.Name,
		Type:     providerName + "/deletedservices",
		Location: d.Service.Location,
		Properties: deletedServiceProperties{
			ServiceID:          d.Service.ARMID(),
			DeletionDate:       d.DeletionDate.UTC().Format(time.RFC3339),
			ScheduledPurgeDate: d.ScheduledPurgeDate.UTC().Format(time.RFC3339),
		},
	}
}

// toChildResponse projects a child resource under serviceID/segment.
func toChildResponse(serviceID, segment string, c *apimanagement.ChildResource) childResponse {
	return childResponse{
		ID:         serviceID + "/" + segment + "/" + c.Name,
		Name:       c.Name,
		Type:       serviceArmType + "/" + segment,
		Properties: c.Properties,
	}
}

// toTenantAccessResponse projects a tenant access entity without its keys.
func toTenantAccessResponse(serviceID string, t *apimanagement.TenantAccess) tenantAccessResponse {
	return tenantAccessResponse{
		ID:         serviceID + "/tenant/" + t.Name,
		Name:       t.Name,
		Type:       serviceArmType + "/tenant",
		Properties: tenantAccessProperties{ID: t.Name, PrincipalID: t.PrincipalID, Enabled: t.Enabled},
	}
}

// toIdentityWire projects a stored managed identity onto the wire block,
// synthesizing per-identity principal/client ids for each user-assigned entry.
func toIdentityWire(id *apimanagement.ManagedIdentity) *identityWire {
	if id == nil {
		return nil
	}

	out := &identityWire{Type: id.Type, PrincipalID: id.PrincipalID, TenantID: id.TenantID}

	if len(id.UserAssignedIDs) > 0 {
		out.UserAssignedIdentities = make(map[string]json.RawMessage, len(id.UserAssignedIDs))
		for _, uaID := range id.UserAssignedIDs {
			out.UserAssignedIdentities[uaID] = userAssignedValue(uaID)
		}
	}

	return out
}

// userAssignedValue synthesizes the deterministic {principalId, clientId}
// block Azure returns for an assigned user identity.
func userAssignedValue(uaID string) json.RawMessage {
	principal := idgen.SyntheticGUID("apimanagement/ua-principal/" + strings.ToLower(uaID))
	client := idgen.SyntheticGUID("apimanagement/ua-client/" + strings.ToLower(uaID))

	raw, err := json.Marshal(map[string]string{"principalId": principal, "clientId": client})
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return raw
}
