// Package apimanagement provides an in-memory mock of Azure API Management
// (Microsoft.ApiManagement), the ARM control plane only. It manages the
// service lifecycle (create-or-update, get, patch, delete, list-by-group,
// list-by-subscription), the service SKU (name + capacity, bounded per tier),
// availability zones (Premium only), the system/user-assigned managed identity,
// the global name reservation (checkNameAvailability), and soft delete: a
// deleted service is kept for 48 hours under
// locations/{location}/deletedservices/{name}, where it can be read, purged,
// or recovered by a PUT with properties.restore = true.
//
// A service also carries the child resources an infrastructure-as-code tool
// touches around create, refresh and destroy: the sample Echo API and the
// Starter/Unlimited products every non-Consumption service is born with (list,
// get, delete), the service-level policy (get, put, delete), the developer
// portal sign-in/sign-up/delegation settings (get, put) and the tenant access
// information (get, patch, listSecrets). Creating APIs, products, operations,
// subscriptions, backends, named values and loggers is out of scope, as are the
// gateway data plane, backup/restore and network-configuration updates.
//
// The provider owns the whole resource: the properties block it stores and
// returns already holds Azure's defaults for unset writable fields and the
// computed read-only fields (provisioningState, createdAtUtc, platformVersion,
// the endpoint URLs derived from the name and location), so the Go library and
// the HTTP server return the same resource. id/name, createdAtUtc and the
// system-assigned identity's principalId/tenantId are minted once and stay
// stable; the etag changes on every write and a non-wildcard If-Match that no
// longer matches is rejected with FailedPrecondition.
//
// Terraform: the requests terraform-provider-azurerm v4 makes for
// azurerm_api_management create, refresh and destroy (with its default
// recover_soft_deleted and purge_soft_delete_on_destroy features) are served,
// and two refreshes read back identical state. That was verified by replaying
// the provider's request sequence through the official SDK clients
// (server/azure/apimanagement TestSDKTerraformCreateReadDestroy), not by running
// a terraform binary, so an empty plan after apply is not yet proven.
package apimanagement

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.ApiManagement"
	// serviceType is the ARM service resource-type segment.
	serviceType = "service"

	// stateSucceeded is the terminal provisioningState a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"

	// emulatorTenantID is the single Azure AD directory (tenant) that all
	// system-assigned identities in this emulator belong to.
	emulatorTenantID = "11111111-1111-1111-1111-111111111111"

	// hostSuffix is the DNS suffix every API Management endpoint lives under.
	hostSuffix = ".azure-api.net"

	// wildcardETag is the If-Match value that matches any current version.
	wildcardETag = "*"
)

// ManagedIdentity is a service's top-level managed identity. For a
// system-assigned identity the PrincipalID/TenantID are synthesized once (as
// Azure mints them on assignment) and stay stable; UserAssignedIDs holds the
// assigned user-identity resource ids.
type ManagedIdentity struct {
	Type            string   `json:"type"`
	PrincipalID     string   `json:"principalId,omitempty"`
	TenantID        string   `json:"tenantId,omitempty"`
	UserAssignedIDs []string `json:"userAssignedIds,omitempty"`
}

// Service is a stored Microsoft.ApiManagement/service resource. Subscription,
// ResourceGroup and Name preserve the caller's casing. Properties is the full
// properties block as Azure returns it: the caller's writable properties,
// Azure's defaults for the unset ones and the computed read-only fields.
type Service struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Zones         []string          `json:"zones,omitempty"`

	SkuName     string           `json:"skuName"`
	SkuCapacity int32            `json:"skuCapacity"`
	Identity    *ManagedIdentity `json:"identity,omitempty"`

	Properties json.RawMessage `json:"properties,omitempty"`

	// Computed fields. Etag changes on every write; the rest are stable.
	ProvisioningState string    `json:"provisioningState"`
	Etag              string    `json:"etag"`
	CreatedAt         time.Time `json:"createdAt"`
}

// ARMID returns the fully-qualified ARM resource id of the service.
func (s *Service) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, serviceType, s.Name)
}

// Endpoints are the host URLs Azure derives from a service name.
type Endpoints struct {
	Gateway         string // https://<name>.azure-api.net (the proxy)
	Portal          string // legacy publisher portal
	DeveloperPortal string // developer portal
	ManagementAPI   string // direct management REST endpoint
	Scm             string // git configuration (SCM) endpoint
}

// Endpoints returns the service's endpoint URLs.
func (s *Service) Endpoints() Endpoints {
	return Endpoints{
		Gateway:         s.endpoint(""),
		Portal:          s.endpoint(".portal"),
		DeveloperPortal: s.endpoint(".developer"),
		ManagementAPI:   s.endpoint(".management"),
		Scm:             s.endpoint(".scm"),
	}
}

// endpoint renders https://<name><infix>.azure-api.net. Azure host names are
// lowercase whatever casing the caller used for the service name.
func (s *Service) endpoint(infix string) string {
	return "https://" + strings.ToLower(s.Name) + infix + hostSuffix
}

// ServiceInput carries the mutable fields of a service create/update request.
// A nil pointer/map/slice means "not supplied": on a PATCH the stored value is
// preserved, so the request overlays only what it names. IfMatch, when set to
// anything but "*", makes the write conditional on the stored etag.
type ServiceInput struct {
	Tags        map[string]string
	Zones       []string
	SkuName     *string
	SkuCapacity *int32
	Identity    *ManagedIdentity
	Properties  json.RawMessage
	IfMatch     string
}

// Mock is the in-memory backend for API Management services.
type Mock struct {
	mu       sync.RWMutex
	clock    config.Clock
	services *memstore.Store[*Service]
	children *memstore.Store[*Children]
	deleted  *memstore.Store[*DeletedService]
}

// New creates an empty API Management mock. It falls back to the real clock
// when opts (or its clock) is nil so the mock stays usable standalone.
func New(opts *config.Options) *Mock {
	clock := config.Clock(config.RealClock{})
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		clock:    clock,
		services: memstore.New[*Service](),
		children: memstore.New[*Children](),
		deleted:  memstore.New[*DeletedService](),
	}
}

// serviceKey is the case-insensitive store key for a service.
func serviceKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, serviceType, name))
}

// CreateOrUpdateService creates a new service or replaces an existing one (ARM
// PUT semantics: tags, zones, identity and the properties block are replaced
// wholesale). The service name is a global DNS label, so a name already held by
// another live service (in any subscription or group) is ErrNameNotAvailable
// and one held by a soft-deleted service is ErrSoftDeleted, unless the request
// sets properties.restore to recover it. Location is immutable: a replace that
// names another location is ErrLocationMismatch. createdAtUtc is minted once;
// the etag changes on every write. It returns the stored service and whether it
// was newly created.
func (m *Mock) CreateOrUpdateService(
	_ context.Context, sub, rg, name, location string, in *ServiceInput,
) (Service, bool, error) {
	if restoreRequested(in.Properties) {
		return m.restoreService(sub, rg, name, location, in.IfMatch)
	}

	if err := validateCreate(sub, rg, name, location, in); err != nil {
		return Service{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := serviceKey(sub, rg, name)

	existing, existed := m.services.Get(k)
	if err := checkIfMatch(existed, existing, in.IfMatch, name); err != nil {
		return Service{}, false, err
	}

	var s Service

	if existed {
		if normalizeLocation(existing.Location) != normalizeLocation(location) {
			return Service{}, false, coded(ErrLocationMismatch, cerrors.Newf(cerrors.AlreadyExists,
				"the resource %q already exists in location %q; a resource cannot be moved to %q",
				name, existing.Location, location))
		}

		s = *existing
	} else {
		if err := m.nameTakenLocked(name); err != nil {
			return Service{}, false, err
		}

		s = m.newService(sub, rg, name, location)
	}

	s.Tags = maps.Clone(in.Tags)
	s.Zones = append([]string(nil), in.Zones...)
	s.SkuName = canonicalSKU(*in.SkuName)
	s.SkuCapacity = *in.SkuCapacity
	s.Identity = resolveIdentity(in.Identity, sub, rg, name)
	s.Properties = append(json.RawMessage(nil), in.Properties...)
	m.commitLocked(k, &s)

	if !existed {
		m.children.Set(k, seedChildren(&s))
	}

	return cloneService(&s), !existed, nil
}

// commitLocked re-materializes the full properties block, rotates the etag and
// stores the service. The caller holds m.mu.
func (m *Mock) commitLocked(k string, s *Service) {
	s.materializeProperties()
	s.Etag = nextETag(k, s)
	m.services.Set(k, s)
}

// nextETag derives a new etag from the resource key, its creation time and the
// previous etag, so every write yields a different value (If-Match optimistic
// concurrency works) while the sequence stays deterministic and survives a
// snapshot round trip. Seeding with the creation time keeps a re-created
// service from reusing the etags of an earlier one with the same name.
func nextETag(k string, s *Service) string {
	return idgen.SyntheticGUID("apimanagement/etag/" + k + "/" + s.CreatedAt.String() + "/" + s.Etag)
}

// checkIfMatch enforces a conditional write: an If-Match other than "*" must
// equal the stored etag, and a conditional write on a missing resource fails.
func checkIfMatch(existed bool, s *Service, ifMatch, name string) error {
	if ifMatch == "" || ifMatch == wildcardETag {
		return nil
	}

	if !existed || !etagMatches(s.Etag, ifMatch) {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"the If-Match etag %s does not match the current state of API Management service %q", ifMatch, name)
	}

	return nil
}

// etagMatches compares two etags ignoring the weak-validator prefix and quotes,
// which clients add or strip inconsistently.
func etagMatches(stored, given string) bool {
	norm := func(e string) string {
		return strings.Trim(strings.TrimPrefix(strings.TrimSpace(e), "W/"), `"`)
	}

	return norm(stored) == norm(given)
}

// nameTakenLocked reports ErrNameNotAvailable when a live service anywhere
// already holds name, or ErrSoftDeleted when a soft-deleted one does. Service
// names are global DNS labels (<name>.azure-api.net). The caller holds m.mu.
func (m *Mock) nameTakenLocked(name string) error {
	for _, s := range m.services.All() {
		if strings.EqualFold(s.Name, name) {
			return coded(ErrNameNotAvailable, cerrors.Newf(cerrors.AlreadyExists,
				"API Management service name %q is already in use: %s%s is taken", name, strings.ToLower(name), hostSuffix))
		}
	}

	if d := m.deletedByNameLocked(name); d != nil {
		return coded(ErrSoftDeleted, cerrors.Newf(cerrors.AlreadyExists,
			"API Management service %q is soft-deleted in location %q; recover it (properties.restore = true) "+
				"or purge it before reusing the name", name, d.Service.Location))
	}

	return nil
}

// UpdateService applies an ARM PATCH: tags and zones are replaced wholesale
// when supplied, sku/identity are re-resolved only when supplied, and the
// properties block is merged key-by-key onto the stored block. The merged
// result is re-validated, so a PATCH cannot blank the publisher fields or leave
// an invalid SKU/capacity/zones combination. A PATCH on a missing service is a
// NotFound.
func (m *Mock) UpdateService(_ context.Context, sub, rg, name string, in *ServiceInput) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := serviceKey(sub, rg, name)

	existing, ok := m.services.Get(k)
	if !ok {
		return Service{}, notFound(name)
	}

	if err := checkIfMatch(true, existing, in.IfMatch, name); err != nil {
		return Service{}, err
	}

	s := *existing
	applyPatch(&s, in, sub, rg, name)

	if err := validateSKU(s.SkuName, s.SkuCapacity); err != nil {
		return Service{}, err
	}

	if err := validateZones(s.SkuName, s.Zones); err != nil {
		return Service{}, err
	}

	if err := validatePublisher(s.Properties); err != nil {
		return Service{}, err
	}

	m.commitLocked(k, &s)

	return cloneService(&s), nil
}

// applyPatch overlays the supplied PATCH fields onto s. A nil pointer/map/slice
// preserves the stored value.
func applyPatch(s *Service, in *ServiceInput, sub, rg, name string) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.Zones != nil {
		s.Zones = append([]string(nil), in.Zones...)
	}

	if in.SkuName != nil {
		s.SkuName = canonicalSKU(*in.SkuName)
	}

	if in.SkuCapacity != nil {
		s.SkuCapacity = *in.SkuCapacity
	}

	if in.Identity != nil {
		s.Identity = resolveIdentity(in.Identity, sub, rg, name)
	}

	if in.Properties != nil {
		s.Properties = mergeRaw(s.Properties, in.Properties)
	}
}

// newService seeds a fresh service with its immutable identity and its
// computed, stable fields.
func (m *Mock) newService(sub, rg, name, location string) Service {
	return Service{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		ProvisioningState: stateSucceeded,
		CreatedAt:         m.clock.Now().UTC().Truncate(time.Second),
	}
}

// GetService returns the service, or a NotFound error.
func (m *Mock) GetService(_ context.Context, sub, rg, name string) (Service, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.services.Get(serviceKey(sub, rg, name))
	if !ok {
		return Service{}, notFound(name)
	}

	return cloneService(s), nil
}

// DeleteService soft-deletes the service, reporting whether it existed. It is
// DeleteServiceIfMatch with no precondition.
func (m *Mock) DeleteService(ctx context.Context, sub, rg, name string) (bool, error) {
	return m.DeleteServiceIfMatch(ctx, sub, rg, name, "")
}

// DeleteServiceIfMatch soft-deletes the service, as Azure does for every delete
// since API version 2020-06-01-preview: the service leaves the live store and is
// kept, with its child resources, under
// locations/{location}/deletedservices/{name} until it is purged, recovered or
// its 48-hour retention lapses. It reports whether the service existed; a
// non-wildcard ifMatch that does not equal the stored etag is a
// FailedPrecondition.
func (m *Mock) DeleteServiceIfMatch(_ context.Context, sub, rg, name, ifMatch string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := serviceKey(sub, rg, name)

	s, ok := m.services.Get(k)
	if ifMatch != "" && ifMatch != wildcardETag {
		if err := checkIfMatch(ok, s, ifMatch, name); err != nil {
			return false, err
		}
	}

	if !ok {
		return false, nil
	}

	m.softDeleteLocked(k, s)

	return true, nil
}

// ListServicesByResourceGroup returns every service in the group, sorted by
// name.
func (m *Mock) ListServicesByResourceGroup(_ context.Context, sub, rg string) ([]Service, error) {
	return m.filterServices(func(s *Service) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListServicesBySubscription returns every service in the subscription, sorted
// by name.
func (m *Mock) ListServicesBySubscription(_ context.Context, sub string) ([]Service, error) {
	return m.filterServices(func(s *Service) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverServices returns every stored service, for the inventory walk.
func (m *Mock) DiscoverServices(_ context.Context) ([]Service, error) {
	return m.filterServices(func(*Service) bool { return true }), nil
}

// PurgeResourceGroup soft-deletes every service under sub/rg, so a
// resource-group delete cascades into its API Management services exactly as a
// service delete does.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, s := range m.services.All() {
		if strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg) {
			m.softDeleteLocked(k, s)
		}
	}

	return nil
}

// NameAvailability is the checkNameAvailability verdict.
type NameAvailability struct {
	Available bool
	Reason    string // "Valid", "Invalid" or "AlreadyExists"
	Message   string
}

// CheckNameAvailability reports whether name can be used for a new service. The
// name is a global DNS label, so any live or soft-deleted service holding it,
// in any subscription, makes it unavailable.
func (m *Mock) CheckNameAvailability(_ context.Context, name string) NameAvailability {
	if !validName(name) {
		return NameAvailability{Reason: "Invalid", Message: cerrors.Message(validateName(name))}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.nameTakenLocked(name); err != nil {
		return NameAvailability{Reason: "AlreadyExists", Message: cerrors.Message(err)}
	}

	return NameAvailability{Available: true, Reason: "Valid"}
}

// filterServices returns the services matching pred, sorted by name.
func (m *Mock) filterServices(pred func(*Service) bool) []Service {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Service

	for _, s := range m.services.All() {
		if pred(s) {
			out = append(out, cloneService(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// resolveIdentity normalizes an incoming managed identity: for a
// system-assigned identity it synthesizes deterministic principal/tenant GUIDs
// (as Azure does on assignment); a nil or "None" identity resolves to nil.
func resolveIdentity(in *ManagedIdentity, sub, rg, name string) *ManagedIdentity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &ManagedIdentity{
		Type:            in.Type,
		UserAssignedIDs: append([]string(nil), in.UserAssignedIDs...),
	}
	sort.Strings(out.UserAssignedIDs)

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		// Keyed on the full resource id so two services with the same name in
		// different groups stay distinct, while the value is stable across
		// gets/patches/restarts for the same service.
		out.PrincipalID = idgen.SyntheticGUID("apimanagement/principal/" + serviceKey(sub, rg, name))
		out.TenantID = emulatorTenantID
	}

	return out
}

// notFound is the NotFound error for a missing service.
func notFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "API Management service %q not found", name)
}

// cloneService deep-copies a stored service so callers never alias the store.
func cloneService(s *Service) Service {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Zones = append([]string(nil), s.Zones...)
	out.Identity = cloneIdentity(s.Identity)

	if s.Properties != nil {
		out.Properties = append(json.RawMessage(nil), s.Properties...)
	}

	return out
}

// cloneIdentity deep-copies a managed identity, or returns nil.
func cloneIdentity(id *ManagedIdentity) *ManagedIdentity {
	if id == nil {
		return nil
	}

	out := *id
	out.UserAssignedIDs = append([]string(nil), id.UserAssignedIDs...)

	return &out
}

// mergeRaw overlays the top-level keys of patch onto base and returns the
// merged raw JSON object. A malformed base or patch falls back to whichever
// side parses, so a merge never drops the caller's bytes silently.
func mergeRaw(base, patch json.RawMessage) json.RawMessage {
	merged := map[string]json.RawMessage{}
	if len(base) > 0 {
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = map[string]json.RawMessage{}
		}
	}

	overlay := map[string]json.RawMessage{}
	if err := json.Unmarshal(patch, &overlay); err != nil {
		return append(json.RawMessage(nil), patch...)
	}

	maps.Copy(merged, overlay)

	raw, err := json.Marshal(merged)
	if err != nil {
		return append(json.RawMessage(nil), patch...)
	}

	return raw
}
