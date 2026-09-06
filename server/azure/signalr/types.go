package signalr

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/signalr"
)

// signalRRequest is the ARM PUT/PATCH body. location, tags, kind, sku and
// identity are top-level; the remaining configuration lives under properties.
type signalRRequest struct {
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
	Cors                *corsWire       `json:"cors,omitempty"`
	Features            []featureWire   `json:"features,omitempty"`
	Upstream            *upstreamWire   `json:"upstream,omitempty"`
	PublicNetworkAccess string          `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth    *bool           `json:"disableLocalAuth,omitempty"`
	DisableAadAuth      *bool           `json:"disableAadAuth,omitempty"`
	TLS                 *tlsWire        `json:"tls,omitempty"`
	Serverless          *serverlessWire `json:"serverless,omitempty"`
}

type corsWire struct {
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
}

type featureWire struct {
	Flag       string            `json:"flag"`
	Value      string            `json:"value"`
	Properties map[string]string `json:"properties,omitempty"`
}

type upstreamWire struct {
	Templates []upstreamTemplateWire `json:"templates,omitempty"`
}

type upstreamTemplateWire struct {
	URLTemplate     string `json:"urlTemplate"`
	HubPattern      string `json:"hubPattern,omitempty"`
	EventPattern    string `json:"eventPattern,omitempty"`
	CategoryPattern string `json:"categoryPattern,omitempty"`
}

type tlsWire struct {
	ClientCertEnabled *bool `json:"clientCertEnabled,omitempty"`
}

type serverlessWire struct {
	ConnectionTimeoutInSeconds int `json:"connectionTimeoutInSeconds,omitempty"`
}

// signalRResponse is the ARM representation of a signalR resource.
type signalRResponse struct {
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
	ProvisioningState   string          `json:"provisioningState"`
	ExternalIP          string          `json:"externalIP"`
	HostName            string          `json:"hostName"`
	PublicPort          int             `json:"publicPort"`
	ServerPort          int             `json:"serverPort"`
	Version             string          `json:"version"`
	PublicNetworkAccess string          `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth    *bool           `json:"disableLocalAuth,omitempty"`
	DisableAadAuth      *bool           `json:"disableAadAuth,omitempty"`
	Cors                *corsWire       `json:"cors,omitempty"`
	Features            []featureWire   `json:"features,omitempty"`
	Upstream            *upstreamWire   `json:"upstream,omitempty"`
	TLS                 *tlsWire        `json:"tls,omitempty"`
	Serverless          *serverlessWire `json:"serverless"`
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
	Value []signalRResponse `json:"value"`
}

// toResponse projects a stored resource onto its ARM wire representation.
func toResponse(s *signalr.SignalR) signalRResponse {
	return signalRResponse{
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

func toPropertiesResponse(s *signalr.SignalR) propertiesResponse {
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
		Cors:                toCorsWire(s.Cors),
		Features:            toFeaturesWire(s.Features),
		Upstream:            toUpstreamWire(s.Upstream),
		TLS:                 toTLSWire(s.TLSClientCertEnabled),
		Serverless:          &serverlessWire{ConnectionTimeoutInSeconds: s.ServerlessTimeout},
	}
}

func toKeysResponse(s *signalr.SignalR) keysResponse {
	return keysResponse{
		PrimaryKey:                s.PrimaryKey,
		SecondaryKey:              s.SecondaryKey,
		PrimaryConnectionString:   s.PrimaryConnectionString(),
		SecondaryConnectionString: s.SecondaryConnectionString(),
	}
}

func toSkuWire(in *signalr.Sku) *skuWire {
	if in == nil {
		return nil
	}

	return &skuWire{Name: in.Name, Tier: in.Tier, Size: in.Size, Capacity: in.Capacity}
}

func toIdentityResponse(in *signalr.Identity) *identityResponse {
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

func toCorsWire(in *signalr.Cors) *corsWire {
	if in == nil {
		return nil
	}

	return &corsWire{AllowedOrigins: in.AllowedOrigins}
}

func toFeaturesWire(in []signalr.Feature) []featureWire {
	if len(in) == 0 {
		return nil
	}

	out := make([]featureWire, 0, len(in))
	for i := range in {
		out = append(out, featureWire{Flag: in[i].Flag, Value: in[i].Value, Properties: in[i].Properties})
	}

	return out
}

func toUpstreamWire(in []signalr.UpstreamTemplate) *upstreamWire {
	if len(in) == 0 {
		return nil
	}

	out := &upstreamWire{Templates: make([]upstreamTemplateWire, 0, len(in))}
	for i := range in {
		out.Templates = append(out.Templates, upstreamTemplateWire{
			URLTemplate:     in[i].URLTemplate,
			HubPattern:      in[i].HubPattern,
			EventPattern:    in[i].EventPattern,
			CategoryPattern: in[i].CategoryPattern,
		})
	}

	return out
}

func toTLSWire(clientCertEnabled *bool) *tlsWire {
	if clientCertEnabled == nil {
		return nil
	}

	return &tlsWire{ClientCertEnabled: clientCertEnabled}
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only
// the type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *signalr.Identity {
	if in == nil {
		return nil
	}

	out := &signalr.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]signalr.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = signalr.UserAssignedValue{}
		}
	}

	return out
}

// toDriverSku maps a wire sku onto the driver sku (name and capacity only; the
// mock derives tier and size).
func toDriverSku(in *skuWire) *signalr.Sku {
	if in == nil {
		return nil
	}

	return &signalr.Sku{Name: in.Name, Tier: in.Tier, Size: in.Size, Capacity: in.Capacity}
}

// toDriverCors maps a wire cors onto the driver type.
func toDriverCors(in *corsWire) *signalr.Cors {
	if in == nil {
		return nil
	}

	return &signalr.Cors{AllowedOrigins: in.AllowedOrigins}
}

// toDriverFeatures maps wire features onto the driver type.
func toDriverFeatures(in []featureWire) []signalr.Feature {
	if len(in) == 0 {
		return nil
	}

	out := make([]signalr.Feature, 0, len(in))
	for i := range in {
		out = append(out, signalr.Feature{Flag: in[i].Flag, Value: in[i].Value, Properties: in[i].Properties})
	}

	return out
}

// toDriverUpstream maps a wire upstream block onto the driver templates.
func toDriverUpstream(in *upstreamWire) []signalr.UpstreamTemplate {
	if in == nil || len(in.Templates) == 0 {
		return nil
	}

	out := make([]signalr.UpstreamTemplate, 0, len(in.Templates))
	for i := range in.Templates {
		out = append(out, signalr.UpstreamTemplate{
			URLTemplate:     in.Templates[i].URLTemplate,
			HubPattern:      in.Templates[i].HubPattern,
			EventPattern:    in.Templates[i].EventPattern,
			CategoryPattern: in.Templates[i].CategoryPattern,
		})
	}

	return out
}
