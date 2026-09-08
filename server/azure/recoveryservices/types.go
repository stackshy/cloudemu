package recoveryservices

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/recoveryservices"
)

// vaultRequest is the ARM vault PUT/PATCH body. location, tags, sku and identity
// are top-level; the remaining vault properties live under properties and
// round-trip verbatim.
type vaultRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Sku        *skuWire          `json:"sku,omitempty"`
	Identity   *identityWire     `json:"identity,omitempty"`
	Properties json.RawMessage   `json:"properties,omitempty"`
}

// skuWire is the vault SKU block (name is Standard or RS0; tier is optional).
type skuWire struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

// identityWire is the vault managed-identity request/response block. On a request
// only type and userAssignedIdentities are read; on a response principalId and
// tenantId are the computed, stable values.
type identityWire struct {
	Type                   string                     `json:"type,omitempty"`
	PrincipalID            string                     `json:"principalId,omitempty"`
	TenantID               string                     `json:"tenantId,omitempty"`
	UserAssignedIdentities map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// vaultResponse is the ARM representation of a vault.
type vaultResponse struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Sku        skuWire           `json:"sku"`
	Identity   *identityWire     `json:"identity,omitempty"`
	Etag       string            `json:"etag"`
	Properties json.RawMessage   `json:"properties"`
}

// vaultListResponse is the ARM vault list envelope. nextLink is omitted — the
// emulator returns a single page.
type vaultListResponse struct {
	Value []vaultResponse `json:"value"`
}

// configRequest is the ARM backup vault/storage config PUT/PATCH body: a
// properties block that round-trips verbatim.
type configRequest struct {
	Properties json.RawMessage `json:"properties,omitempty"`
}

// configResponse is the ARM representation of a backup vault/storage config
// singleton, with the stable etag surfaced as the top-level eTag.
type configResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Etag       string          `json:"eTag,omitempty"`
	Properties json.RawMessage `json:"properties"`
}

// policyRequest is the ARM backup-policy PUT body: a properties block
// (backupManagementType, schedulePolicy, retentionPolicy, …) round-tripped
// verbatim.
type policyRequest struct {
	Properties json.RawMessage `json:"properties,omitempty"`
}

// policyResponse is the ARM representation of a backup policy.
type policyResponse struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Etag       string          `json:"etag"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

// policyListResponse is the ARM backup-policy list envelope.
type policyListResponse struct {
	Value []policyResponse `json:"value"`
}

// vaultInputFromRequest builds a vault create/update Input from a request body.
func vaultInputFromRequest(req *vaultRequest) recoveryservices.VaultInput {
	in := recoveryservices.VaultInput{Tags: req.Tags, Properties: req.Properties}

	if req.Sku != nil {
		name := req.Sku.Name
		tier := req.Sku.Tier
		in.SkuName = &name
		in.SkuTier = &tier
	}

	if req.Identity != nil {
		in.Identity = &recoveryservices.ManagedIdentity{
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

// toVaultResponse projects a stored vault onto the ARM wire representation,
// injecting the stable provisioningState into the properties block.
func toVaultResponse(v *recoveryservices.Vault) vaultResponse {
	return vaultResponse{
		ID:         v.ARMID(),
		Name:       v.Name,
		Type:       vaultArmType,
		Location:   v.Location,
		Tags:       v.Tags,
		Sku:        skuWire{Name: v.SkuName, Tier: v.SkuTier},
		Identity:   toIdentityWire(v.Identity),
		Etag:       v.Etag,
		Properties: propertiesWith(v.Properties, "provisioningState", v.ProvisioningState),
	}
}

// toIdentityWire projects a stored managed identity onto the wire block,
// synthesizing per-identity principal/client ids for each user-assigned entry.
func toIdentityWire(id *recoveryservices.ManagedIdentity) *identityWire {
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

// toConfigResponse projects stored config properties onto the ARM wire shape.
func toConfigResponse(id, name, armType, etag string, props json.RawMessage) configResponse {
	return configResponse{ID: id, Name: name, Type: armType, Etag: etag, Properties: props}
}

// toPolicyResponse projects a stored policy onto its ARM wire representation.
func toPolicyResponse(p *recoveryservices.BackupPolicy) policyResponse {
	return policyResponse{
		ID:         p.ARMID(),
		Name:       p.Name,
		Type:       p.ARMType(),
		Etag:       p.Etag,
		Properties: p.Properties,
	}
}
