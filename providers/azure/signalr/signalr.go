// Package signalr provides an in-memory mock of Azure SignalR Service
// (Microsoft.SignalRService/signalR) — the ARM control plane only. It manages
// the signalR resource lifecycle (create/update/get/delete/list) and the
// listKeys action; the data plane (a running SignalR hub, negotiate, websocket
// traffic) is out of scope.
//
// A signalR resource carries a set of computed, service-minted fields that MUST
// stay stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_signalr_service) see no drift on re-plan:
//   - hostName: "<name>.service.signalr.net", deterministic from the name.
//   - externalIP / publicPort / serverPort: the resource's network coordinates.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - primaryKey / secondaryKey and their connection strings.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//   - sku.tier / sku.size, derived from the sku name (Standard_S1 -> Standard/S1).
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listKeys and a snapshot/restore.
package signalr

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// providerNamespace is the ARM provider namespace.
	providerNamespace = "Microsoft.SignalRService"
	// resourceType is the ARM resource type segment.
	resourceType = "signalR"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// hostNameSuffix is the fixed tail of every signalR hostname; real Azure
	// emits "<name>.service.signalr.net".
	hostNameSuffix = "service.signalr.net"
	// defaultPort is the public and server port real Azure reports (443).
	defaultPort = 443
	// defaultServerlessTimeout is the default serverless connection timeout in
	// seconds real Azure applies when the client sends none.
	defaultServerlessTimeout = 30
	// defaultVersion is the reported SignalR service version.
	defaultVersion = "1.0"
	// publicNetworkAccessEnabled is the default public-network-access value.
	publicNetworkAccessEnabled = "Enabled"
	// firstOctet anchors the synthetic external IP in an Azure-looking range.
	firstOctet = 20
	// octetMod keeps a synthetic octet in the 1..254 range.
	octetMod = 254
)

// Sku is the pricing tier of a signalR resource. Name is required (e.g.
// Free_F1, Standard_S1, Premium_P1); Tier and Size are derived from Name to
// match what real Azure echoes back. Capacity is the unit count.
type Sku struct {
	Name     string `json:"name"`
	Tier     string `json:"tier,omitempty"`
	Size     string `json:"size,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to a signalR resource. Type is one of
// SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID and
// TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// Cors is the cross-origin configuration.
type Cors struct {
	AllowedOrigins []string `json:"allowedOrigins,omitempty"`
}

// Feature is one SignalR feature flag (ServiceMode, EnableConnectivityLogs,
// EnableMessagingLogs, EnableLiveTrace, …). Stored verbatim so it round-trips.
type Feature struct {
	Flag       string            `json:"flag"`
	Value      string            `json:"value"`
	Properties map[string]string `json:"properties,omitempty"`
}

// UpstreamTemplate is one serverless upstream routing template.
type UpstreamTemplate struct {
	URLTemplate     string `json:"urlTemplate"`
	HubPattern      string `json:"hubPattern,omitempty"`
	EventPattern    string `json:"eventPattern,omitempty"`
	CategoryPattern string `json:"categoryPattern,omitempty"`
}

// SignalR is a stored Microsoft.SignalRService/signalR resource. Subscription,
// ResourceGroup and Name preserve the caller's casing; the computed fields are
// minted at create and never regenerated on a read.
type SignalR struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	Cors                 *Cors              `json:"cors,omitempty"`
	Features             []Feature          `json:"features,omitempty"`
	Upstream             []UpstreamTemplate `json:"upstream,omitempty"`
	PublicNetworkAccess  string             `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth     *bool              `json:"disableLocalAuth,omitempty"`
	DisableAadAuth       *bool              `json:"disableAadAuth,omitempty"`
	TLSClientCertEnabled *bool              `json:"tlsClientCertEnabled,omitempty"`
	ServerlessTimeout    int                `json:"serverlessTimeout,omitempty"`

	HostName          string `json:"hostName"`
	ExternalIP        string `json:"externalIP"`
	PublicPort        int    `json:"publicPort"`
	ServerPort        int    `json:"serverPort"`
	Version           string `json:"version"`
	ProvisioningState string `json:"provisioningState"`
	PrimaryKey        string `json:"primaryKey"`
	SecondaryKey      string `json:"secondaryKey"`
	CreatedAt         string `json:"createdAt,omitempty"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *SignalR) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// PrimaryConnectionString is the connection string built from the primary key.
func (s *SignalR) PrimaryConnectionString() string {
	return connectionString(s.HostName, s.PrimaryKey)
}

// SecondaryConnectionString is the connection string built from the secondary key.
func (s *SignalR) SecondaryConnectionString() string {
	return connectionString(s.HostName, s.SecondaryKey)
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location             string
	Tags                 map[string]string
	Kind                 string
	Sku                  *Sku
	Identity             *Identity
	Cors                 *Cors
	Features             []Feature
	Upstream             []UpstreamTemplate
	PublicNetworkAccess  string
	DisableLocalAuth     *bool
	DisableAadAuth       *bool
	TLSClientCertEnabled *bool
	ServerlessTimeout    *int
}

// Mock is the in-memory backend for signalR resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*SignalR]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty signalR mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*SignalR](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new signalR resource or updates an existing one. The
// computed fields (hostName, externalIP, ports, keys, identity ids) are minted
// once at create and preserved across updates, so they stay stable. Location is
// immutable in real Azure and is preserved on update. It returns the stored
// resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (SignalR, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return SignalR{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	s := SignalR{Subscription: sub, ResourceGroup: rg, Name: name, Location: in.Location}
	if existed {
		s = *existing
	} else {
		mintComputed(&s, sub, rg, name)
	}

	applyInput(&s, in)
	s.Identity = m.resolveIdentity(in.Identity, sub, rg, name)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (SignalR, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return SignalR{}, cerrors.Newf(cerrors.NotFound, "signalR %q not found", name)
	}

	return clone(s), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every resource in the group, sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]SignalR, error) {
	return m.filter(func(s *SignalR) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]SignalR, error) {
	return m.filter(func(s *SignalR) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverSignalR returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverSignalR(_ context.Context) ([]SignalR, error) {
	return m.filter(func(*SignalR) bool { return true }), nil
}

// PurgeResourceGroup deletes every signalR resource under sub/rg, so a
// resource-group delete cascades into its signalR resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, s := range m.store.All() {
		if strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the resources matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(*SignalR) bool) []SignalR {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []SignalR

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// fields untouched. ServerlessTimeout defaults to 30 when the client sends none.
func applyInput(s *SignalR, in *Input) {
	s.Tags = maps.Clone(in.Tags)
	s.Kind = in.Kind
	s.Sku = resolveSku(in.Sku)
	s.Cors = cloneCors(in.Cors)
	s.Features = slices.Clone(in.Features)
	s.Upstream = slices.Clone(in.Upstream)
	s.DisableLocalAuth = clonePtrBool(in.DisableLocalAuth)
	s.DisableAadAuth = clonePtrBool(in.DisableAadAuth)
	s.TLSClientCertEnabled = clonePtrBool(in.TLSClientCertEnabled)

	s.PublicNetworkAccess = in.PublicNetworkAccess
	if s.PublicNetworkAccess == "" {
		s.PublicNetworkAccess = publicNetworkAccessEnabled
	}

	s.ServerlessTimeout = defaultServerlessTimeout
	if in.ServerlessTimeout != nil {
		s.ServerlessTimeout = *in.ServerlessTimeout
	}
}

// mintComputed fills the stable, service-minted fields once, at create.
func mintComputed(s *SignalR, sub, rg, name string) {
	id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
	s.HostName = strings.ToLower(name) + "." + hostNameSuffix
	s.ExternalIP = syntheticIP(id)
	s.PublicPort = defaultPort
	s.ServerPort = defaultPort
	s.Version = defaultVersion
	s.ProvisioningState = stateSucceeded
	s.PrimaryKey = mintKey("primary/" + id)
	s.SecondaryKey = mintKey("secondary/" + id)
}

// resolveSku derives tier and size from the sku name so a read echoes the same
// fields real Azure computes. A nil sku resolves to nil.
func resolveSku(in *Sku) *Sku {
	if in == nil || in.Name == "" {
		return nil
	}

	out := &Sku{Name: in.Name, Tier: in.Tier, Size: in.Size, Capacity: in.Capacity}
	if out.Capacity == 0 {
		out.Capacity = 1
	}

	if out.Tier == "" {
		out.Tier = tierFromName(in.Name)
	}

	if out.Size == "" {
		out.Size = sizeFromName(in.Name)
	}

	return out
}

// tierFromName maps a sku name onto its Azure tier (Standard_S1 -> Standard).
func tierFromName(name string) string {
	switch {
	case strings.HasPrefix(name, "Free"):
		return "Free"
	case strings.HasPrefix(name, "Premium"):
		return "Premium"
	default:
		return "Standard"
	}
}

// sizeFromName returns the sku name's size suffix (Standard_S1 -> S1).
func sizeFromName(name string) string {
	if i := strings.IndexByte(name, '_'); i >= 0 && i+1 < len(name) {
		return name[i+1:]
	}

	return name
}

// resolveIdentity normalizes an incoming managed identity, synthesizing the
// deterministic ids Azure mints on assignment. A nil or "None" identity resolves
// to nil.
func (m *Mock) resolveIdentity(in *Identity, sub, rg, name string) *Identity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &Identity{Type: in.Type}

	if strings.Contains(strings.ToLower(in.Type), "systemassigned") {
		id := strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
		out.PrincipalID = idgen.SyntheticGUID("principal/" + id)
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

// connectionString builds the SignalR connection string for a host and key.
func connectionString(host, accessKey string) string {
	return "Endpoint=https://" + host + ";AccessKey=" + accessKey + ";Version=" + defaultVersion + ";"
}

// mintKey derives a stable 64-hex-character access key from a seed.
func mintKey(seed string) string {
	a := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "")
	b := strings.ReplaceAll(idgen.SyntheticGUID(seed+"#2"), "-", "")

	return a + b
}

// syntheticIP derives a stable public-looking IPv4 from a resource id.
func syntheticIP(id string) string {
	g := strings.ReplaceAll(idgen.SyntheticGUID("ip/"+id), "-", "")

	return strconv.Itoa(firstOctet) + "." +
		octet(g[0:2]) + "." + octet(g[2:4]) + "." + octet(g[4:6])
}

// octet parses a hex byte pair into a decimal string in the 1..254 range.
func octet(hexPair string) string {
	v, err := strconv.ParseUint(hexPair, 16, 16)
	if err != nil {
		return "1"
	}

	return strconv.FormatUint(v%octetMod+1, 10)
}

// validate rejects a create/update with missing required fields.
func validate(sub, rg, name string, in *Input) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "signalR name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *SignalR) SignalR {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Sku = clonePtrSku(s.Sku)
	out.Cors = cloneCors(s.Cors)
	out.Features = slices.Clone(s.Features)
	out.Upstream = slices.Clone(s.Upstream)
	out.DisableLocalAuth = clonePtrBool(s.DisableLocalAuth)
	out.DisableAadAuth = clonePtrBool(s.DisableAadAuth)
	out.TLSClientCertEnabled = clonePtrBool(s.TLSClientCertEnabled)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

func clonePtrSku(in *Sku) *Sku {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}

func cloneCors(in *Cors) *Cors {
	if in == nil {
		return nil
	}

	return &Cors{AllowedOrigins: slices.Clone(in.AllowedOrigins)}
}

func clonePtrBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}
