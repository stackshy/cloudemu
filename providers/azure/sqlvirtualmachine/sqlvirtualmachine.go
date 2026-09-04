// Package sqlvirtualmachine provides an in-memory mock of Azure SQL virtual
// machines (Microsoft.SqlVirtualMachine/sqlVirtualMachines).
//
// A SQL virtual machine is a management overlay resource that Azure layers on
// top of an existing Microsoft.Compute virtual machine: its
// virtualMachineResourceId points at the compute VM, and its own lifecycle
// (registration with the SQL IaaS Agent extension, license type, storage /
// patching / backup configuration) is tracked independently of the VM. The two
// are distinct resources — deleting the SQL virtual machine deregisters SQL
// management but never touches the underlying compute VM.
//
// This mock owns only the SQL-virtual-machine records; the paired compute VM
// lives in the virtualmachines mock. virtualMachineResourceId is stored
// verbatim (its presence is required, but the referenced VM is not resolved),
// matching an emulator that records the link rather than enforcing it.
package sqlvirtualmachine

import (
	"context"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

// providerNamespace and resourceType are the canonical ARM provider namespace
// and type segment, used when composing a record's fully-qualified id with the
// casing real Azure emits.
const (
	providerNamespace = "Microsoft.SqlVirtualMachine"
	resourceType      = "sqlVirtualMachines"
)

// Terminal provisioning state and documented property defaults. Real Azure
// reports a transient "Provisioning"/"Updating" then settles to "Succeeded"
// through an async operation; the emulator settles immediately so an SDK LRO
// poller completes on the first response. sqlImageSku / sqlManagement default to
// the values the real create API fills in when the caller omits them.
const (
	provisioningStateSucceeded = "Succeeded"
	defaultSQLImageSku         = "Unknown"
	defaultSQLManagement       = "Full"
)

// Properties holds the SQL-virtual-machine properties block. The scalar fields
// real callers set (and read back) are modeled explicitly; the nested
// configuration objects (storage, patching, backup, server configuration,
// assessment, identity) round-trip verbatim as raw JSON so the full request
// shape survives without hand-modeling every setting.
type Properties struct {
	VirtualMachineResourceID string `json:"virtualMachineResourceId,omitempty"`
	SQLServerLicenseType     string `json:"sqlServerLicenseType,omitempty"`
	SQLManagement            string `json:"sqlManagement,omitempty"`
	SQLImageSku              string `json:"sqlImageSku,omitempty"`
	SQLImageOffer            string `json:"sqlImageOffer,omitempty"`
	OSType                   string `json:"osType,omitempty"`
	LeastPrivilegeMode       string `json:"leastPrivilegeMode,omitempty"`
	EnableAutomaticUpgrade   *bool  `json:"enableAutomaticUpgrade,omitempty"`
	ProvisioningState        string `json:"provisioningState,omitempty"`

	AutoBackupSettings                     json.RawMessage `json:"autoBackupSettings,omitempty"`
	AutoPatchingSettings                   json.RawMessage `json:"autoPatchingSettings,omitempty"`
	KeyVaultCredentialSettings             json.RawMessage `json:"keyVaultCredentialSettings,omitempty"`
	StorageConfigurationSettings           json.RawMessage `json:"storageConfigurationSettings,omitempty"`
	ServerConfigurationsManagementSettings json.RawMessage `json:"serverConfigurationsManagementSettings,omitempty"`
	AssessmentSettings                     json.RawMessage `json:"assessmentSettings,omitempty"`
	VirtualMachineIdentitySettings         json.RawMessage `json:"virtualMachineIdentitySettings,omitempty"`
}

// Record is a stored SQL virtual machine. Subscription, ResourceGroup and Name
// preserve the caller's original casing for the response id.
type Record struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Properties    Properties        `json:"properties"`
}

// ARMID returns the fully-qualified ARM resource id for the record.
func (r *Record) ARMID() string {
	return "/subscriptions/" + r.Subscription +
		"/resourceGroups/" + r.ResourceGroup +
		"/providers/" + providerNamespace +
		"/" + resourceType +
		"/" + r.Name
}

// Input carries the mutable fields of a create-or-update request. A PUT is a
// full replace: every field on the stored record is taken from Input.
type Input struct {
	Location   string
	Tags       map[string]string
	Properties Properties
}

// Mock is the in-memory backend for SQL virtual machines.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[Record]
}

// New creates an empty SQL-virtual-machine mock.
func New(_ *config.Options) *Mock {
	return &Mock{store: memstore.New[Record]()}
}

// key is the case-insensitive store key for a record.
func key(sub, rg, name string) string {
	return strings.ToLower("/subscriptions/" + sub +
		"/resourceGroups/" + rg +
		"/providers/" + providerNamespace +
		"/" + resourceType +
		"/" + name)
}

// CreateOrUpdate creates a new SQL virtual machine or replaces an existing one
// (ARM PUT is a full replace). virtualMachineResourceId is required; the
// provisioning state settles to Succeeded immediately, and the two documented
// defaults are filled in when the caller omits them. It returns the stored
// record and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name string, in *Input) (Record, bool, error) {
	if strings.TrimSpace(in.Properties.VirtualMachineResourceID) == "" {
		return Record{}, false, cerrors.New(cerrors.InvalidArgument,
			"sqlVirtualMachine requires properties.virtualMachineResourceId")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)
	_, existed := m.store.Get(k)

	props := in.Properties
	if props.SQLImageSku == "" {
		props.SQLImageSku = defaultSQLImageSku
	}

	if props.SQLManagement == "" {
		props.SQLManagement = defaultSQLManagement
	}

	props.ProvisioningState = provisioningStateSucceeded

	rec := Record{
		Subscription:  sub,
		ResourceGroup: rg,
		Name:          name,
		Location:      in.Location,
		Tags:          maps.Clone(in.Tags),
		Properties:    props,
	}

	m.store.Set(k, rec)

	return rec, !existed, nil
}

// Get returns the SQL virtual machine, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return Record{}, cerrors.Newf(cerrors.NotFound, "sqlVirtualMachine %q not found", name)
	}

	return rec, nil
}

// UpdateTags replaces the record's tag set wholesale (ARM PATCH UpdateTags is a
// full replace, not a merge; a nil/empty map clears every tag) and leaves the
// properties untouched. Updating a missing record is a NotFound error.
func (m *Mock) UpdateTags(_ context.Context, sub, rg, name string, tags map[string]string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	rec, ok := m.store.Get(k)
	if !ok {
		return Record{}, cerrors.Newf(cerrors.NotFound, "sqlVirtualMachine %q not found", name)
	}

	rec.Tags = maps.Clone(tags)
	m.store.Set(k, rec)

	return rec, nil
}

// Delete removes the SQL virtual machine, reporting whether it existed. The
// paired compute VM is a separate resource and is left untouched.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// ListByResourceGroup returns every SQL virtual machine in the resource group,
// sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]Record, error) {
	return m.filter(func(r Record) bool {
		return strings.EqualFold(r.Subscription, sub) && strings.EqualFold(r.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every SQL virtual machine in the subscription,
// sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]Record, error) {
	return m.filter(func(r Record) bool {
		return strings.EqualFold(r.Subscription, sub)
	}), nil
}

// DiscoverSQLVirtualMachines returns every stored record, for the inventory walk.
func (m *Mock) DiscoverSQLVirtualMachines(_ context.Context) ([]Record, error) {
	return m.filter(func(Record) bool { return true }), nil
}

// PurgeResourceGroup deletes every record under sub/rg, so a resource-group
// delete cascades into its SQL virtual machines.
func (m *Mock) PurgeResourceGroup(_ context.Context, sub, rg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	all := m.store.All()
	for k := range all {
		rec := all[k]
		if strings.EqualFold(rec.Subscription, sub) && strings.EqualFold(rec.ResourceGroup, rg) {
			m.store.Delete(k)
		}
	}

	return nil
}

// filter returns the records matching pred, sorted by name for a stable order.
func (m *Mock) filter(pred func(Record) bool) []Record {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Record

	all := m.store.All()
	for k := range all {
		rec := all[k]
		if pred(rec) {
			out = append(out, rec)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}
