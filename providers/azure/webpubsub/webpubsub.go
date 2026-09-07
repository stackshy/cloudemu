// Package webpubsub provides an in-memory mock of Azure Web PubSub Service
// (Microsoft.SignalRService/webPubSub) — the ARM control plane only. It manages
// the webPubSub resource lifecycle (create/update/get/delete/list) and the
// listKeys action; the data plane (a running Web PubSub hub, negotiate,
// websocket traffic) is out of scope.
//
// Web PubSub is the sibling of Azure SignalR Service: both live under the
// Microsoft.SignalRService provider and share a near-identical ARM wire shape.
// A webPubSub resource carries a set of computed, service-minted fields that
// MUST stay stable for the lifetime of the resource so infrastructure-as-code
// tools (Terraform's azurerm_web_pubsub) see no drift on re-plan:
//   - hostName: "<name>.webpubsub.azure.com", deterministic from the name.
//     (Note the ".webpubsub.azure.com" suffix — SignalR uses
//     ".service.signalr.net".)
//   - externalIP / publicPort / serverPort: the resource's network coordinates.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - primaryKey / secondaryKey and their connection strings.
//   - identity.principalId / identity.tenantId for a system-assigned identity.
//   - sku.tier / sku.size, derived from the sku name (Standard_S1 -> Standard/S1).
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values — across gets, patches,
// listKeys and a snapshot/restore.
package webpubsub

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
	// providerNamespace is the ARM provider namespace shared with SignalR.
	providerNamespace = "Microsoft.SignalRService"
	// resourceType is the ARM resource type segment.
	resourceType = "webPubSub"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// hostNameSuffix is the fixed tail of every webPubSub hostname; real Azure
	// emits "<name>.webpubsub.azure.com" (SignalR uses ".service.signalr.net").
	hostNameSuffix = "webpubsub.azure.com"
	// defaultPort is the public and server port real Azure reports (443).
	defaultPort = 443
	// defaultVersion is the reported Web PubSub service version.
	defaultVersion = "1.0"
	// publicNetworkAccessEnabled is the default public-network-access value.
	publicNetworkAccessEnabled = "Enabled"
	// defaultKind is the service kind real Azure defaults to when none is sent.
	defaultKind = "WebPubSub"
	// firstOctet anchors the synthetic external IP in an Azure-looking range.
	firstOctet = 20
	// octetMod keeps a synthetic octet in the 1..254 range.
	octetMod = 254
)

// Sku is the pricing tier of a webPubSub resource. Name is required (e.g.
// Free_F1, Standard_S1, Premium_P1, Premium_P2); Tier and Size are derived from
// Name to match what real Azure echoes back. Capacity is the unit count.
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

// Identity is a managed identity attached to a webPubSub resource. Type is one
// of SystemAssigned, UserAssigned or "SystemAssigned,UserAssigned". PrincipalID
// and TenantID are populated only for a system-assigned identity.
type Identity struct {
	Type         string                       `json:"type"`
	PrincipalID  string                       `json:"principalId,omitempty"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// LiveTraceCategory is one live-trace category toggle (ConnectivityLogs,
// MessagingLogs, HttpRequestLogs). Enabled is the Azure "true"/"false" string.
type LiveTraceCategory struct {
	Name    string `json:"name"`
	Enabled string `json:"enabled"`
}

// LiveTrace is the resource's live-trace configuration. Enabled is the master
// "true"/"false" toggle; Categories carries the per-category toggles. Stored
// verbatim so it round-trips without drift.
type LiveTrace struct {
	Enabled    string              `json:"enabled,omitempty"`
	Categories []LiveTraceCategory `json:"categories,omitempty"`
}

// WebPubSub is a stored Microsoft.SignalRService/webPubSub resource.
// Subscription, ResourceGroup and Name preserve the caller's casing; the
// computed fields are minted at create and never regenerated on a read.
type WebPubSub struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`

	PublicNetworkAccess  string     `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth     *bool      `json:"disableLocalAuth,omitempty"`
	DisableAadAuth       *bool      `json:"disableAadAuth,omitempty"`
	TLSClientCertEnabled *bool      `json:"tlsClientCertEnabled,omitempty"`
	LiveTrace            *LiveTrace `json:"liveTrace,omitempty"`

	HostName          string `json:"hostName"`
	ExternalIP        string `json:"externalIP"`
	PublicPort        int    `json:"publicPort"`
	ServerPort        int    `json:"serverPort"`
	Version           string `json:"version"`
	ProvisioningState string `json:"provisioningState"`
	PrimaryKey        string `json:"primaryKey"`
	SecondaryKey      string `json:"secondaryKey"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *WebPubSub) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// PrimaryConnectionString is the connection string built from the primary key.
func (s *WebPubSub) PrimaryConnectionString() string {
	return connectionString(s.HostName, s.PrimaryKey)
}

// SecondaryConnectionString is the connection string built from the secondary key.
func (s *WebPubSub) SecondaryConnectionString() string {
	return connectionString(s.HostName, s.SecondaryKey)
}

// Input carries the mutable fields of a create/update request.
type Input struct {
	Location             string
	Tags                 map[string]string
	Kind                 string
	Sku                  *Sku
	Identity             *Identity
	PublicNetworkAccess  string
	DisableLocalAuth     *bool
	DisableAadAuth       *bool
	TLSClientCertEnabled *bool
	LiveTrace            *LiveTrace
}

// Mock is the in-memory backend for webPubSub resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*WebPubSub]

	// tenantID is the single AAD tenant this estate belongs to; every
	// system-assigned identity reports it. Deterministic, so it survives a
	// restart without being persisted.
	tenantID string
}

// New creates an empty webPubSub mock.
func New(_ *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*WebPubSub](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new webPubSub resource or updates an existing one.
// The computed fields (hostName, externalIP, ports, keys, identity ids) are
// minted once at create and preserved across updates, so they stay stable.
// Location is immutable in real Azure and is preserved on update. It returns the
// stored resource and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (WebPubSub, bool, error) {
	if err := validate(sub, rg, name, in); err != nil {
		return WebPubSub{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	s := WebPubSub{Subscription: sub, ResourceGroup: rg, Name: name, Location: in.Location}
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
func (m *Mock) Get(_ context.Context, sub, rg, name string) (WebPubSub, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return WebPubSub{}, cerrors.Newf(cerrors.NotFound, "webPubSub %q not found", name)
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
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]WebPubSub, error) {
	return m.filter(func(s *WebPubSub) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]WebPubSub, error) {
	return m.filter(func(s *WebPubSub) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverWebPubSub returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverWebPubSub(_ context.Context) ([]WebPubSub, error) {
	return m.filter(func(*WebPubSub) bool { return true }), nil
}

// PurgeResourceGroup deletes every webPubSub resource under sub/rg, so a
// resource-group delete cascades into its webPubSub resources.
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
func (m *Mock) filter(pred func(*WebPubSub) bool) []WebPubSub {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []WebPubSub

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the computed
// fields untouched. Kind defaults to "WebPubSub" and publicNetworkAccess to
// "Enabled" when the client sends none, matching real Azure.
func applyInput(s *WebPubSub, in *Input) {
	s.Tags = maps.Clone(in.Tags)

	s.Kind = in.Kind
	if s.Kind == "" {
		s.Kind = defaultKind
	}

	s.Sku = resolveSku(in.Sku)
	s.DisableLocalAuth = clonePtrBool(in.DisableLocalAuth)
	s.DisableAadAuth = clonePtrBool(in.DisableAadAuth)
	s.TLSClientCertEnabled = clonePtrBool(in.TLSClientCertEnabled)
	s.LiveTrace = cloneLiveTrace(in.LiveTrace)

	s.PublicNetworkAccess = in.PublicNetworkAccess
	if s.PublicNetworkAccess == "" {
		s.PublicNetworkAccess = publicNetworkAccessEnabled
	}
}

// mintComputed fills the stable, service-minted fields once, at create.
func mintComputed(s *WebPubSub, sub, rg, name string) {
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

// connectionString builds the Web PubSub connection string for a host and key.
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
		return cerrors.New(cerrors.InvalidArgument, "webPubSub name is required")
	case in.Location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *WebPubSub) WebPubSub {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Sku = clonePtrSku(s.Sku)
	out.DisableLocalAuth = clonePtrBool(s.DisableLocalAuth)
	out.DisableAadAuth = clonePtrBool(s.DisableAadAuth)
	out.TLSClientCertEnabled = clonePtrBool(s.TLSClientCertEnabled)
	out.LiveTrace = cloneLiveTrace(s.LiveTrace)

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

func cloneLiveTrace(in *LiveTrace) *LiveTrace {
	if in == nil {
		return nil
	}

	return &LiveTrace{Enabled: in.Enabled, Categories: slices.Clone(in.Categories)}
}

func clonePtrBool(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}
