package appconfiguration

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/appconfiguration"
)

// configStoreRequest is the ARM PUT/PATCH body. location, tags, sku and identity
// are top-level; the remaining configuration lives under properties.
type configStoreRequest struct {
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Identity   *identityRequest   `json:"identity,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// skuWire is the sku block on the wire — a single-field object {name}.
type skuWire struct {
	Name string `json:"name"`
}

// identityRequest is the writable half of the identity block; the minted ids are
// read-only. userAssignedIdentities is a map of ARM ids to (empty) objects on
// input.
type identityRequest struct {
	Type         string                     `json:"type"`
	UserAssigned map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
}

// propertiesRequest is the writable subset of properties.
type propertiesRequest struct {
	DisableLocalAuth          *bool               `json:"disableLocalAuth,omitempty"`
	EnablePurgeProtection     *bool               `json:"enablePurgeProtection,omitempty"`
	PublicNetworkAccess       string              `json:"publicNetworkAccess,omitempty"`
	SoftDeleteRetentionInDays *int                `json:"softDeleteRetentionInDays,omitempty"`
	Encryption                *encryptionWire     `json:"encryption,omitempty"`
	DataPlaneProxy            *dataPlaneProxyWire `json:"dataPlaneProxy,omitempty"`
}

type encryptionWire struct {
	KeyVaultProperties *keyVaultPropsWire `json:"keyVaultProperties,omitempty"`
}

type keyVaultPropsWire struct {
	KeyIdentifier    string `json:"keyIdentifier,omitempty"`
	IdentityClientID string `json:"identityClientId,omitempty"`
}

type dataPlaneProxyWire struct {
	AuthenticationMode    string `json:"authenticationMode,omitempty"`
	PrivateLinkDelegation string `json:"privateLinkDelegation,omitempty"`
}

// configStoreResponse is the ARM representation of a configuration store.
type configStoreResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Identity   *identityResponse  `json:"identity,omitempty"`
	Properties propertiesResponse `json:"properties"`
}

// identityResponse carries the identity block, including the service-minted ids.
type identityResponse struct {
	Type         string                         `json:"type"`
	PrincipalID  string                         `json:"principalId,omitempty"`
	TenantID     string                         `json:"tenantId,omitempty"`
	UserAssigned map[string]userAssignedWireVal `json:"userAssignedIdentities,omitempty"`
}

// userAssignedWireVal is the minted id pair for a user-assigned identity.
type userAssignedWireVal struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// propertiesResponse is the properties block. The computed fields (endpoint,
// provisioningState, creationDate) are stable.
type propertiesResponse struct {
	ProvisioningState         string              `json:"provisioningState"`
	CreationDate              string              `json:"creationDate,omitempty"`
	Endpoint                  string              `json:"endpoint"`
	DisableLocalAuth          *bool               `json:"disableLocalAuth,omitempty"`
	EnablePurgeProtection     *bool               `json:"enablePurgeProtection,omitempty"`
	PublicNetworkAccess       string              `json:"publicNetworkAccess,omitempty"`
	SoftDeleteRetentionInDays *int                `json:"softDeleteRetentionInDays,omitempty"`
	Encryption                *encryptionWire     `json:"encryption,omitempty"`
	DataPlaneProxy            *dataPlaneProxyWire `json:"dataPlaneProxy,omitempty"`
}

// apiKeyWire is one entry of the ARM listKeys action body.
type apiKeyWire struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Value            string `json:"value"`
	ConnectionString string `json:"connectionString"`
	LastModified     string `json:"lastModified,omitempty"`
	ReadOnly         bool   `json:"readOnly"`
}

// keysResponse is the ARM listKeys action body.
type keysResponse struct {
	Value []apiKeyWire `json:"value"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []configStoreResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *appconfiguration.ConfigurationStore) configStoreResponse {
	return configStoreResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Tags:       s.Tags,
		Sku:        toSkuWire(s.Sku),
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}
}

func toPropertiesResponse(s *appconfiguration.ConfigurationStore) propertiesResponse {
	return propertiesResponse{
		ProvisioningState:         s.ProvisioningState,
		CreationDate:              s.CreationDate,
		Endpoint:                  s.Endpoint,
		DisableLocalAuth:          s.DisableLocalAuth,
		EnablePurgeProtection:     s.EnablePurgeProtection,
		PublicNetworkAccess:       s.PublicNetworkAccess,
		SoftDeleteRetentionInDays: s.SoftDeleteRetentionInDays,
		Encryption:                toEncryptionWire(s.Encryption),
		DataPlaneProxy:            toDataPlaneProxyWire(s.DataPlaneProxy),
	}
}

// toKeyWire projects one stored key onto its ARM apiKeyWire representation.
func toKeyWire(s *appconfiguration.ConfigurationStore, k *appconfiguration.AccessKey) apiKeyWire {
	return apiKeyWire{
		ID:               k.ID,
		Name:             k.Name,
		Value:            k.Value,
		ConnectionString: s.ConnectionString(k),
		LastModified:     s.CreationDate,
		ReadOnly:         k.ReadOnly,
	}
}

func toKeysResponse(s *appconfiguration.ConfigurationStore) keysResponse {
	out := keysResponse{Value: make([]apiKeyWire, 0, len(s.Keys))}

	for i := range s.Keys {
		out.Value = append(out.Value, toKeyWire(s, &s.Keys[i]))
	}

	return out
}

func toSkuWire(in *appconfiguration.Sku) *skuWire {
	if in == nil {
		return nil
	}

	return &skuWire{Name: in.Name}
}

func toIdentityResponse(in *appconfiguration.Identity) *identityResponse {
	if in == nil {
		return nil
	}

	out := &identityResponse{Type: in.Type, PrincipalID: in.PrincipalID, TenantID: in.TenantID}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]userAssignedWireVal, len(in.UserAssigned))
		for id, v := range in.UserAssigned {
			out.UserAssigned[id] = userAssignedWireVal{PrincipalID: v.PrincipalID, ClientID: v.ClientID}
		}
	}

	return out
}

func toEncryptionWire(in *appconfiguration.Encryption) *encryptionWire {
	if in == nil {
		return nil
	}

	out := &encryptionWire{}
	if in.KeyVaultProperties != nil {
		out.KeyVaultProperties = &keyVaultPropsWire{
			KeyIdentifier:    in.KeyVaultProperties.KeyIdentifier,
			IdentityClientID: in.KeyVaultProperties.IdentityClientID,
		}
	}

	return out
}

func toDataPlaneProxyWire(in *appconfiguration.DataPlaneProxy) *dataPlaneProxyWire {
	if in == nil {
		return nil
	}

	return &dataPlaneProxyWire{
		AuthenticationMode:    in.AuthenticationMode,
		PrivateLinkDelegation: in.PrivateLinkDelegation,
	}
}

// toDriverSku maps a wire sku onto the driver sku.
func toDriverSku(in *skuWire) *appconfiguration.Sku {
	if in == nil {
		return nil
	}

	return &appconfiguration.Sku{Name: in.Name}
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only
// the type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *appconfiguration.Identity {
	if in == nil {
		return nil
	}

	out := &appconfiguration.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]appconfiguration.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = appconfiguration.UserAssignedValue{}
		}
	}

	return out
}

func toDriverEncryption(in *encryptionWire) *appconfiguration.Encryption {
	if in == nil {
		return nil
	}

	out := &appconfiguration.Encryption{}
	if in.KeyVaultProperties != nil {
		out.KeyVaultProperties = &appconfiguration.KeyVaultProperties{
			KeyIdentifier:    in.KeyVaultProperties.KeyIdentifier,
			IdentityClientID: in.KeyVaultProperties.IdentityClientID,
		}
	}

	return out
}

func toDriverDataPlaneProxy(in *dataPlaneProxyWire) *appconfiguration.DataPlaneProxy {
	if in == nil {
		return nil
	}

	return &appconfiguration.DataPlaneProxy{
		AuthenticationMode:    in.AuthenticationMode,
		PrivateLinkDelegation: in.PrivateLinkDelegation,
	}
}
