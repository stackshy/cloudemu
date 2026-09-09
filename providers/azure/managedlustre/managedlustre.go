// Package managedlustre provides an in-memory mock of Azure Managed Lustre
// (Microsoft.StorageCache/amlFilesystems) — the ARM control plane only. It
// manages the amlFilesystems resource lifecycle (create/update/get/delete/list)
// and the archive/cancelArchive HSM actions; the Lustre data plane (a mounted
// file system, HSM hydration to blob storage) is out of scope.
//
// An amlFilesystem carries a set of computed, service-minted fields that MUST
// stay stable for the lifetime of the resource so infrastructure-as-code tools
// (Terraform's azurerm_managed_lustre_file_system) see no drift on re-plan:
//   - clientInfo.mgsAddress: a deterministic IPv4, derived once from the name —
//     this is Terraform's exported `mgs_address` and the drift-critical field.
//   - clientInfo.lustreVersion / mountCommand: deterministic from mgsAddress.
//   - provisioningState: "Succeeded" once provisioning completes.
//   - health: {state:"Available"} — a healthy, ready file system.
//   - throughputProvisionedMBps: storageCapacityTiB * the per-TiB throughput
//     encoded in the sku name (AMLFS-Durable-Premium-250 -> 250 MB/s per TiB).
//   - identity principal/client ids: minted once via idgen.SyntheticGUID off a
//     stable seed and stored, so they never change across get/patch/restore.
//
// Every computed field is derived deterministically from the resource identity,
// so the same resource always reports the same values across gets, patches,
// archive actions and a snapshot/restore.
package managedlustre

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
	providerNamespace = "Microsoft.StorageCache"
	// resourceType is the ARM resource type segment.
	resourceType = "amlFilesystems"
	// stateSucceeded is the terminal provisioning state a synchronous ARM PUT
	// reaches immediately.
	stateSucceeded = "Succeeded"
	// lustreVersion is the reported Lustre software version (matches real Azure).
	lustreVersion = "2.15.0"
	// defaultImportPrefix is the HSM import prefix real Azure applies by default.
	defaultImportPrefix = "/"
	// mgsFirstOctet / mgsSecondOctet anchor the synthetic MGS address in a
	// private, Lustre-looking 10.0/16 range so it reads like a real subnet IP.
	mgsFirstOctet  = 10
	mgsSecondOctet = 0
	// octetMod keeps a synthetic host octet in the 4..253 range (avoiding the
	// reserved low addresses of a subnet).
	octetMod  = 250
	octetBase = 4
	// archiveComplete is the terminal state of a synchronous archive action.
	archiveComplete = "Completed"
	// archiveCanceled is the state a cancelArchive action leaves behind.
	archiveCanceled = "Canceled"
	// archivePercentComplete is the completion percentage of a finished archive.
	archivePercentComplete = 100
)

// Sku is the pricing tier of an amlFilesystem. Only Name is carried; real Azure
// echoes just {name} (e.g. AMLFS-Durable-Premium-250).
type Sku struct {
	Name string `json:"name"`
}

// UserAssignedValue is the pair of ids Azure mints for a user-assigned identity
// once it is attached to a resource.
type UserAssignedValue struct {
	PrincipalID string `json:"principalId"`
	ClientID    string `json:"clientId"`
}

// Identity is a managed identity attached to an amlFilesystem. Managed Lustre
// supports only UserAssigned (or None); the user-assigned ids are minted on
// assignment and stored so they stay stable.
type Identity struct {
	Type         string                       `json:"type"`
	TenantID     string                       `json:"tenantId,omitempty"`
	UserAssigned map[string]UserAssignedValue `json:"userAssignedIdentities,omitempty"`
}

// MaintenanceWindow is the start of the weekly 30-minute maintenance window.
type MaintenanceWindow struct {
	DayOfWeek    string `json:"dayOfWeek"`
	TimeOfDayUTC string `json:"timeOfDayUTC"`
}

// HsmSettings are the hydrate/archive blob-container settings.
type HsmSettings struct {
	Container             string   `json:"container,omitempty"`
	ImportPrefix          string   `json:"importPrefix,omitempty"`
	LoggingContainer      string   `json:"loggingContainer,omitempty"`
	ImportPrefixesInitial []string `json:"importPrefixesInitial,omitempty"`
}

// ArchiveStatus is the status of a single archive operation.
type ArchiveStatus struct {
	State              string `json:"state,omitempty"`
	LastStartedTime    string `json:"lastStartedTime,omitempty"`
	LastCompletionTime string `json:"lastCompletionTime,omitempty"`
	PercentComplete    int    `json:"percentComplete,omitempty"`
	ErrorCode          string `json:"errorCode,omitempty"`
	ErrorMessage       string `json:"errorMessage,omitempty"`
}

// Archive is one archive-status entry under hsm.archiveStatus.
type Archive struct {
	FilesystemPath string        `json:"filesystemPath,omitempty"`
	Status         ArchiveStatus `json:"status"`
}

// KeyVaultKeyReference locates a customer-managed encryption key in Key Vault.
type KeyVaultKeyReference struct {
	KeyURL      string `json:"keyUrl,omitempty"`
	SourceVault string `json:"sourceVault,omitempty"`
}

// EncryptionSettings carry the optional customer-managed key configuration.
type EncryptionSettings struct {
	KeyEncryptionKey *KeyVaultKeyReference `json:"keyEncryptionKey,omitempty"`
}

// AmlFilesystem is a stored Microsoft.StorageCache/amlFilesystems resource.
// Subscription, ResourceGroup and Name preserve the caller's casing; the
// computed fields are minted at create and preserved across updates.
type AmlFilesystem struct {
	Subscription  string            `json:"subscription"`
	ResourceGroup string            `json:"resourceGroup"`
	Name          string            `json:"name"`
	Location      string            `json:"location"`
	Tags          map[string]string `json:"tags,omitempty"`
	Sku           *Sku              `json:"sku,omitempty"`
	Identity      *Identity         `json:"identity,omitempty"`
	Zones         []string          `json:"zones,omitempty"`

	// Writable properties.
	StorageCapacityTiB float64             `json:"storageCapacityTiB"`
	FilesystemSubnet   string              `json:"filesystemSubnet,omitempty"`
	MaintenanceWindow  *MaintenanceWindow  `json:"maintenanceWindow,omitempty"`
	Hsm                *HsmSettings        `json:"hsm,omitempty"`
	Encryption         *EncryptionSettings `json:"encryptionSettings,omitempty"`

	// Computed, stable fields.
	ProvisioningState         string    `json:"provisioningState"`
	MgsAddress                string    `json:"mgsAddress"`
	ThroughputProvisionedMBps int       `json:"throughputProvisionedMBps"`
	ClusterUUID               string    `json:"clusterUuid"`
	ArchiveStatus             []Archive `json:"archiveStatus,omitempty"`
}

// ARMID returns the fully-qualified ARM resource id.
func (s *AmlFilesystem) ARMID() string {
	return idgen.AzureID(s.Subscription, s.ResourceGroup, providerNamespace, resourceType, s.Name)
}

// MountCommand is the recommended Lustre mount command, deterministic from the
// MGS address.
func (s *AmlFilesystem) MountCommand() string {
	return "mount -t lustre " + s.MgsAddress + "@tcp:/lustrefs /lustre/lustrefs"
}

// LustreVersion returns the reported Lustre software version.
func (*AmlFilesystem) LustreVersion() string { return lustreVersion }

// Input carries the mutable fields of a create/update request. Pointer/slice
// fields distinguish "not supplied" (nil, preserve existing) from an explicit
// value, so a PATCH overlays only what it names.
type Input struct {
	Tags               map[string]string
	Sku                *Sku
	Identity           *Identity
	Zones              []string
	StorageCapacityTiB *float64
	FilesystemSubnet   *string
	MaintenanceWindow  *MaintenanceWindow
	Hsm                *HsmSettings
	Encryption         *EncryptionSettings
}

// Mock is the in-memory backend for amlFilesystems resources.
type Mock struct {
	mu    sync.RWMutex
	store *memstore.Store[*AmlFilesystem]

	// tenantID is the single AAD tenant this estate belongs to; every attached
	// identity reports it. Deterministic, so it survives a restart unpersisted.
	tenantID string
	opts     *config.Options
}

// New creates an empty Managed Lustre mock.
func New(o *config.Options) *Mock {
	return &Mock{
		store:    memstore.New[*AmlFilesystem](),
		tenantID: idgen.SyntheticGUID("cloudemu/azure/tenant"),
		opts:     o,
	}
}

// key is the case-insensitive store key for a resource.
func key(sub, rg, name string) string {
	return strings.ToLower(idgen.AzureID(sub, rg, providerNamespace, resourceType, name))
}

// CreateOrUpdate creates a new amlFilesystem or updates an existing one. The
// computed fields (mgsAddress, cluster uuid, throughput, identity ids) are
// minted once at create and preserved across updates so they stay stable.
// Location is immutable and preserved on update. It returns the stored resource
// and whether it was newly created.
func (m *Mock) CreateOrUpdate(_ context.Context, sub, rg, name, location string, in *Input) (AmlFilesystem, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	existing, existed := m.store.Get(k)
	created := !existed

	if err := validate(sub, rg, name, in, created); err != nil {
		return AmlFilesystem{}, false, err
	}

	var s AmlFilesystem
	if existed {
		s = *existing
	} else {
		s = newAmlFilesystem(sub, rg, name, location)
	}

	applyInput(&s, in)
	s.Identity = m.resolveIdentity(in.Identity, existing, existed)
	mintComputed(&s)

	m.store.Set(k, &s)

	return clone(&s), created, nil
}

// newAmlFilesystem seeds a fresh resource with its immutable identity and the
// deterministic MGS address and cluster uuid derived from its resource id.
func newAmlFilesystem(sub, rg, name, location string) AmlFilesystem {
	id := key(sub, rg, name)

	return AmlFilesystem{
		Subscription:      sub,
		ResourceGroup:     rg,
		Name:              name,
		Location:          location,
		ProvisioningState: stateSucceeded,
		MgsAddress:        syntheticIP(id),
		ClusterUUID:       idgen.SyntheticGUID("cluster/" + id),
	}
}

// Get returns the resource, or a NotFound error.
func (m *Mock) Get(_ context.Context, sub, rg, name string) (AmlFilesystem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.store.Get(key(sub, rg, name))
	if !ok {
		return AmlFilesystem{}, cerrors.Newf(cerrors.NotFound, "aml filesystem %q not found", name)
	}

	return clone(s), nil
}

// Delete removes the resource, reporting whether it existed.
func (m *Mock) Delete(_ context.Context, sub, rg, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.store.Delete(key(sub, rg, name)), nil
}

// Archive records a synchronous HSM archive of filesystemPath. Real Azure runs
// this as a background HSM job; the emulator completes it immediately, leaving a
// Completed archiveStatus entry. filesystemPath defaults to "/" (archive all).
// Archive requires HSM settings to be configured, matching real Azure.
func (m *Mock) Archive(_ context.Context, sub, rg, name, filesystemPath string) error {
	return m.mutateArchive(sub, rg, name, filesystemPath, archiveComplete)
}

// CancelArchive cancels an in-progress archive, leaving a Canceled archiveStatus
// entry. Like Archive it requires HSM settings to be configured.
func (m *Mock) CancelArchive(_ context.Context, sub, rg, name string) error {
	return m.mutateArchive(sub, rg, name, "", archiveCanceled)
}

// mutateArchive is the shared body of Archive/CancelArchive: it validates the
// resource exists and has HSM configured, then records an archiveStatus entry.
func (m *Mock) mutateArchive(sub, rg, name, filesystemPath, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	k := key(sub, rg, name)

	s, ok := m.store.Get(k)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "aml filesystem %q not found", name)
	}

	if s.Hsm == nil {
		return cerrors.New(cerrors.FailedPrecondition, "archive requires HSM settings to be configured")
	}

	path := filesystemPath
	if path == "" {
		path = defaultImportPrefix
	}

	now := m.opts.Clock.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	updated := *s
	updated.ArchiveStatus = []Archive{{
		FilesystemPath: path,
		Status: ArchiveStatus{
			State:              state,
			LastStartedTime:    now,
			LastCompletionTime: now,
			PercentComplete:    archivePercentComplete,
		},
	}}

	m.store.Set(k, &updated)

	return nil
}

// ListByResourceGroup returns every resource in the group, sorted by name.
func (m *Mock) ListByResourceGroup(_ context.Context, sub, rg string) ([]AmlFilesystem, error) {
	return m.filter(func(s *AmlFilesystem) bool {
		return strings.EqualFold(s.Subscription, sub) && strings.EqualFold(s.ResourceGroup, rg)
	}), nil
}

// ListBySubscription returns every resource in the subscription, sorted by name.
func (m *Mock) ListBySubscription(_ context.Context, sub string) ([]AmlFilesystem, error) {
	return m.filter(func(s *AmlFilesystem) bool {
		return strings.EqualFold(s.Subscription, sub)
	}), nil
}

// DiscoverAmlFilesystems returns every stored resource, for the inventory walk.
func (m *Mock) DiscoverAmlFilesystems(_ context.Context) ([]AmlFilesystem, error) {
	return m.filter(func(*AmlFilesystem) bool { return true }), nil
}

// PurgeResourceGroup deletes every amlFilesystem under sub/rg, so a
// resource-group delete cascades into its file systems.
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
func (m *Mock) filter(pred func(*AmlFilesystem) bool) []AmlFilesystem {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []AmlFilesystem

	for _, s := range m.store.All() {
		if pred(s) {
			out = append(out, clone(s))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// applyInput overlays the mutable request fields onto s, leaving the immutable
// location and computed fields untouched. A nil pointer/slice field means "not
// supplied": the stored value is preserved, so a PATCH merges only what it names.
func applyInput(s *AmlFilesystem, in *Input) {
	if in.Tags != nil {
		s.Tags = maps.Clone(in.Tags)
	}

	if in.Sku != nil && in.Sku.Name != "" {
		s.Sku = &Sku{Name: in.Sku.Name}
	}

	if in.Zones != nil {
		s.Zones = slices.Clone(in.Zones)
	}

	if in.StorageCapacityTiB != nil {
		s.StorageCapacityTiB = *in.StorageCapacityTiB
	}

	if in.FilesystemSubnet != nil {
		s.FilesystemSubnet = *in.FilesystemSubnet
	}

	if in.MaintenanceWindow != nil {
		mw := *in.MaintenanceWindow
		s.MaintenanceWindow = &mw
	}

	if in.Hsm != nil {
		s.Hsm = resolveHsm(in.Hsm)
	}

	if in.Encryption != nil {
		s.Encryption = cloneEncryption(in.Encryption)
	}
}

// resolveHsm normalizes incoming HSM settings, defaulting the import prefix to
// "/" as real Azure does.
func resolveHsm(in *HsmSettings) *HsmSettings {
	out := &HsmSettings{
		Container:             in.Container,
		ImportPrefix:          in.ImportPrefix,
		LoggingContainer:      in.LoggingContainer,
		ImportPrefixesInitial: slices.Clone(in.ImportPrefixesInitial),
	}
	if out.ImportPrefix == "" {
		out.ImportPrefix = defaultImportPrefix
	}

	return out
}

// mintComputed (re)derives the stable, service-minted fields. mgsAddress and the
// cluster uuid are seeded at create and preserved; throughput recomputes from
// the (possibly updated) sku and storage capacity.
func mintComputed(s *AmlFilesystem) {
	s.ProvisioningState = stateSucceeded
	s.ThroughputProvisionedMBps = int(s.StorageCapacityTiB) * throughputPerTiB(s.Sku)
}

// throughputPerTiB extracts the per-TiB throughput encoded in the sku name's
// trailing number (AMLFS-Durable-Premium-250 -> 250 MB/s per TiB). An unparsable
// or nil sku yields 0.
func throughputPerTiB(sku *Sku) int {
	if sku == nil {
		return 0
	}

	i := strings.LastIndexByte(sku.Name, '-')
	if i < 0 || i+1 >= len(sku.Name) {
		return 0
	}

	n, err := strconv.Atoi(sku.Name[i+1:])
	if err != nil {
		return 0
	}

	return n
}

// resolveIdentity normalizes an incoming managed identity, synthesizing the
// deterministic ids Azure mints on assignment. Ids already minted on the stored
// resource are preserved so they stay stable across updates. A nil or "None"
// identity resolves to nil.
func (m *Mock) resolveIdentity(in *Identity, existing *AmlFilesystem, existed bool) *Identity {
	if in == nil || in.Type == "" || strings.EqualFold(in.Type, "None") {
		return nil
	}

	out := &Identity{Type: in.Type, TenantID: m.tenantID}

	if len(in.UserAssigned) == 0 {
		return out
	}

	out.UserAssigned = make(map[string]UserAssignedValue, len(in.UserAssigned))

	for id := range in.UserAssigned {
		if existed && existing.Identity != nil {
			if v, ok := existing.Identity.UserAssigned[id]; ok {
				out.UserAssigned[id] = v
				continue
			}
		}

		out.UserAssigned[id] = UserAssignedValue{
			PrincipalID: idgen.SyntheticGUID("ua-principal/" + strings.ToLower(id)),
			ClientID:    idgen.SyntheticGUID("ua-client/" + strings.ToLower(id)),
		}
	}

	return out
}

// syntheticIP derives a stable private IPv4 (10.0.x.y) from a resource id, used
// as the deterministic MGS address.
func syntheticIP(id string) string {
	g := strings.ReplaceAll(idgen.SyntheticGUID("mgs/"+id), "-", "")

	return strconv.Itoa(mgsFirstOctet) + "." + strconv.Itoa(mgsSecondOctet) + "." +
		octet(g[0:2]) + "." + octet(g[2:4])
}

// octet parses a hex byte pair into a decimal string in the 4..253 range.
func octet(hexPair string) string {
	v, err := strconv.ParseUint(hexPair, 16, 16)
	if err != nil {
		return strconv.Itoa(octetBase)
	}

	return strconv.FormatUint(v%octetMod+octetBase, 10)
}

// validate rejects a create/update with missing required fields. On create the
// sku name, storage capacity and filesystem subnet are required; a PATCH may
// omit them (preserving the stored value), so they are enforced only on create.
func validate(sub, rg, name string, in *Input, created bool) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "aml filesystem name is required")
	}

	if !created {
		return nil
	}

	return validateCreateRequired(in)
}

// validateCreateRequired enforces the fields real Azure requires on create.
func validateCreateRequired(in *Input) error {
	switch {
	case in.Sku == nil || in.Sku.Name == "":
		return cerrors.New(cerrors.InvalidArgument, "sku.name is required")
	case in.StorageCapacityTiB == nil || *in.StorageCapacityTiB <= 0:
		return cerrors.New(cerrors.InvalidArgument, "storageCapacityTiB must be a positive number")
	case in.FilesystemSubnet == nil || *in.FilesystemSubnet == "":
		return cerrors.New(cerrors.InvalidArgument, "filesystemSubnet is required")
	default:
		return nil
	}
}

// clone deep-copies a stored resource so callers never alias the backing store.
func clone(s *AmlFilesystem) AmlFilesystem {
	out := *s
	out.Tags = maps.Clone(s.Tags)
	out.Zones = slices.Clone(s.Zones)

	if s.Sku != nil {
		sku := *s.Sku
		out.Sku = &sku
	}

	if s.MaintenanceWindow != nil {
		mw := *s.MaintenanceWindow
		out.MaintenanceWindow = &mw
	}

	if s.Hsm != nil {
		out.Hsm = resolveHsm(s.Hsm)
	}

	out.Encryption = cloneEncryption(s.Encryption)
	out.ArchiveStatus = slices.Clone(s.ArchiveStatus)

	if s.Identity != nil {
		id := *s.Identity
		id.UserAssigned = maps.Clone(s.Identity.UserAssigned)
		out.Identity = &id
	}

	return out
}

// cloneEncryption deep-copies the optional encryption settings.
func cloneEncryption(in *EncryptionSettings) *EncryptionSettings {
	if in == nil {
		return nil
	}

	out := &EncryptionSettings{}

	if in.KeyEncryptionKey != nil {
		kek := *in.KeyEncryptionKey
		out.KeyEncryptionKey = &kek
	}

	return out
}
