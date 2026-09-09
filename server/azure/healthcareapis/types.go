package healthcareapis

import (
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/providers/azure/healthcareapis"
)

// identityRequest is the writable half of the identity block; the minted ids are
// read-only. userAssignedIdentities is a map of ARM ids to (empty) objects on
// input.
type identityRequest struct {
	Type         string                     `json:"type"`
	UserAssigned map[string]json.RawMessage `json:"userAssignedIdentities,omitempty"`
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

// corsWire is the CORS configuration block, shared by the FHIR and DICOM services.
type corsWire struct {
	Origins          []string `json:"origins,omitempty"`
	Headers          []string `json:"headers,omitempty"`
	Methods          []string `json:"methods,omitempty"`
	MaxAge           *int     `json:"maxAge,omitempty"`
	AllowCredentials *bool    `json:"allowCredentials,omitempty"`
}

// ---- workspace ----

// workspaceRequest is the ARM workspace PUT/PATCH body.
type workspaceRequest struct {
	Location   string                      `json:"location"`
	Tags       map[string]string           `json:"tags,omitempty"`
	Properties *workspacePropertiesRequest `json:"properties,omitempty"`
}

// workspacePropertiesRequest is the writable subset of workspace properties.
type workspacePropertiesRequest struct {
	PublicNetworkAccess *string `json:"publicNetworkAccess,omitempty"`
}

// workspaceResponse is the ARM representation of a healthcareapis workspace.
type workspaceResponse struct {
	ID         string                      `json:"id"`
	Name       string                      `json:"name"`
	Type       string                      `json:"type"`
	Location   string                      `json:"location"`
	Etag       string                      `json:"etag"`
	Tags       map[string]string           `json:"tags,omitempty"`
	Properties workspacePropertiesResponse `json:"properties"`
}

// workspacePropertiesResponse is the workspace properties block.
type workspacePropertiesResponse struct {
	ProvisioningState   string `json:"provisioningState"`
	PublicNetworkAccess string `json:"publicNetworkAccess"`
}

// ---- fhir ----

// fhirRequest is the ARM FHIR service PUT/PATCH body.
type fhirRequest struct {
	Location   string                 `json:"location"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Kind       *string                `json:"kind,omitempty"`
	Identity   *identityRequest       `json:"identity,omitempty"`
	Properties *fhirPropertiesRequest `json:"properties,omitempty"`
}

// fhirPropertiesRequest is the writable subset of FHIR service properties.
type fhirPropertiesRequest struct {
	AuthenticationConfiguration *fhirAuthWire `json:"authenticationConfiguration,omitempty"`
	CorsConfiguration           *corsWire     `json:"corsConfiguration,omitempty"`
	ExportConfiguration         *exportWire   `json:"exportConfiguration,omitempty"`
	AcrConfiguration            *acrWire      `json:"acrConfiguration,omitempty"`
	PublicNetworkAccess         *string       `json:"publicNetworkAccess,omitempty"`
}

// fhirAuthWire is the FHIR authenticationConfiguration block.
type fhirAuthWire struct {
	Authority         string `json:"authority,omitempty"`
	Audience          string `json:"audience,omitempty"`
	SmartProxyEnabled *bool  `json:"smartProxyEnabled,omitempty"`
}

// exportWire is the FHIR exportConfiguration block.
type exportWire struct {
	StorageAccountName string `json:"storageAccountName,omitempty"`
}

// acrWire is the FHIR acrConfiguration block.
type acrWire struct {
	LoginServers []string          `json:"loginServers,omitempty"`
	OciArtifacts []ociArtifactWire `json:"ociArtifacts,omitempty"`
}

// ociArtifactWire is one OCI artifact entry on the wire.
type ociArtifactWire struct {
	LoginServer string `json:"loginServer,omitempty"`
	ImageName   string `json:"imageName,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

// fhirResponse is the ARM representation of a FHIR service.
type fhirResponse struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Location   string                 `json:"location"`
	Etag       string                 `json:"etag"`
	Kind       string                 `json:"kind"`
	Tags       map[string]string      `json:"tags,omitempty"`
	Identity   *identityResponse      `json:"identity,omitempty"`
	Properties fhirPropertiesResponse `json:"properties"`
}

// fhirPropertiesResponse is the FHIR service properties block.
type fhirPropertiesResponse struct {
	ProvisioningState           string        `json:"provisioningState"`
	AuthenticationConfiguration *fhirAuthWire `json:"authenticationConfiguration,omitempty"`
	CorsConfiguration           *corsWire     `json:"corsConfiguration,omitempty"`
	ExportConfiguration         *exportWire   `json:"exportConfiguration,omitempty"`
	AcrConfiguration            *acrWire      `json:"acrConfiguration,omitempty"`
	PublicNetworkAccess         string        `json:"publicNetworkAccess"`
}

// ---- dicom ----

// dicomRequest is the ARM DICOM service PUT/PATCH body.
type dicomRequest struct {
	Location   string                  `json:"location"`
	Tags       map[string]string       `json:"tags,omitempty"`
	Identity   *identityRequest        `json:"identity,omitempty"`
	Properties *dicomPropertiesRequest `json:"properties,omitempty"`
}

// dicomPropertiesRequest is the writable subset of DICOM service properties. The
// authenticationConfiguration is read-only, so it is not accepted on input.
type dicomPropertiesRequest struct {
	CorsConfiguration   *corsWire `json:"corsConfiguration,omitempty"`
	PublicNetworkAccess *string   `json:"publicNetworkAccess,omitempty"`
}

// dicomAuthWire is the read-only DICOM authenticationConfiguration block.
type dicomAuthWire struct {
	Authority string   `json:"authority,omitempty"`
	Audiences []string `json:"audiences,omitempty"`
}

// dicomResponse is the ARM representation of a DICOM service.
type dicomResponse struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Type       string                  `json:"type"`
	Location   string                  `json:"location"`
	Etag       string                  `json:"etag"`
	Tags       map[string]string       `json:"tags,omitempty"`
	Identity   *identityResponse       `json:"identity,omitempty"`
	Properties dicomPropertiesResponse `json:"properties"`
}

// dicomPropertiesResponse is the DICOM service properties block. serviceUrl and
// authenticationConfiguration are read-only computed fields.
type dicomPropertiesResponse struct {
	ProvisioningState           string         `json:"provisioningState"`
	ServiceURL                  string         `json:"serviceUrl"`
	AuthenticationConfiguration *dicomAuthWire `json:"authenticationConfiguration,omitempty"`
	CorsConfiguration           *corsWire      `json:"corsConfiguration,omitempty"`
	PublicNetworkAccess         string         `json:"publicNetworkAccess"`
}

// ---- projections: stored -> wire ----

// toWorkspaceResponse projects a stored workspace onto its ARM wire form.
func toWorkspaceResponse(w *healthcareapis.Workspace) workspaceResponse {
	return workspaceResponse{
		ID:       w.ARMID(),
		Name:     w.Name,
		Type:     workspaceArmType,
		Location: w.Location,
		Etag:     w.Etag,
		Tags:     w.Tags,
		Properties: workspacePropertiesResponse{
			ProvisioningState:   w.ProvisioningState,
			PublicNetworkAccess: w.PublicNetworkAccess,
		},
	}
}

// toFhirResponse projects a stored FHIR service onto its ARM wire form.
func toFhirResponse(f *healthcareapis.FhirService) fhirResponse {
	out := fhirResponse{
		ID:       f.ARMID(),
		Name:     f.Name,
		Type:     fhirArmType,
		Location: f.Location,
		Etag:     f.Etag,
		Kind:     f.Kind,
		Tags:     f.Tags,
		Identity: toIdentityResponse(f.Identity),
		Properties: fhirPropertiesResponse{
			ProvisioningState:           f.ProvisioningState,
			AuthenticationConfiguration: &fhirAuthWire{Authority: f.Authority, Audience: f.Audience, SmartProxyEnabled: f.SmartProxyEnabled},
			CorsConfiguration:           toCorsWire(f.Cors),
			PublicNetworkAccess:         f.PublicNetworkAccess,
		},
	}

	if f.ExportStorageName != "" {
		out.Properties.ExportConfiguration = &exportWire{StorageAccountName: f.ExportStorageName}
	}

	if len(f.AcrLoginServers) > 0 || len(f.AcrOciArtifacts) > 0 {
		out.Properties.AcrConfiguration = &acrWire{
			LoginServers: f.AcrLoginServers,
			OciArtifacts: toOciWire(f.AcrOciArtifacts),
		}
	}

	return out
}

// toDicomResponse projects a stored DICOM service onto its ARM wire form.
func toDicomResponse(d *healthcareapis.DicomService) dicomResponse {
	return dicomResponse{
		ID:       d.ARMID(),
		Name:     d.Name,
		Type:     dicomArmType,
		Location: d.Location,
		Etag:     d.Etag,
		Tags:     d.Tags,
		Identity: toIdentityResponse(d.Identity),
		Properties: dicomPropertiesResponse{
			ProvisioningState:           d.ProvisioningState,
			ServiceURL:                  d.ServiceURL,
			AuthenticationConfiguration: &dicomAuthWire{Authority: d.Authority, Audiences: d.Audiences},
			CorsConfiguration:           toCorsWire(d.Cors),
			PublicNetworkAccess:         d.PublicNetworkAccess,
		},
	}
}

func toIdentityResponse(in *healthcareapis.Identity) *identityResponse {
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

func toCorsWire(in *healthcareapis.Cors) *corsWire {
	if in == nil {
		return nil
	}

	return &corsWire{
		Origins:          in.Origins,
		Headers:          in.Headers,
		Methods:          in.Methods,
		MaxAge:           in.MaxAge,
		AllowCredentials: in.AllowCredentials,
	}
}

func toOciWire(in []healthcareapis.OciArtifact) []ociArtifactWire {
	if len(in) == 0 {
		return nil
	}

	out := make([]ociArtifactWire, 0, len(in))
	for i := range in {
		out = append(out, ociArtifactWire{LoginServer: in[i].LoginServer, ImageName: in[i].ImageName, Digest: in[i].Digest})
	}

	return out
}

// ---- projections: wire -> driver input ----

func workspaceInputFromRequest(req *workspaceRequest) healthcareapis.WorkspaceInput {
	in := healthcareapis.WorkspaceInput{Tags: req.Tags}

	if req.Properties != nil {
		in.PublicNetworkAccess = req.Properties.PublicNetworkAccess
	}

	return in
}

func fhirInputFromRequest(req *fhirRequest) healthcareapis.FhirInput {
	in := healthcareapis.FhirInput{
		Tags:     req.Tags,
		Kind:     req.Kind,
		Identity: toDriverIdentity(req.Identity),
	}

	if req.Properties == nil {
		return in
	}

	p := req.Properties
	in.Cors = toDriverCors(p.CorsConfiguration)
	in.PublicNetworkAccess = p.PublicNetworkAccess

	applyFhirAuthInput(&in, p.AuthenticationConfiguration)

	if p.ExportConfiguration != nil {
		name := p.ExportConfiguration.StorageAccountName
		in.ExportStorageName = &name
	}

	if p.AcrConfiguration != nil {
		in.AcrLoginServers = p.AcrConfiguration.LoginServers
		in.AcrOciArtifacts = toDriverOci(p.AcrConfiguration.OciArtifacts)
	}

	return in
}

// applyFhirAuthInput copies a wire authentication block onto the driver input as
// pointer fields so an absent field falls back to the stored/default value.
func applyFhirAuthInput(in *healthcareapis.FhirInput, auth *fhirAuthWire) {
	if auth == nil {
		return
	}

	if auth.Authority != "" {
		v := auth.Authority
		in.Authority = &v
	}

	if auth.Audience != "" {
		v := auth.Audience
		in.Audience = &v
	}

	in.SmartProxyEnabled = auth.SmartProxyEnabled
}

func dicomInputFromRequest(req *dicomRequest) healthcareapis.DicomInput {
	in := healthcareapis.DicomInput{
		Tags:     req.Tags,
		Identity: toDriverIdentity(req.Identity),
	}

	if req.Properties != nil {
		in.Cors = toDriverCors(req.Properties.CorsConfiguration)
		in.PublicNetworkAccess = req.Properties.PublicNetworkAccess
	}

	return in
}

// toDriverIdentity maps a wire identity request onto the driver identity. Only the
// type and the user-assigned id keys are carried; the mock mints the ids.
func toDriverIdentity(in *identityRequest) *healthcareapis.Identity {
	if in == nil {
		return nil
	}

	out := &healthcareapis.Identity{Type: in.Type}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]healthcareapis.UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = healthcareapis.UserAssignedValue{}
		}
	}

	return out
}

func toDriverCors(in *corsWire) *healthcareapis.Cors {
	if in == nil {
		return nil
	}

	return &healthcareapis.Cors{
		Origins:          in.Origins,
		Headers:          in.Headers,
		Methods:          in.Methods,
		MaxAge:           in.MaxAge,
		AllowCredentials: in.AllowCredentials,
	}
}

func toDriverOci(in []ociArtifactWire) []healthcareapis.OciArtifact {
	if in == nil {
		return nil
	}

	out := make([]healthcareapis.OciArtifact, 0, len(in))
	for i := range in {
		out = append(out, healthcareapis.OciArtifact{LoginServer: in[i].LoginServer, ImageName: in[i].ImageName, Digest: in[i].Digest})
	}

	return out
}
