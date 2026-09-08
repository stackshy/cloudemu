package healthcareapis

import (
	"context"
	"maps"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// DicomService is a stored Microsoft.HealthcareApis/workspaces/dicomservices child
// resource. It is keyed under its parent workspace; deleting the workspace
// cascades to it. Its authenticationConfiguration (authority, audiences) and
// serviceUrl are read-only computed fields real Azure mints — they are stamped
// once at create and stay byte-stable across reads.
type DicomService struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	WorkspaceName string            `json:"workspaceName"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	Identity            *Identity `json:"identity,omitempty"`
	Cors                *Cors     `json:"cors,omitempty"`
	PublicNetworkAccess string    `json:"publicNetworkAccess"`

	// Computed, stable fields.
	Etag              string   `json:"etag"`
	ProvisioningState string   `json:"provisioningState"`
	ServiceURL        string   `json:"serviceUrl"`
	Authority         string   `json:"authority"`
	Audiences         []string `json:"audiences"`
}

// ARMID returns the fully-qualified ARM resource id of the DICOM service, nested
// under its parent workspace.
func (d *DicomService) ARMID() string {
	return idgen.AzureID(d.Subscription, d.ResourceGroup, providerNamespace, workspaceType, d.WorkspaceName) +
		"/" + dicomType + "/" + d.Name
}

// DicomInput carries the mutable fields of a DICOM service create/update request.
// A nil pointer/map means "not supplied": the stored value is preserved, so a
// PATCH overlays only what it names.
type DicomInput struct {
	Tags                map[string]string
	Identity            *Identity
	Cors                *Cors
	PublicNetworkAccess *string
}

// CreateOrUpdateDicom creates a new DICOM service or updates an existing one under
// its parent workspace. The parent workspace must exist — otherwise it returns a
// NotFound error (the wire layer maps it to ParentResourceNotFound). The computed
// etag, provisioningState, serviceUrl, authentication defaults and identity ids are
// minted once at create and preserved across updates. It returns the stored
// service and whether it was newly created.
func (m *Mock) CreateOrUpdateDicom(
	_ context.Context, sub, rg, workspace, name, location string, in *DicomInput,
) (DicomService, bool, error) {
	return createChild(m, m.dicom, sub, rg, workspace, dicomType, name,
		func() DicomService { return m.newDicom(sub, rg, workspace, name, location) },
		func(d *DicomService) { m.applyDicomInput(d, in) },
		cloneDicom)
}

// newDicom seeds a fresh DICOM service with its immutable identity, its computed,
// stable fields and its read-only authentication configuration.
func (m *Mock) newDicom(sub, rg, workspace, name, location string) DicomService {
	id := childKey(sub, rg, workspace, dicomType, name)

	return DicomService{
		Subscription:        sub,
		ResourceGroup:       rg,
		WorkspaceName:       workspace,
		Name:                name,
		Location:            location,
		PublicNetworkAccess: publicNetworkEnabled,
		Etag:                idgen.SyntheticGUID("etag/" + id),
		ProvisioningState:   stateSucceeded,
		ServiceURL:          serviceHost(workspace, name, dicomURLSuffix),
		Authority:           authorityHostPrefix + m.tenantID,
		Audiences:           []string{dicomDefaultAudience},
	}
}

// applyDicomInput overlays the mutable request fields onto d, leaving the immutable
// location, computed fields and read-only authentication untouched.
func (m *Mock) applyDicomInput(d *DicomService, in *DicomInput) {
	if in.Tags != nil {
		d.Tags = maps.Clone(in.Tags)
	}

	if in.Identity != nil {
		d.Identity = m.resolveIdentity(in.Identity, childKey(d.Subscription, d.ResourceGroup, d.WorkspaceName, dicomType, d.Name))
	}

	if in.Cors != nil {
		d.Cors = cloneCors(in.Cors)
	}

	if in.PublicNetworkAccess != nil && *in.PublicNetworkAccess != "" {
		d.PublicNetworkAccess = *in.PublicNetworkAccess
	}
}

// GetDicom returns the DICOM service, or a NotFound error.
func (m *Mock) GetDicom(_ context.Context, sub, rg, workspace, name string) (DicomService, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, ok := m.dicom.Get(childKey(sub, rg, workspace, dicomType, name))
	if !ok {
		return DicomService{}, cerrors.Newf(cerrors.NotFound, "healthcareapis dicom service %q not found", name)
	}

	return cloneDicom(d), nil
}

// DeleteDicom removes the DICOM service, reporting whether it existed.
func (m *Mock) DeleteDicom(_ context.Context, sub, rg, workspace, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.dicom.Delete(childKey(sub, rg, workspace, dicomType, name)), nil
}

// ListDicomByWorkspace returns every DICOM service under the workspace, sorted by
// name.
func (m *Mock) ListDicomByWorkspace(_ context.Context, sub, rg, workspace string) ([]DicomService, error) {
	prefix := workspaceKey(sub, rg, workspace) + "/" + dicomType + "/"

	return listChildren(m, m.dicom, prefix, cloneDicom, func(d *DicomService) string { return d.Name }), nil
}

// cloneDicom deep-copies a stored DICOM service so callers never alias the backing
// store.
func cloneDicom(d *DicomService) DicomService {
	out := *d
	out.Tags = maps.Clone(d.Tags)
	out.Identity = cloneIdentity(d.Identity)
	out.Cors = cloneCors(d.Cors)
	out.Audiences = append([]string(nil), d.Audiences...)

	return out
}
