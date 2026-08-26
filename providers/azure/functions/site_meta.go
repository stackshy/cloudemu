package functions

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"maps"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// keyBytes is the entropy of a generated function/host key before base64.
const keyBytes = 32

// defaultKeyName is the name of the default key Azure provisions for a host and
// for each function.
const defaultKeyName = "default"

// emulatorTenantID is the single Azure AD directory all emulated resources
// report as their tenant, matching the value used by VM/AKS/ACR identity.
const emulatorTenantID = "11111111-1111-1111-1111-111111111111"

// identityTypeNone is the ARM ResourceIdentityType "None" value: no identity
// attached.
const identityTypeNone = "None"

// SiteIdentity is the managed-service-identity attached to a function app
// (Microsoft.Web/sites identity envelope). For a system-assigned identity the
// PrincipalID/TenantID are synthesized on store; for each user-assigned
// identity the per-identity PrincipalID/ClientID are synthesized. On input only
// Type and the UserAssigned keys (the identities to attach) are meaningful.
type SiteIdentity struct {
	Type         string
	PrincipalID  string
	TenantID     string
	UserAssigned map[string]UserAssignedIdentity
}

// UserAssignedIdentity is one entry of SiteIdentity.UserAssigned: the
// synthesized principal/client id pair Azure reports for an attached
// user-assigned identity.
type UserAssignedIdentity struct {
	PrincipalID string
	ClientID    string
}

// clone returns a deep copy safe to hand outside the store lock.
func (i *SiteIdentity) clone() *SiteIdentity {
	out := *i
	out.UserAssigned = maps.Clone(i.UserAssigned)

	return &out
}

// identitySeed derives the deterministic system-assigned principal seed for a
// site from its (resourceGroup, name), so the synthesized principalId is stable
// across GET/List for the same site.
func identitySeed(in *SiteMeta) string {
	return in.ResourceGroup + "/" + in.Name
}

// resolveSiteIdentity normalizes a submitted managed identity: for a
// system-assigned identity it synthesizes a deterministic principal/tenant GUID
// pair, and for each user-assigned identity a deterministic principal/client
// GUID pair, keyed by seed so the same site always reports the same identity
// across GET/List. A nil or "None" identity resolves to nil (no identity).
func resolveSiteIdentity(in *SiteIdentity, seed string) *SiteIdentity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, identityTypeNone) {
		return nil
	}

	out := &SiteIdentity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		out.PrincipalID = idgen.SyntheticGUID("principal/site/" + seed)
		out.TenantID = emulatorTenantID
	}

	if len(in.UserAssigned) > 0 {
		out.UserAssigned = make(map[string]UserAssignedIdentity, len(in.UserAssigned))
		for id := range in.UserAssigned {
			out.UserAssigned[id] = UserAssignedIdentity{
				PrincipalID: idgen.SyntheticGUID("principal/uai/" + id),
				ClientID:    idgen.SyntheticGUID("client/uai/" + id),
			}
		}
	}

	return out
}

// SiteFunction is one function deployed to a function app
// (Microsoft.Web/sites/functions). Azure discovers these from the deployed
// code; here they are created explicitly via the ARM CreateFunction PUT.
type SiteFunction struct {
	Name       string
	Config     map[string]any
	Language   string
	IsDisabled bool
	Keys       map[string]string
}

// SiteMeta holds the Azure-specific state of a function app that the portable
// serverless FunctionInfo does not model: the region it was created in, its
// plan/HTTPS flags, the app settings (with values), and the host/function keys.
// It is stored alongside the portable function so a Microsoft.Web/sites request
// can reconstruct the full ARM site resource.
type SiteMeta struct {
	Name              string
	Subscription      string
	ResourceGroup     string
	Location          string
	Kind              string
	ServerFarmID      string
	HTTPSOnly         bool
	Reserved          bool
	LinuxFxVersion    string
	ProvisioningState string
	// Identity is the resolved managed-service-identity, nil when the site has
	// none attached. It is resolved (synthesized) on upsert.
	Identity         *SiteIdentity
	AppSettings      map[string]string
	MasterKey        string
	HostFunctionKeys map[string]string
	SystemKeys       map[string]string
	Functions        map[string]*SiteFunction
}

// clone returns a deep copy safe to hand outside the store lock.
func (s *SiteMeta) clone() *SiteMeta {
	out := *s
	out.AppSettings = maps.Clone(s.AppSettings)
	out.HostFunctionKeys = maps.Clone(s.HostFunctionKeys)
	out.SystemKeys = maps.Clone(s.SystemKeys)

	if s.Identity != nil {
		out.Identity = s.Identity.clone()
	}

	out.Functions = make(map[string]*SiteFunction, len(s.Functions))

	for name, fn := range s.Functions {
		out.Functions[name] = fn.clone()
	}

	return &out
}

func (f *SiteFunction) clone() *SiteFunction {
	out := *f
	out.Config = maps.Clone(f.Config)
	out.Keys = maps.Clone(f.Keys)

	return &out
}

// UpsertSiteMeta creates the site metadata on first call and updates the mutable
// fields on subsequent calls, preserving generated keys and deployed functions.
//
//nolint:gocritic // in is the by-value request payload, cloned on store.
func (m *Mock) UpsertSiteMeta(_ context.Context, in SiteMeta) (*SiteMeta, error) {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	if existing, ok := m.sites.Get(in.Name); ok {
		existing.Location = in.Location
		existing.Kind = in.Kind
		existing.ServerFarmID = in.ServerFarmID
		existing.HTTPSOnly = in.HTTPSOnly
		existing.Reserved = in.Reserved
		existing.LinuxFxVersion = in.LinuxFxVersion
		existing.AppSettings = maps.Clone(in.AppSettings)

		// A nil in.Identity means the request omitted the identity block, so the
		// already-attached identity is preserved (real ARM PUT semantics).
		if in.Identity != nil {
			existing.Identity = resolveSiteIdentity(in.Identity, identitySeed(&in))
		}

		m.sites.Set(in.Name, existing)

		return existing.clone(), nil
	}

	meta := in.clone()
	meta.Identity = resolveSiteIdentity(in.Identity, identitySeed(&in))
	meta.ProvisioningState = "Succeeded"
	meta.MasterKey = generateKey()
	meta.HostFunctionKeys = map[string]string{defaultKeyName: generateKey()}
	meta.SystemKeys = map[string]string{}

	if meta.Functions == nil {
		meta.Functions = map[string]*SiteFunction{}
	}

	m.sites.Set(in.Name, meta)

	return meta.clone(), nil
}

// GetSiteMeta returns the site metadata, scoped to the given subscription and
// resource group. Azure Web App names are globally unique, so this is a plain
// name lookup guarded by a scope check (not a composite-keyed lookup): a site
// that exists under a different subscription/resource group than requested is
// NotFound here, exactly as real ARM answers a GET against the wrong
// resourceGroups segment.
func (m *Mock) GetSiteMeta(_ context.Context, subscription, resourceGroup, name string) (*SiteMeta, error) {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	meta, ok := m.sites.Get(name)
	if !ok || meta.Subscription != subscription || meta.ResourceGroup != resourceGroup {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", name)
	}

	return meta.clone(), nil
}

// DeleteSiteMeta removes the site metadata, scoped to the given subscription
// and resource group (see GetSiteMeta). A site that exists under a different
// scope is left untouched — a DELETE against the wrong resourceGroups segment
// must not delete another resource group's site. A site that doesn't exist at
// all is also left untouched (ignored) so this can trail a portable
// DeleteFunction without racing.
func (m *Mock) DeleteSiteMeta(_ context.Context, subscription, resourceGroup, name string) error {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(name)
	if !ok || meta.Subscription != subscription || meta.ResourceGroup != resourceGroup {
		return nil
	}

	m.sites.Delete(name)

	return nil
}

// UpdateAppSettings replaces a site's app settings only, preserving every
// other stored field. This is the ARM contract for PUT .../config/appsettings
// ("Replaces the application settings of an app" — not the whole site), and
// is scoped exactly like GetSiteMeta/DeleteSiteMeta.
func (m *Mock) UpdateAppSettings(
	_ context.Context, subscription, resourceGroup, name string, settings map[string]string,
) (*SiteMeta, error) {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(name)
	if !ok || meta.Subscription != subscription || meta.ResourceGroup != resourceGroup {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", name)
	}

	meta.AppSettings = maps.Clone(settings)
	m.sites.Set(name, meta)

	return meta.clone(), nil
}

// GetFunctionScoped returns the function only when it belongs to the given
// subscription and resource group. The underlying portable function record
// (m.funcs, driver.Serverless) is keyed by name alone across every resource
// group — matching real Azure's globally-unique Web App names — so scope is
// enforced here via the site metadata before ever touching that store,
// closing the gap where an ARM GET against the wrong resourceGroups segment
// would otherwise return another resource group's site.
func (m *Mock) GetFunctionScoped(ctx context.Context, subscription, resourceGroup, name string) (*driver.FunctionInfo, error) {
	m.sitesMu.RLock()
	meta, ok := m.sites.Get(name)
	m.sitesMu.RUnlock()

	if !ok || meta.Subscription != subscription || meta.ResourceGroup != resourceGroup {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", name)
	}

	return m.GetFunction(ctx, name)
}

// DeleteFunctionScoped deletes the function and its site metadata only when
// the site belongs to the given subscription and resource group (see
// GetFunctionScoped). The scope check, function delete and site-metadata
// delete run under a single sitesMu hold so a concurrent
// UpsertSiteMeta/DeleteSiteMeta for the same name can't interleave and shift
// which resource group "owns" the name mid-operation.
func (m *Mock) DeleteFunctionScoped(ctx context.Context, subscription, resourceGroup, name string) error {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(name)
	if !ok || meta.Subscription != subscription || meta.ResourceGroup != resourceGroup {
		return cerrors.Newf(cerrors.NotFound, "site %s not found", name)
	}

	if err := m.DeleteFunction(ctx, name); err != nil {
		return err
	}

	m.sites.Delete(name)

	return nil
}

// ListSiteMeta returns the sites in the given resource group, or all sites in
// the subscription when resourceGroup is empty.
func (m *Mock) ListSiteMeta(_ context.Context, subscription, resourceGroup string) ([]SiteMeta, error) {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	all := m.sites.All()
	out := make([]SiteMeta, 0, len(all))

	for _, meta := range all {
		if subscription != "" && meta.Subscription != subscription {
			continue
		}

		if resourceGroup != "" && meta.ResourceGroup != resourceGroup {
			continue
		}

		out = append(out, *meta.clone())
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// CreateSiteFunction adds a function to a site, generating its default key.
func (m *Mock) CreateSiteFunction(_ context.Context, site string, fn SiteFunction) (*SiteFunction, error) {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(site)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", site)
	}

	stored := fn.clone()
	if len(stored.Keys) == 0 {
		stored.Keys = map[string]string{defaultKeyName: generateKey()}
	}

	meta.Functions[fn.Name] = stored
	m.sites.Set(site, meta)

	return stored.clone(), nil
}

// GetSiteFunction returns one function of a site, or NotFound.
func (m *Mock) GetSiteFunction(_ context.Context, site, name string) (*SiteFunction, error) {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	fn, err := m.lookupFunction(site, name)
	if err != nil {
		return nil, err
	}

	return fn.clone(), nil
}

// ListSiteFunctions returns a site's functions sorted by name, or NotFound when
// the site does not exist.
func (m *Mock) ListSiteFunctions(_ context.Context, site string) ([]SiteFunction, error) {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	meta, ok := m.sites.Get(site)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", site)
	}

	out := make([]SiteFunction, 0, len(meta.Functions))
	for _, fn := range meta.Functions {
		out = append(out, *fn.clone())
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteSiteFunction removes a function from a site.
func (m *Mock) DeleteSiteFunction(_ context.Context, site, name string) error {
	m.sitesMu.Lock()
	defer m.sitesMu.Unlock()

	meta, ok := m.sites.Get(site)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "site %s not found", site)
	}

	if _, ok := meta.Functions[name]; !ok {
		return cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	delete(meta.Functions, name)
	m.sites.Set(site, meta)

	return nil
}

// FunctionKeys returns the keys of one function, or NotFound.
func (m *Mock) FunctionKeys(_ context.Context, site, name string) (map[string]string, error) {
	m.sitesMu.RLock()
	defer m.sitesMu.RUnlock()

	fn, err := m.lookupFunction(site, name)
	if err != nil {
		return nil, err
	}

	return maps.Clone(fn.Keys), nil
}

// lookupFunction resolves a function within a site under the caller's lock.
func (m *Mock) lookupFunction(site, name string) (*SiteFunction, error) {
	meta, ok := m.sites.Get(site)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "site %s not found", site)
	}

	fn, ok := meta.Functions[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "function %s not found", name)
	}

	return fn, nil
}

// generateKey returns a URL-safe base64 secret used for host and function keys.
func generateKey() string {
	buf := make([]byte, keyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "cloudemu-key"
	}

	return base64.RawURLEncoding.EncodeToString(buf)
}
