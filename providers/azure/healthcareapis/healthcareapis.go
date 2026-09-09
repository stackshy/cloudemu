// Package healthcareapis provides an in-memory mock of Azure Health Data Services
// (Microsoft.HealthcareApis) — the ARM control plane only. It manages the
// workspaces lifecycle plus the nested workspaces/{workspace}/fhirservices and
// workspaces/{workspace}/dicomservices child resources (create/update/get/delete/
// list). The FHIR/DICOM data planes (the running FHIR/DICOM REST endpoints), IoT
// connectors, private endpoints and customer-managed keys are out of scope.
//
// Every child service carries computed, service-minted fields that MUST stay
// stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_healthcare_workspace / azurerm_healthcare_fhir_service /
// azurerm_healthcare_dicom_service) see no drift on re-plan:
//   - workspace provisioningState ("Succeeded") and publicNetworkAccess.
//   - child etag, minted once at create and stable across every get/patch.
//   - child identity principalId / tenantId — minted once for a system-assigned
//     identity and stable (top-level identity ids that an echo cannot reach, so
//     they are modeled deterministically like the databricks precedent).
//   - fhir authenticationConfiguration authority / audience — the audience default
//     is the deterministic service host
//     ("https://<workspace>-<name>.fhir.azurehealthcareapis.com"), minted once and
//     byte-stable across reads (real ARM FhirService exposes no serviceUrl field;
//     the service host surfaces through this audience default).
//   - dicom serviceUrl ("https://<workspace>-<name>.dicom.azurehealthcareapis.com")
//     and its read-only authenticationConfiguration authority / audiences.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches and
// a snapshot/restore. No clock or randomness is read on a get.
package healthcareapis

import (
	"context"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.HealthcareApis"
	// workspaceType is the ARM workspace resource type segment.
	workspaceType = "workspaces"
	// fhirType is the ARM fhirservices child resource type segment.
	fhirType = "fhirservices"
	// dicomType is the ARM dicomservices child resource type segment.
	dicomType = "dicomservices"

	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// publicNetworkEnabled is the default publicNetworkAccess value real Azure
	// stamps on a fresh workspace or service.
	publicNetworkEnabled = "Enabled"

	// authorityHostPrefix is the fixed head of the default AAD authority a child
	// service reports; the estate tenant id is appended.
	authorityHostPrefix = "https://login.microsoftonline.com/"
	// fhirURLSuffix is the fixed tail of a FHIR service host.
	fhirURLSuffix = "fhir.azurehealthcareapis.com"
	// dicomURLSuffix is the fixed tail of a DICOM service host.
	dicomURLSuffix = "dicom.azurehealthcareapis.com"
	// kindFhirR4 is the default FHIR service kind.
	kindFhirR4 = "fhir-R4"
	// dicomDefaultAudience is the read-only audience real Azure mints for a DICOM
	// service.
	dicomDefaultAudience = "https://dicom.azurehealthcareapis.azure.com"
)

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a FHIR or DICOM service. Type is one
// of None, SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned".
// PrincipalID and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Cors is the CORS configuration of a FHIR or DICOM service. It round-trips
// verbatim; the pointer fields distinguish an explicit 0/false from an absent
// value.
type Cors struct {
	Origins          []string `json:"origins,omitempty"`
	Headers          []string `json:"headers,omitempty"`
	Methods          []string `json:"methods,omitempty"`
	MaxAge           *int     `json:"maxAge,omitempty"`
	AllowCredentials *bool    `json:"allowCredentials,omitempty"`
}

// Workspace is a stored Microsoft.HealthcareApis/workspaces resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type Workspace struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`

	PublicNetworkAccess string `json:"publicNetworkAccess"`

	// Computed, stable fields.
	Etag              string `json:"etag"`
	ProvisioningState string `json:"provisioningState"`
}

// ARMID returns the fully-qualified ARM resource id of the workspace.
func (w *Workspace) ARMID() string {
	return idgen.AzureID(w.Subscription, w.ResourceGroup, providerNamespace, workspaceType, w.Name)
}

// WorkspaceInput carries the mutable fields of a workspace create/update request.
// A nil pointer/map means "not supplied": the stored value is preserved, so a
// PATCH overlays only what it names.
type WorkspaceInput struct {
	Tags                map[string]string
	PublicNetworkAccess *string
}

// Mock is the in-memory backend for healthcareapis workspaces and their nested
// FHIR and DICOM services.
type Mock struct {
	mu         sync.RWMutex
	workspaces *memstore.Store[*Workspace]
	fhir       *memstore.Store[*FhirService]
	dicom      *memstore.Store[*DicomService]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity and default authority reports it. Deterministic,
	// so it survives a restart without being persisted.
	tenantID string
}

// New creates an empty healthcareapis mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		workspaces: memstore.New[*Workspace](),
		fhir:       memstore.New[*FhirService](),
		dicom:      memstore.New[*DicomService](),
		tenantID:   idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// workspaceKey is the case-insensitive store key for a workspace.
func workspaceKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, workspaceType, name))
}

// childKey is the case-insensitive store key for a child service under a
// workspace, keyed by its child type segment (fhirservices / dicomservices).
func childKey(sub, rg, workspace, childType, name string) string {
	return workspaceKey(sub, rg, workspace) + "/" + childType + "/" + strings.ToLower(name)
}

// CreateOrUpdateWorkspace creates a new workspace or updates an existing one. The
// computed fields (provisioningState, etag) are minted once at create and
// preserved across updates. Location is immutable in real Azure and is preserved
// on update. It returns the stored workspace and whether it was newly created.
func (m *Mock) CreateOrUpdateWorkspace(
	_ context.Context, sub, rg, name, location string, in *WorkspaceInput,
) (Workspace, bool, error) {
	if err := validateWorkspace(sub, rg, name, location); err != nil {
		return Workspace{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := workspaceKey(sub, rg, name)

	existing, existed := m.workspaces.Get(k)
	created := !existed

	var w Workspace
	if existed {
		w = *existing
	} else {
		w = newWorkspace(sub, rg, name, location)
	}

	applyWorkspaceInput(&w, in)
	m.workspaces.Set(k, &w)

	return cloneWorkspace(&w), created, nil
}

// newWorkspace seeds a fresh workspace with its immutable identity and its
// computed, stable fields.
func newWorkspace(sub, rg, name, location string) Workspace {
	return Workspace{
		Subscription:        sub,
		ResourceGroup:       rg,
		Name:                name,
		Location:            location,
		PublicNetworkAccess: publicNetworkEnabled,
		Etag:                idgen.SyntheticGUID("etag/" + workspaceKey(sub, rg, name)),
		ProvisioningState:   stateSucceeded,
	}
}

// applyWorkspaceInput overlays the mutable request fields onto w, leaving the
// immutable location and computed fields untouched.
func applyWorkspaceInput(w *Workspace, in *WorkspaceInput) {
	if in.Tags != nil {
		w.Tags = maps.Clone(in.Tags)
	}

	if in.PublicNetworkAccess != nil && *in.PublicNetworkAccess != "" {
		w.PublicNetworkAccess = *in.PublicNetworkAccess
	}

	if w.PublicNetworkAccess == "" {
		w.PublicNetworkAccess = publicNetworkEnabled
	}
}

// GetWorkspace returns the workspace, or a NotFound error.
func (m *Mock) GetWorkspace(_ context.Context, sub, rg, name string) (Workspace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	w, ok := m.workspaces.Get(workspaceKey(sub, rg, name))
	if !ok {
		return Workspace{}, cerrors.Newf(cerrors.NotFound, "healthcareapis workspace %q not found", name)
	}

	return cloneWorkspace(w), nil
}

// DeleteWorkspace removes the workspace and cascades to every FHIR and DICOM
// service under it, reporting whether the workspace existed.
func (m *Mock) DeleteWorkspace(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existed := m.workspaces.Delete(workspaceKey(sub, rg, name))

	m.deleteChildrenUnder(sub, rg, name)

	return existed, nil
}

// deleteChildrenUnder removes every FHIR and DICOM service nested under the named
// workspace. The caller holds the write lock.
func (m *Mock) deleteChildrenUnder(sub, rg, workspace string) {
	fhirPrefix := workspaceKey(sub, rg, workspace) + "/" + fhirType + "/"
	for k := range m.fhir.All() {
		if strings.HasPrefix(k, fhirPrefix) {
			m.fhir.Delete(k)
		}
	}

	dicomPrefix := workspaceKey(sub, rg, workspace) + "/" + dicomType + "/"
	for k := range m.dicom.All() {
		if strings.HasPrefix(k, dicomPrefix) {
			m.dicom.Delete(k)
		}
	}
}

// ListWorkspacesByResourceGroup returns every workspace in the group, sorted by
// name.
func (m *Mock) ListWorkspacesByResourceGroup(_ context.Context, sub, rg string) ([]Workspace, error) {
	return m.filterWorkspaces(func(w *Workspace) bool {
		return strings.EqualFold(w.Subscription, sub) && strings.EqualFold(w.ResourceGroup, rg)
	}), nil
}

// ListWorkspacesBySubscription returns every workspace in the subscription, sorted
// by name.
func (m *Mock) ListWorkspacesBySubscription(_ context.Context, sub string) ([]Workspace, error) {
	return m.filterWorkspaces(func(w *Workspace) bool {
		return strings.EqualFold(w.Subscription, sub)
	}), nil
}

// DiscoverWorkspaces returns every stored workspace, for the inventory walk.
func (m *Mock) DiscoverWorkspaces(_ context.Context) ([]Workspace, error) {
	return m.filterWorkspaces(func(*Workspace) bool { return true }), nil
}

// PurgeResourceGroup deletes every workspace and child service under sub/rg, so a
// resource-group delete cascades into its healthcareapis resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, w := range m.workspaces.All() {
		if strings.EqualFold(w.Subscription, sub) && strings.EqualFold(w.ResourceGroup, rg) {
			m.workspaces.Delete(k)
		}
	}

	for k, f := range m.fhir.All() {
		if strings.EqualFold(f.Subscription, sub) && strings.EqualFold(f.ResourceGroup, rg) {
			m.fhir.Delete(k)
		}
	}

	for k, d := range m.dicom.All() {
		if strings.EqualFold(d.Subscription, sub) && strings.EqualFold(d.ResourceGroup, rg) {
			m.dicom.Delete(k)
		}
	}

	return nil
}

// filterWorkspaces returns the workspaces matching pred, sorted by name.
func (m *Mock) filterWorkspaces(pred func(*Workspace) bool) []Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Workspace

	for _, w := range m.workspaces.All() {
		if pred(w) {
			out = append(out, cloneWorkspace(w))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// createChild is the shared create-or-update body for a child service under a
// workspace: it validates the identity, verifies the parent workspace exists,
// seeds or loads the child by its type-scoped key, applies the caller's input and
// stores it. It takes the write lock and returns a deep copy of the stored value
// and whether it was newly created.
func createChild[T any](
	m *Mock, store *memstore.Store[*T], sub, rg, workspace, childType, name string,
	seed func() T, apply func(*T), clone func(*T) T,
) (stored T, created bool, err error) {
	if verr := validateChild(sub, rg, workspace, name); verr != nil {
		return stored, false, verr
	}

	key := childKey(sub, rg, workspace, childType, name)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.workspaces.Get(workspaceKey(sub, rg, workspace)); !ok {
		return stored, false, cerrors.Newf(cerrors.NotFound, "healthcareapis workspace %q not found", workspace)
	}

	existing, existed := store.Get(key)
	if existed {
		stored = *existing
	} else {
		stored = seed()
	}

	apply(&stored)
	store.Set(key, &stored)

	return clone(&stored), !existed, nil
}

// listChildren returns every stored child whose key carries prefix, deep-copied via
// clone and sorted by the name accessor. The parent workspace must exist —
// otherwise it returns a NotFound error (the wire layer maps it to
// ParentResourceNotFound), mirroring real ARM, which 404s a list under a
// nonexistent parent rather than returning an empty set. It takes the read lock.
func listChildren[T any](
	m *Mock, store *memstore.Store[*T], sub, rg, workspace, prefix string, clone func(*T) T, name func(*T) string,
) ([]T, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.workspaces.Get(workspaceKey(sub, rg, workspace)); !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "healthcareapis workspace %q not found", workspace)
	}

	var out []T

	for k, v := range store.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, clone(v))
		}
	}

	sort.Slice(out, func(i, j int) bool { return name(&out[i]) < name(&out[j]) })

	return out, nil
}

// resolveIdentity normalizes an incoming managed identity, synthesizing the
// deterministic ids Azure mints on assignment. A nil or "None" identity resolves
// to nil. The seed folds the resource id so two services never share ids.
func (m *Mock) resolveIdentity(in *Identity, resourceID string) *Identity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &Identity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		out.PrincipalID = idgen.SyntheticGUID("principal/" + resourceID)
		out.TenantID = m.tenantID
	}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]UserAssignedValue, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = UserAssignedValue{
				PrincipalID: idgen.SyntheticGUID("ua-principal/" + strings.ToLower(id)),
				ClientID:    idgen.SyntheticGUID("ua-client/" + strings.ToLower(id)),
			}
		}
	}

	return out
}

// serviceHost derives the stable service host from the workspace, service name and
// suffix, matching the "https://<workspace>-<name>.<suffix>" form real Azure emits.
func serviceHost(workspace, name, suffix string) string {
	return "https://" + strings.ToLower(workspace) + "-" + strings.ToLower(name) + "." + suffix
}

// validateWorkspace rejects a workspace create/update with missing required fields.
func validateWorkspace(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "workspace name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// validateChild rejects a child service create/update with missing required
// fields.
func validateChild(sub, rg, workspace, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case workspace == "":
		return cerrors.New(cerrors.InvalidArgument, "workspace name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "service name is required")
	default:
		return nil
	}
}

// cloneWorkspace deep-copies a stored workspace so callers never alias the backing
// store.
func cloneWorkspace(w *Workspace) Workspace {
	out := *w
	out.Tags = maps.Clone(w.Tags)

	return out
}

// cloneIdentity deep-copies an identity block so callers never alias the backing
// store.
func cloneIdentity(in *Identity) *Identity {
	if in == nil {
		return nil
	}

	out := *in
	out.UserAssigned = maps.Clone(in.UserAssigned)

	return &out
}

// cloneCors deep-copies a CORS block so callers never alias the backing store.
func cloneCors(in *Cors) *Cors {
	if in == nil {
		return nil
	}

	out := &Cors{
		Origins: append([]string(nil), in.Origins...),
		Headers: append([]string(nil), in.Headers...),
		Methods: append([]string(nil), in.Methods...),
	}

	if in.MaxAge != nil {
		v := *in.MaxAge
		out.MaxAge = &v
	}

	if in.AllowCredentials != nil {
		v := *in.AllowCredentials
		out.AllowCredentials = &v
	}

	return out
}
