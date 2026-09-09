package healthcareapis

import (
	"context"
	"maps"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// OciArtifact is one Open Container Initiative artifact entry in a FHIR service's
// acrConfiguration. All three fields round-trip verbatim.
type OciArtifact struct {
	LoginServer string `json:"loginServer,omitempty"`
	ImageName   string `json:"imageName,omitempty"`
	Digest      string `json:"digest,omitempty"`
}

// FhirService is a stored Microsoft.HealthcareApis/workspaces/fhirservices child
// resource. It is keyed under its parent workspace; deleting the workspace
// cascades to it.
type FhirService struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	WorkspaceName string            `json:"workspaceName"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	Kind                string        `json:"kind"`
	Identity            *Identity     `json:"identity,omitempty"`
	Authority           string        `json:"authority"`
	Audience            string        `json:"audience"`
	SmartProxyEnabled   *bool         `json:"smartProxyEnabled,omitempty"`
	Cors                *Cors         `json:"cors,omitempty"`
	ExportStorageName   string        `json:"exportStorageName,omitempty"`
	AcrLoginServers     []string      `json:"acrLoginServers,omitempty"`
	AcrOciArtifacts     []OciArtifact `json:"acrOciArtifacts,omitempty"`
	PublicNetworkAccess string        `json:"publicNetworkAccess"`

	// Computed, stable fields.
	Etag              string `json:"etag"`
	ProvisioningState string `json:"provisioningState"`
}

// ARMID returns the fully-qualified ARM resource id of the FHIR service, nested
// under its parent workspace.
func (f *FhirService) ARMID() string {
	return idgen.AzureID(f.Subscription, f.ResourceGroup, providerNamespace, workspaceType, f.WorkspaceName) +
		"/" + fhirType + "/" + f.Name
}

// FhirInput carries the mutable fields of a FHIR service create/update request.
// A nil pointer/slice means "not supplied": the stored value is preserved, so a
// PATCH overlays only what it names.
type FhirInput struct {
	Tags                map[string]string
	Kind                *string
	Identity            *Identity
	Authority           *string
	Audience            *string
	SmartProxyEnabled   *bool
	Cors                *Cors
	ExportStorageName   *string
	AcrLoginServers     []string
	AcrOciArtifacts     []OciArtifact
	PublicNetworkAccess *string
}

// CreateOrUpdateFhir creates a new FHIR service or updates an existing one under
// its parent workspace. The parent workspace must exist — otherwise it returns a
// NotFound error (the wire layer maps it to ParentResourceNotFound). The computed
// etag, provisioningState, identity ids and authentication defaults are minted
// once at create and preserved across updates. It returns the stored service and
// whether it was newly created.
func (m *Mock) CreateOrUpdateFhir(
	_ context.Context, sub, rg, workspace, name, location string, in *FhirInput,
) (FhirService, bool, error) {
	return createChild(m, m.fhir, sub, rg, workspace, fhirType, name,
		func() FhirService { return m.newFhir(sub, rg, workspace, name, location) },
		func(f *FhirService) { m.applyFhirInput(f, in) },
		cloneFhir)
}

// newFhir seeds a fresh FHIR service with its immutable identity, its ARM defaults
// and its computed, stable fields. The authentication defaults (authority from the
// estate tenant, audience from the deterministic service host) are minted here so
// a read echoes byte-stable values.
func (m *Mock) newFhir(sub, rg, workspace, name, location string) FhirService {
	id := childKey(sub, rg, workspace, fhirType, name)

	return FhirService{
		Subscription:        sub,
		ResourceGroup:       rg,
		WorkspaceName:       workspace,
		Name:                name,
		Location:            location,
		Kind:                kindFhirR4,
		Authority:           authorityHostPrefix + m.tenantID,
		Audience:            serviceHost(workspace, name, fhirURLSuffix),
		PublicNetworkAccess: publicNetworkEnabled,
		Etag:                idgen.SyntheticGUID("etag/" + id),
		ProvisioningState:   stateSucceeded,
	}
}

// applyFhirInput overlays the mutable request fields onto f, leaving the immutable
// location and computed fields untouched. A nil pointer/slice means "not supplied":
// the stored (or default) value is preserved, so a PATCH merges only what it names.
func (m *Mock) applyFhirInput(f *FhirService, in *FhirInput) {
	if in.Tags != nil {
		f.Tags = maps.Clone(in.Tags)
	}

	if in.Kind != nil && *in.Kind != "" {
		f.Kind = *in.Kind
	}

	if in.Identity != nil {
		f.Identity = m.resolveIdentity(in.Identity, childKey(f.Subscription, f.ResourceGroup, f.WorkspaceName, fhirType, f.Name))
	}

	applyFhirAuth(f, in)

	if in.Cors != nil {
		f.Cors = cloneCors(in.Cors)
	}

	if in.PublicNetworkAccess != nil && *in.PublicNetworkAccess != "" {
		f.PublicNetworkAccess = *in.PublicNetworkAccess
	}

	applyFhirAcrExport(f, in)
}

// applyFhirAcrExport overlays the export and ACR configuration blocks, each
// preserved when the request omits it.
func applyFhirAcrExport(f *FhirService, in *FhirInput) {
	if in.ExportStorageName != nil {
		f.ExportStorageName = *in.ExportStorageName
	}

	if in.AcrLoginServers != nil {
		f.AcrLoginServers = append([]string(nil), in.AcrLoginServers...)
	}

	if in.AcrOciArtifacts != nil {
		f.AcrOciArtifacts = append([]OciArtifact(nil), in.AcrOciArtifacts...)
	}
}

// applyFhirAuth overlays a supplied authenticationConfiguration onto f. The stored
// defaults are preserved when a field is absent, so a partial PATCH never wipes the
// minted authority/audience.
func applyFhirAuth(f *FhirService, in *FhirInput) {
	if in.Authority != nil && *in.Authority != "" {
		f.Authority = *in.Authority
	}

	if in.Audience != nil && *in.Audience != "" {
		f.Audience = *in.Audience
	}

	if in.SmartProxyEnabled != nil {
		v := *in.SmartProxyEnabled
		f.SmartProxyEnabled = &v
	}
}

// GetFhir returns the FHIR service, or a NotFound error.
func (m *Mock) GetFhir(_ context.Context, sub, rg, workspace, name string) (FhirService, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	f, ok := m.fhir.Get(childKey(sub, rg, workspace, fhirType, name))
	if !ok {
		return FhirService{}, cerrors.Newf(cerrors.NotFound, "healthcareapis fhir service %q not found", name)
	}

	return cloneFhir(f), nil
}

// DeleteFhir removes the FHIR service, reporting whether it existed.
func (m *Mock) DeleteFhir(_ context.Context, sub, rg, workspace, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.fhir.Delete(childKey(sub, rg, workspace, fhirType, name)), nil
}

// ListFhirByWorkspace returns every FHIR service under the workspace, sorted by
// name.
func (m *Mock) ListFhirByWorkspace(_ context.Context, sub, rg, workspace string) ([]FhirService, error) {
	prefix := workspaceKey(sub, rg, workspace) + "/" + fhirType + "/"

	return listChildren(m, m.fhir, sub, rg, workspace, prefix, cloneFhir, func(f *FhirService) string { return f.Name })
}

// cloneFhir deep-copies a stored FHIR service so callers never alias the backing
// store.
func cloneFhir(f *FhirService) FhirService {
	out := *f
	out.Tags = maps.Clone(f.Tags)
	out.Identity = cloneIdentity(f.Identity)
	out.Cors = cloneCors(f.Cors)
	out.AcrLoginServers = append([]string(nil), f.AcrLoginServers...)
	out.AcrOciArtifacts = append([]OciArtifact(nil), f.AcrOciArtifacts...)

	if f.SmartProxyEnabled != nil {
		v := *f.SmartProxyEnabled
		out.SmartProxyEnabled = &v
	}

	return out
}
