// Package iothub provides an in-memory mock of Azure IoT Hub
// (Microsoft.Devices/IotHubs) — the ARM control plane only. It manages the hub
// lifecycle (create-or-update, get, patch, delete, list-by-group,
// list-by-subscription), the shared-access-policy keys retrieved via the
// listkeys / getKeysForKeyName actions, and the nested event-hub consumer
// groups (.../eventHubEndpoints/events/ConsumerGroups/{name}).
//
// The IoT Hub data plane — the device registry, device twins, telemetry
// ingestion and cloud-to-device messaging — is out of scope; this surface is
// the management-plane resource provider only. No devices are registered and no
// messages are routed; a hub is a stored resource, not a live broker.
//
// Every service-minted field stays stable for the lifetime of the resource so
// infrastructure-as-code tools (Terraform's azurerm_iothub,
// azurerm_iothub_consumer_group and azurerm_iothub_shared_access_policy) see no
// drift on re-plan:
//   - hostName (<name>.azure-devices.net), provisioningState ("Succeeded"),
//     state ("Active"), etag and the built-in event-hub endpoint (endpoint,
//     path, partitionIds), minted once at create and byte-stable across reads.
//   - the shared-access-policy primaryKey / secondaryKey pairs, minted once at
//     create, stored, and byte-stable across every listkeys call. They are
//     never echoed on the plain hub GET — only the listkeys actions surface
//     them, matching real IoT Hub behavior.
//   - a consumer group's etag, minted once at create.
//
// The routing block is stored as raw JSON and round-trips verbatim, so a caller
// reads back exactly the endpoints / routes / fallbackRoute it sent.
package iothub

import (
	"context"
	"encoding/json"
	"maps"
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
	// ProviderNamespace is the ARM provider namespace.
	ProviderNamespace = "Microsoft.Devices"
	// HubType is the ARM IoT Hub resource type segment.
	HubType = "IotHubs"

	// stateSucceeded is the terminal provisioningState a synchronous ARM PUT
	// settles to immediately (real Azure runs this as an LRO).
	stateSucceeded = "Succeeded"
	// stateActive is the hub runtime state a provisioned hub carries.
	stateActive = "Active"
	// featuresNone is the default capabilities value real Azure assigns.
	featuresNone = "None"
	// hostSuffix is the DNS suffix appended to a hub name to form its hostName.
	hostSuffix = ".azure-devices.net"

	// defaultSkuName / defaultTier / defaultCapacity seed a hub whose create body
	// omitted the sku (real Azure requires it; the mock is lenient).
	defaultSkuName  = "S1"
	defaultCapacity = 1

	// defaultPartitionCount / defaultRetentionDays seed the built-in event-hub
	// endpoint when the create body does not specify them; they match Terraform's
	// azurerm_iothub defaults.
	defaultPartitionCount = 4
	defaultRetentionDays  = 1

	// keyLen is the length of a base64-shaped shared-access-policy key.
	keyLen = 44
)

// Sku is the hub's billing SKU: name (F1/B1/S1/S2/S3), computed tier and unit
// capacity.
type Sku struct {
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Capacity int64  `json:"capacity"`
}

// SharedAccessPolicy is one shared-access (SAS) authorization rule. The
// primary/secondary keys are minted once at create and stay byte-stable.
type SharedAccessPolicy struct {
	KeyName      string `json:"keyName"`
	PrimaryKey   string `json:"primaryKey"`
	SecondaryKey string `json:"secondaryKey"`
	Rights       string `json:"rights"`
}

// EventHubEndpoint is the built-in Event Hub-compatible ("events") endpoint. Its
// endpoint / path / partitionIds are computed once and stable.
type EventHubEndpoint struct {
	RetentionTimeInDays int64    `json:"retentionTimeInDays"`
	PartitionCount      int64    `json:"partitionCount"`
	PartitionIDs        []string `json:"partitionIds"`
	Path                string   `json:"path"`
	Endpoint            string   `json:"endpoint"`
}

// Hub is a stored Microsoft.Devices/IotHubs resource. Subscription, ResourceGroup
// and Name preserve the caller's casing; the computed fields are minted at create
// and never regenerated on a read.
type Hub struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Sku           Sku               `json:"sku"`

	// Input passthrough fields. Pointers round-trip an explicit false/zero.
	MinTLSVersion                 string          `json:"minTlsVersion,omitempty"`
	PublicNetworkAccess           string          `json:"publicNetworkAccess,omitempty"`
	DisableLocalAuth              *bool           `json:"disableLocalAuth,omitempty"`
	EnableFileUploadNotifications *bool           `json:"enableFileUploadNotifications,omitempty"`
	Routing                       json.RawMessage `json:"routing,omitempty"`

	// Computed, stable fields.
	ProvisioningState string               `json:"provisioningState"`
	State             string               `json:"state"`
	HostName          string               `json:"hostName"`
	Etag              string               `json:"etag"`
	Features          string               `json:"features"`
	Policies          []SharedAccessPolicy `json:"policies"`
	Events            EventHubEndpoint     `json:"events"`
}

// ARMID returns the fully-qualified ARM resource id of the hub.
func (h *Hub) ARMID() string {
	return idgen.AzureID(h.Subscription, h.ResourceGroup, ProviderNamespace, HubType, h.Name)
}

// HubInput carries the mutable fields of a hub create/update request. Pointer
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names while a create seeds defaults.
type HubInput struct {
	Tags                          map[string]string
	SkuName                       *string
	SkuCapacity                   *int64
	PartitionCount                *int64
	RetentionTimeInDays           *int64
	Features                      *string
	MinTLSVersion                 *string
	PublicNetworkAccess           *string
	DisableLocalAuth              *bool
	EnableFileUploadNotifications *bool
	Routing                       json.RawMessage
	// Policies, when non-nil, adds/overrides caller-supplied authorization
	// policies on top of the seeded defaults.
	Policies []SharedAccessPolicy
}

// Mock is the in-memory backend for IoT hubs and their consumer groups.
type Mock struct {
	mu             sync.RWMutex
	hubs           *memstore.Store[*Hub]
	consumerGroups *memstore.Store[*ConsumerGroup]
}

// New creates an empty IoT Hub mock. opts is accepted for signature parity with
// the other Azure services; the hub surface stamps no clock-derived fields.
func New(_ *config.Options) *Mock {
	return &Mock{
		hubs:           memstore.New[*Hub](),
		consumerGroups: memstore.New[*ConsumerGroup](),
	}
}

// hubKey is the case-insensitive store key for a hub.
func hubKey(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, ProviderNamespace, HubType, name))
}

// CreateOrUpdateHub creates a new hub or updates an existing one. The computed
// fields (hostName, provisioningState, state, etag, keys, event-hub endpoint) are
// minted once at create and preserved across updates. Location is immutable in
// real Azure and is preserved on update. A hub name is globally unique in real
// Azure, so a create under a different group with a name already in use elsewhere
// is rejected with a conflict. It returns the stored hub and whether it was newly
// created.
func (m *Mock) CreateOrUpdateHub(
	_ context.Context, sub, rg, name, location string, in *HubInput,
) (Hub, bool, error) {
	if err := validateHub(sub, rg, name, location); err != nil {
		return Hub{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := hubKey(sub, rg, name)

	existing, existed := m.hubs.Get(k)
	created := !existed

	if created {
		if err := m.checkGlobalName(sub, rg, name); err != nil {
			return Hub{}, false, err
		}
	}

	var h Hub
	if existed {
		h = *existing
	} else {
		h = m.newHub(sub, rg, name, location)
	}

	applyHubInput(&h, in)
	m.hubs.Set(k, &h)

	if created {
		m.seedDefaultConsumerGroup(sub, rg, name)
	}

	return cloneHub(&h), created, nil
}

// checkGlobalName rejects a create whose hub name is already taken by a hub in a
// different subscription/resource-group, modeling the global uniqueness of the
// <name>.azure-devices.net hostName. The caller holds the write lock.
func (m *Mock) checkGlobalName(sub, rg, name string) error {
	for _, h := range m.hubs.All() {
		if strings.EqualFold(h.Name, name) &&
			!(strings.EqualFold(h.Subscription, sub) && strings.EqualFold(h.ResourceGroup, rg)) {
			return cerrors.Newf(cerrors.AlreadyExists,
				"iot hub name %q is not available", name)
		}
	}

	return nil
}

// newHub seeds a fresh hub with its immutable identity, ARM defaults and its
// computed, stable fields: the default shared-access policies with minted keys
// and the built-in event-hub endpoint.
func (*Mock) newHub(sub, rg, name, location string) Hub {
	id := hubKey(sub, rg, name)

	return Hub{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		Sku:               Sku{Name: defaultSkuName, Tier: skuTier(defaultSkuName), Capacity: defaultCapacity},
		ProvisioningState: stateSucceeded,
		State:             stateActive,
		HostName:          name + hostSuffix,
		Etag:              idgen.SyntheticGUID("iothub/etag/" + id),
		Features:          featuresNone,
		Policies:          defaultPolicies(id),
		Events:            newEventHubEndpoint(id, name, defaultPartitionCount, defaultRetentionDays),
	}
}

// newEventHubEndpoint builds the built-in "events" endpoint with a deterministic
// service-bus endpoint URL and partition-id list derived from partitionCount.
func newEventHubEndpoint(id, name string, partitionCount, retentionDays int64) EventHubEndpoint {
	ns := "iothub-ns-" + strings.ToLower(name) + "-" + strings.ReplaceAll(idgen.SyntheticGUID("iothub/ns/"+id), "-", "")[:8]

	return EventHubEndpoint{
		RetentionTimeInDays: retentionDays,
		PartitionCount:      partitionCount,
		PartitionIDs:        partitionIDs(partitionCount),
		Path:                name,
		Endpoint:            "sb://" + ns + ".servicebus.windows.net/",
	}
}

// partitionIDs returns the stable ["0".."n-1"] partition-id list.
func partitionIDs(n int64) []string {
	if n <= 0 {
		return []string{}
	}

	out := make([]string, 0, n)
	for i := int64(0); i < n; i++ {
		out = append(out, strconv.FormatInt(i, 10))
	}

	return out
}

// GetHub returns the hub, or a NotFound error.
func (m *Mock) GetHub(_ context.Context, sub, rg, name string) (Hub, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	h, ok := m.hubs.Get(hubKey(sub, rg, name))
	if !ok {
		return Hub{}, cerrors.Newf(cerrors.NotFound, "iot hub %q not found", name)
	}

	return cloneHub(h), nil
}

// DeleteHub removes the hub and cascades to every consumer group under it,
// reporting whether the hub existed.
func (m *Mock) DeleteHub(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existed := m.hubs.Delete(hubKey(sub, rg, name))

	prefix := hubKey(sub, rg, name) + "/"
	for ck := range m.consumerGroups.All() {
		if strings.HasPrefix(ck, prefix) {
			m.consumerGroups.Delete(ck)
		}
	}

	return existed, nil
}

// ListHubsByResourceGroup returns every hub in the group, sorted by name.
func (m *Mock) ListHubsByResourceGroup(_ context.Context, sub, rg string) ([]Hub, error) {
	return m.filterHubs(func(h *Hub) bool {
		return strings.EqualFold(h.Subscription, sub) && strings.EqualFold(h.ResourceGroup, rg)
	}), nil
}

// ListHubsBySubscription returns every hub in the subscription, sorted by name.
func (m *Mock) ListHubsBySubscription(_ context.Context, sub string) ([]Hub, error) {
	return m.filterHubs(func(h *Hub) bool {
		return strings.EqualFold(h.Subscription, sub)
	}), nil
}

// DiscoverHubs returns every stored hub, for the inventory walk.
func (m *Mock) DiscoverHubs(_ context.Context) ([]Hub, error) {
	return m.filterHubs(func(*Hub) bool { return true }), nil
}

// ListKeys returns every shared-access policy of a hub with its stable keys.
func (m *Mock) ListKeys(_ context.Context, sub, rg, name string) ([]SharedAccessPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	h, ok := m.hubs.Get(hubKey(sub, rg, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "iot hub %q not found", name)
	}

	return clonePolicies(h.Policies), nil
}

// GetKeysForKeyName returns a single shared-access policy by key name.
func (m *Mock) GetKeysForKeyName(_ context.Context, sub, rg, name, keyName string) (SharedAccessPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	h, ok := m.hubs.Get(hubKey(sub, rg, name))
	if !ok {
		return SharedAccessPolicy{}, cerrors.Newf(cerrors.NotFound, "iot hub %q not found", name)
	}

	for i := range h.Policies {
		if strings.EqualFold(h.Policies[i].KeyName, keyName) {
			return h.Policies[i], nil
		}
	}

	return SharedAccessPolicy{}, cerrors.Newf(cerrors.NotFound, "shared access policy %q not found", keyName)
}

// PurgeResourceGroup deletes every hub and consumer group under sub/rg, so a
// resource-group delete cascades into its IoT Hub resources.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for k, h := range m.hubs.All() {
		if strings.EqualFold(h.Subscription, sub) && strings.EqualFold(h.ResourceGroup, rg) {
			m.hubs.Delete(k)
		}
	}

	for k, c := range m.consumerGroups.All() {
		if strings.EqualFold(c.Subscription, sub) && strings.EqualFold(c.ResourceGroup, rg) {
			m.consumerGroups.Delete(k)
		}
	}

	return nil
}

// filterHubs returns the hubs matching pred, sorted by name.
func (m *Mock) filterHubs(pred func(*Hub) bool) []Hub {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Hub

	for _, h := range m.hubs.All() {
		if pred(h) {
			out = append(out, cloneHub(h))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyHubInput overlays the request fields onto h. A nil pointer/map means "not
// supplied": the stored (or default) value is preserved, so a PATCH merges only
// what it names. A non-nil tags map replaces the whole set, matching ARM
// resource-level PATCH tags semantics. Location and the computed fields are left
// untouched.
func applyHubInput(h *Hub, in *HubInput) {
	if in.Tags != nil {
		h.Tags = maps.Clone(in.Tags)
	}

	applyHubSku(h, in)
	applyHubEndpoint(h, in)
	applyHubScalars(h, in)
	mergePolicies(h, in.Policies)

	if in.Routing != nil {
		h.Routing = append(json.RawMessage(nil), in.Routing...)
	}
}

// applyHubSku overlays the sku name/capacity and recomputes the derived tier.
func applyHubSku(h *Hub, in *HubInput) {
	if in.SkuName != nil && *in.SkuName != "" {
		h.Sku.Name = *in.SkuName
		h.Sku.Tier = skuTier(*in.SkuName)
	}

	if in.SkuCapacity != nil && *in.SkuCapacity > 0 {
		h.Sku.Capacity = *in.SkuCapacity
	}
}

// applyHubEndpoint retunes the built-in event-hub endpoint when a create/update
// changes the partition count or retention, keeping partitionIds consistent.
func applyHubEndpoint(h *Hub, in *HubInput) {
	if in.PartitionCount != nil && *in.PartitionCount > 0 {
		h.Events.PartitionCount = *in.PartitionCount
		h.Events.PartitionIDs = partitionIDs(*in.PartitionCount)
	}

	if in.RetentionTimeInDays != nil && *in.RetentionTimeInDays > 0 {
		h.Events.RetentionTimeInDays = *in.RetentionTimeInDays
	}
}

// applyHubScalars overlays the scalar passthrough properties onto h.
func applyHubScalars(h *Hub, in *HubInput) {
	if in.Features != nil && *in.Features != "" {
		h.Features = *in.Features
	}

	if in.MinTLSVersion != nil {
		h.MinTLSVersion = *in.MinTLSVersion
	}

	if in.PublicNetworkAccess != nil {
		h.PublicNetworkAccess = *in.PublicNetworkAccess
	}

	if in.DisableLocalAuth != nil {
		v := *in.DisableLocalAuth
		h.DisableLocalAuth = &v
	}

	if in.EnableFileUploadNotifications != nil {
		v := *in.EnableFileUploadNotifications
		h.EnableFileUploadNotifications = &v
	}
}

// mergePolicies adds or overrides caller-supplied authorization policies on top
// of the seeded defaults. A supplied policy with no key gets one minted from the
// hub id and its key name so it too stays byte-stable.
func mergePolicies(h *Hub, supplied []SharedAccessPolicy) {
	id := hubKey(h.Subscription, h.ResourceGroup, h.Name)

	for i := range supplied {
		p := supplied[i]
		if p.KeyName == "" {
			continue
		}

		if p.PrimaryKey == "" {
			p.PrimaryKey = mintKey("iothub/key/primary/" + id + "/" + strings.ToLower(p.KeyName))
		}

		if p.SecondaryKey == "" {
			p.SecondaryKey = mintKey("iothub/key/secondary/" + id + "/" + strings.ToLower(p.KeyName))
		}

		upsertPolicy(h, p)
	}
}

// upsertPolicy replaces an existing policy of the same key name or appends a new
// one.
func upsertPolicy(h *Hub, p SharedAccessPolicy) {
	for i := range h.Policies {
		if strings.EqualFold(h.Policies[i].KeyName, p.KeyName) {
			h.Policies[i] = p
			return
		}
	}

	h.Policies = append(h.Policies, p)
}

// defaultPolicies returns the five built-in shared-access policies real Azure
// seeds at hub create, each with deterministic, stable primary/secondary keys.
func defaultPolicies(id string) []SharedAccessPolicy {
	specs := []struct{ name, rights string }{
		{"iothubowner", "RegistryRead, RegistryWrite, ServiceConnect, DeviceConnect"},
		{"service", "ServiceConnect"},
		{"device", "DeviceConnect"},
		{"registryRead", "RegistryRead"},
		{"registryReadWrite", "RegistryRead, RegistryWrite"},
	}

	out := make([]SharedAccessPolicy, 0, len(specs))
	for _, s := range specs {
		out = append(out, SharedAccessPolicy{
			KeyName:      s.name,
			PrimaryKey:   mintKey("iothub/key/primary/" + id + "/" + s.name),
			SecondaryKey: mintKey("iothub/key/secondary/" + id + "/" + s.name),
			Rights:       s.rights,
		})
	}

	return out
}

// skuTier derives the billing tier from a SKU name (F->Free, B->Basic,
// otherwise Standard).
func skuTier(name string) string {
	switch {
	case strings.HasPrefix(strings.ToUpper(name), "F"):
		return "Free"
	case strings.HasPrefix(strings.ToUpper(name), "B"):
		return "Basic"
	default:
		return "Standard"
	}
}

// mintKey derives a stable 44-character base64-shaped SAS key from a seed.
func mintKey(seed string) string {
	a := strings.ReplaceAll(idgen.SyntheticGUID(seed), "-", "")
	b := strings.ReplaceAll(idgen.SyntheticGUID(seed+"#2"), "-", "")

	return (a + b + a)[:keyLen]
}

// validateHub rejects a hub create/update with missing required fields.
func validateHub(sub, rg, name, location string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "hub name is required")
	case location == "":
		return cerrors.New(cerrors.InvalidArgument, "location is required")
	default:
		return nil
	}
}

// cloneHub deep-copies a stored hub so callers never alias the backing store.
func cloneHub(h *Hub) Hub {
	out := *h
	out.Tags = maps.Clone(h.Tags)
	out.Policies = clonePolicies(h.Policies)
	out.Events.PartitionIDs = append([]string(nil), h.Events.PartitionIDs...)
	out.DisableLocalAuth = cloneBoolPtr(h.DisableLocalAuth)
	out.EnableFileUploadNotifications = cloneBoolPtr(h.EnableFileUploadNotifications)

	if h.Routing != nil {
		out.Routing = append(json.RawMessage(nil), h.Routing...)
	}

	return out
}

// clonePolicies deep-copies a policy slice so callers never alias the store.
func clonePolicies(in []SharedAccessPolicy) []SharedAccessPolicy {
	if in == nil {
		return nil
	}

	return append([]SharedAccessPolicy(nil), in...)
}

// cloneBoolPtr returns a fresh pointer to a copy of *p, or nil.
func cloneBoolPtr(p *bool) *bool {
	if p == nil {
		return nil
	}

	v := *p

	return &v
}
