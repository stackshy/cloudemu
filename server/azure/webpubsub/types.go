package webpubsub

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/webpubsub"
)

// webPubSubRequest is the ARM PUT/PATCH body. location, tags, kind, sku and
// identity are top-level; the remaining configuration lives under properties.
type webPubSubRequest struct {
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Sku        *skuWire           `json:"sku,omitempty"`
	Identity   *identityRequest   `json:"identity,omitempty"`
	Properties *propertiesRequest `json:"properties,omitempty"`
}

// skuWire is the sku block on the wire. tier and size are read-only (derived
// from name); the client sends name and capacity.
type skuWire struct {
	Name     string `json:"name"`
	Tier     string `json:"tier,omitempty"`
	Size     string `json:"size,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
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
	PublicNetworkAccess string         `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth    *bool          `json:"disableLocalAuth,omitempty"`
	DisableAadAuth      *bool          `json:"disableAadAuth,omitempty"`
	TLS                 *tlsWire       `json:"tls,omitempty"`
	LiveTrace           *liveTraceWire `json:"liveTraceConfiguration,omitempty"`
}

type tlsWire struct {
	ClientCertEnabled *bool `json:"clientCertEnabled,omitempty"`
}

type liveTraceWire struct {
	Enabled    string                  `json:"enabled,omitempty"`
	Categories []liveTraceCategoryWire `json:"categories,omitempty"`
}

type liveTraceCategoryWire struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
}

// webPubSubResponse is the ARM representation of a webPubSub resource.
type webPubSubResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Kind       string             `json:"kind,omitempty"`
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

// propertiesResponse is the properties block. The computed fields (hostName,
// externalIP, publicPort, serverPort, version, provisioningState) are stable.
type propertiesResponse struct {
	ProvisioningState   string         `json:"provisioningState"`
	ExternalIP          string         `json:"externalIP"`
	HostName            string         `json:"hostName"`
	PublicPort          int            `json:"publicPort"`
	ServerPort          int            `json:"serverPort"`
	Version             string         `json:"version"`
	PublicNetworkAccess string         `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth    *bool          `json:"disableLocalAuth,omitempty"`
	DisableAadAuth      *bool          `json:"disableAadAuth,omitempty"`
	TLS                 *tlsWire       `json:"tls,omitempty"`
	LiveTrace           *liveTraceWire `json:"liveTraceConfiguration,omitempty"`
}

// keysResponse is the ARM listKeys action body.
type keysResponse struct {
	PrimaryKey              string `json:"primaryKey"`
	SecondaryKey            string `json:"secondaryKey"`
	PrimaryConnectionString string `json:"primaryConnectionString"`
	//nolint:tagliatelle // ARM wire name is "secondaryConnectionString"
	SecondaryConnectionString string `json:"secondaryConnectionString"`
}

// listResponse is the ARM list envelope. nextLink is omitted — the emulator
// returns a single page.
type listResponse struct {
	Value []webPubSubResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *webpubsub.WebPubSub) webPubSubResponse {
	return webPubSubResponse{
		ID:         s.ARMID(),
		Name:       s.Name,
		Type:       armType,
		Location:   s.Location,
		Kind:       s.Kind,
		Tags:       s.Tags,
		Sku:        toSkuWire(s.Sku),
		Identity:   toIdentityResponse(s.Identity),
		Properties: toPropertiesResponse(s),
	}
}

func toPropertiesResponse(s *webpubsub.WebPubSub) propertiesResponse {
	return propertiesResponse{
		ProvisioningState:   s.ProvisioningState,
		ExternalIP:          s.ExternalIP,
		HostName:            s.HostName,
		PublicPort:          s.PublicPort,
		ServerPort:          s.ServerPort,
		Version:             s.Version,
		PublicNetworkAccess: s.PublicNetworkAccess,
		DisableLocalAuth:    s.DisableLocalAuth,
		DisableAadAuth:      s.DisableAadAuth,
		TLS:                 toTLSWire(s.TLSClientCertEnabled),
		LiveTrace:           toLiveTraceWire(s.LiveTrace),
	}
}

func toKeysResponse(s *webpubsub.WebPubSub) keysResponse {
	return keysResponse{
		PrimaryKey:                s.PrimaryKey,
		SecondaryKey:              s.SecondaryKey,
		PrimaryConnectionString:   s.PrimaryConnectionString(),
		SecondaryConnectionString: s.SecondaryConnectionString(),
	}
}

func toSkuWire(in *webpubsub.Sku) *skuWire {
	if in == nil {
		return nil
	}

	return &skuWire{Name: in.Name, Tier: in.Tier, Size: in.Size, Capacity: in.Capacity}
}

func toIdentityResponse(in *webpubsub.Identity) *identityResponse {
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

func toTLSWire(clientCertEnabled *bool) *tlsWire {
	if clientCertEnabled == nil {
		return nil
	}

	return &tlsWire{ClientCertEnabled: clientCertEnabled}
}

func toLiveTraceWire(in *webpubsub.LiveTrace) *liveTraceWire {
	if in == nil {
		return nil
	}

	out := &liveTraceWire{Enabled: in.Enabled}
	if len(in.Categories) > 0 {
		out.Categories = make([]liveTraceCategoryWire, 0, len(in.Categories))
		for i := range in.Categories {
			out.Categories = append(out.Categories, liveTraceCategoryWire{
				Name:    in.Categories[i].Name,
				Enabled: in.Categories[i].Enabled,
			})
		}
	}

	return out
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only
// the type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *webpubsub.Identity {
	if in == nil {
		return nil
	}

	out := &webpubsub.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]webpubsub.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = webpubsub.UserAssignedValue{}
		}
	}

	return out
}

// toDriverSku maps a wire sku onto the driver sku (name and capacity only; the
// mock derives tier and size).
func toDriverSku(in *skuWire) *webpubsub.Sku {
	if in == nil {
		return nil
	}

	return &webpubsub.Sku{Name: in.Name, Tier: in.Tier, Size: in.Size, Capacity: in.Capacity}
}

// toDriverLiveTrace maps a wire live-trace block onto the driver type.
func toDriverLiveTrace(in *liveTraceWire) *webpubsub.LiveTrace {
	if in == nil {
		return nil
	}

	out := &webpubsub.LiveTrace{Enabled: in.Enabled}
	if len(in.Categories) > 0 {
		out.Categories = make([]webpubsub.LiveTraceCategory, 0, len(in.Categories))
		for i := range in.Categories {
			out.Categories = append(out.Categories, webpubsub.LiveTraceCategory{
				Name:    in.Categories[i].Name,
				Enabled: in.Categories[i].Enabled,
			})
		}
	}

	return out
}
