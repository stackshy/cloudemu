// Package vault provides an in-memory mock implementation of OCI Vault. It
// implements the portable secrets driver, and carries alongside it the KMS
// surface the portable interface has no room for: vaults, master encryption
// keys, key versions, and OCI's scheduled deletion.
package vault

import (
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/scope"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// Compile-time check that Mock implements the portable driver. The OCI-shaped
// capabilities live in server/oci/vault and are checked there.
var _ driver.Secrets = (*Mock)(nil)

const timeFormat = time.RFC3339

// day is the unit OCI states its deletion windows in.
const day = 24 * time.Hour

// Lifecycle states an OCI Vault resource reports. The KMS surface and the
// secret surface do not share a live state: per the SDK's
// KeyLifecycleStateEnum a key or key version is ENABLED, never ACTIVE, while
// VaultLifecycleStateEnum and SecretLifecycleStateEnum both use ACTIVE.
// terraform-provider-oci's oci_kms_key waits on ENABLED, so the distinction is
// load-bearing. The rest of KeyLifecycleStateEnum — ENABLING, DISABLING,
// DISABLED — arrives with the enableKey/disableKey operations, which this
// handler does not serve.
const (
	StateActive          = "ACTIVE"
	StateEnabled         = "ENABLED"
	StatePendingDeletion = "PENDING_DELETION"
)

// Vault types.
const (
	VaultTypeDefault        = "DEFAULT"
	VaultTypeVirtualPrivate = "VIRTUAL_PRIVATE"
)

// OCID resource type segments.
const (
	typeVault      = "vault"
	typeKey        = "key"
	typeKeyVersion = "keyversion"
	typeSecret     = "vaultsecret"
)

// Scheduled-deletion windows, in days. Real OCI refuses a time outside them
// and defaults to the far end when the caller names none.
const (
	minSecretDeletionDays = 1
	minVaultDeletionDays  = 7
	minKeyDeletionDays    = 7
	maxDeletionDays       = 30
)

// Names the vault and key that the portable driver creates its secrets in.
const (
	defaultVaultName = "cloudemu-default-vault"
	defaultKeyName   = "cloudemu-default-key"
)

// VaultSpec describes a vault to create.
//
//nolint:revive // Spec alone would not distinguish it from KeySpec and SecretSpec.
type VaultSpec struct {
	CompartmentID string
	DisplayName   string
	// VaultType is DEFAULT or VIRTUAL_PRIVATE; empty means DEFAULT.
	VaultType    string
	FreeformTags map[string]string
}

// Update carries the mutable fields of a vault or a key. A nil field leaves
// the stored value alone.
type Update struct {
	DisplayName  *string
	FreeformTags map[string]string
}

// VaultInfo describes a vault.
//
//nolint:revive // Info alone would not distinguish it from KeyInfo and SecretInfo.
type VaultInfo struct {
	ID            string
	CompartmentID string
	DisplayName   string
	VaultType     string
	WrappingKeyID string
	// EndpointLabel is the short host label a vault's endpoints can lead
	// with. The wire layer renders the endpoints against its own address.
	EndpointLabel  string
	LifecycleState string
	TimeCreated    string
	TimeOfDeletion string
	FreeformTags   map[string]string
}

type vaultData struct {
	ID             string
	DisplayName    string
	VaultType      string
	WrappingKeyID  string
	LifecycleState string
	TimeCreated    string
	TimeOfDeletion string
	Scope          scope.Scope
	FreeformTags   map[string]string
}

// Mock is an in-memory mock implementation of the OCI Vault service.
type Mock struct {
	// mu guards the fields of stored values and spans the reads and writes a
	// single operation makes across stores. Each store locks its own map, but
	// the pointers it hands back are mutated in place, and checks such as
	// "does this secret's vault still exist" read one store before writing
	// another.
	mu sync.RWMutex

	vaults      *memstore.Store[*vaultData]
	keys        *memstore.Store[*keyData]
	keyVersions *memstore.Store[*keyVersionData]
	secrets     *memstore.Store[*secretData]

	// The vault and key the portable driver puts its secrets in, created on
	// the first portable CreateSecret. OCI has no secret outside a vault.
	defaultVaultID string
	defaultKeyID   string

	opts *config.Options
}

// New creates a new OCI Vault mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		vaults:      memstore.New[*vaultData](),
		keys:        memstore.New[*keyData](),
		keyVersions: memstore.New[*keyVersionData](),
		secrets:     memstore.New[*secretData](),
		opts:        opts,
	}
}

// newOCID mints an OCID for the given resource type in the configured realm
// and region.
func (m *Mock) newOCID(resourceType string) string {
	return idgen.OCID(resourceType, m.opts.Realm, m.opts.OCIRegion())
}

// now returns the current time in OCI's timestamp format.
func (m *Mock) now() string {
	return m.opts.Clock.Now().UTC().Format(timeFormat)
}

// CreateVault creates a vault. Real OCI runs this asynchronously; CloudEmu
// completes it before returning and the wire layer records the work request.
func (m *Mock) CreateVault(spec *VaultSpec) (*VaultInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if spec.DisplayName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "displayName is required")
	}

	vaultType := spec.VaultType
	if vaultType == "" {
		vaultType = VaultTypeDefault
	}

	if vaultType != VaultTypeDefault && vaultType != VaultTypeVirtualPrivate {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"vaultType %q is not one of %s, %s", spec.VaultType, VaultTypeDefault, VaultTypeVirtualPrivate)
	}

	v := m.newVaultLocked(spec, vaultType)

	info := m.toVaultInfo(v)

	return &info, nil
}

// newVaultLocked stores a vault built from spec.
func (m *Mock) newVaultLocked(spec *VaultSpec, vaultType string) *vaultData {
	id := m.newOCID(typeVault)
	v := &vaultData{
		ID:             id,
		DisplayName:    spec.DisplayName,
		VaultType:      vaultType,
		WrappingKeyID:  m.newOCID(typeKey),
		LifecycleState: StateActive,
		TimeCreated:    m.now(),
		Scope:          scope.Scope{Compartment: m.compartmentOr(spec.CompartmentID)},
		FreeformTags:   copyTags(spec.FreeformTags),
	}

	m.vaults.Set(id, v)

	return v
}

// GetVault returns a vault by OCID.
func (m *Mock) GetVault(id string) (*VaultInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	v, err := m.vaultLocked(id)
	if err != nil {
		return nil, err
	}

	info := m.toVaultInfo(v)

	return &info, nil
}

// ListVaults returns the vaults in a compartment, ordered by OCID.
func (m *Mock) ListVaults(compartmentID string) ([]VaultInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	out := make([]VaultInfo, 0, m.vaults.Len())

	for _, v := range m.vaults.SortedValues() {
		if !v.Scope.Matches(filter) {
			continue
		}

		out = append(out, m.toVaultInfo(v))
	}

	return out, nil
}

// UpdateVault replaces a vault's display name and freeform tags.
func (m *Mock) UpdateVault(id string, upd Update) (*VaultInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, err := m.vaultLocked(id)
	if err != nil {
		return nil, err
	}

	if upd.DisplayName != nil {
		v.DisplayName = *upd.DisplayName
	}

	if upd.FreeformTags != nil {
		v.FreeformTags = copyTags(upd.FreeformTags)
	}

	info := m.toVaultInfo(v)

	return &info, nil
}

// ScheduleVaultDeletion marks a vault for deletion at the given time, which
// must fall between 7 and 30 days out. An empty time takes the far end, as
// real OCI does. Every key in the vault not already pending deletion follows
// it; a key already due after the vault is refused. Nothing reaps the vault:
// it stays PENDING_DELETION until the deletion is canceled.
func (m *Mock) ScheduleVaultDeletion(id, at string) (*VaultInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, err := m.vaultLocked(id)
	if err != nil {
		return nil, err
	}

	if v.LifecycleState == StatePendingDeletion {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "vault %s is already scheduled for deletion", id)
	}

	when, err := m.deletionTime(at, minVaultDeletionDays)
	if err != nil {
		return nil, err
	}

	keys := m.vaultKeysLocked(id)

	for _, k := range keys {
		if k.LifecycleState == StatePendingDeletion && k.TimeOfDeletion > when {
			return nil, cerrors.Newf(cerrors.FailedPrecondition,
				"key %s is scheduled for deletion at %s, after the vault", k.ID, k.TimeOfDeletion)
		}
	}

	for _, k := range keys {
		if k.LifecycleState == StatePendingDeletion {
			continue
		}

		k.LifecycleState = StatePendingDeletion
		k.TimeOfDeletion = when
		k.PendingWithVault = true
	}

	v.LifecycleState = StatePendingDeletion
	v.TimeOfDeletion = when

	info := m.toVaultInfo(v)

	return &info, nil
}

// CancelVaultDeletion returns a vault scheduled for deletion to ACTIVE, and
// restores the keys its scheduling moved.
func (m *Mock) CancelVaultDeletion(id string) (*VaultInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	v, err := m.vaultLocked(id)
	if err != nil {
		return nil, err
	}

	if v.LifecycleState != StatePendingDeletion {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "vault %s is not scheduled for deletion", id)
	}

	for _, k := range m.vaultKeysLocked(id) {
		if k.PendingWithVault {
			k.LifecycleState = StateEnabled
			k.TimeOfDeletion = ""
			k.PendingWithVault = false
		}
	}

	v.LifecycleState = StateActive
	v.TimeOfDeletion = ""

	info := m.toVaultInfo(v)

	return &info, nil
}

// ChangeVaultCompartment moves a vault to another compartment. The keys and
// secrets it holds keep the compartment they were created in, as in real OCI.
func (m *Mock) ChangeVaultCompartment(id, compartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	v, err := m.vaultLocked(id)
	if err != nil {
		return err
	}

	v.Scope = scope.Scope{Compartment: compartmentID}

	return nil
}

// vaultLocked reads a vault, reporting OCI's not-found for an unknown OCID.
func (m *Mock) vaultLocked(id string) (*vaultData, error) {
	v, ok := m.vaults.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "vault %s not found", id)
	}

	return v, nil
}

// vaultKeysLocked returns the keys a vault holds.
func (m *Mock) vaultKeysLocked(vaultID string) []*keyData {
	var out []*keyData

	for _, k := range m.keys.SortedValues() {
		if k.VaultID == vaultID {
			out = append(out, k)
		}
	}

	return out
}

// requireActiveVaultLocked checks that a vault can still take new keys and
// secrets.
func (m *Mock) requireActiveVaultLocked(id string) error {
	v, err := m.vaultLocked(id)
	if err != nil {
		return err
	}

	if v.LifecycleState != StateActive {
		return cerrors.Newf(cerrors.FailedPrecondition, "vault %s is %s", id, v.LifecycleState)
	}

	return nil
}

// soleVaultLocked picks the vault a key request that names none addresses.
// Every vault shares the plain address, so it is certain only while the
// tenancy has exactly one vault, in any compartment and any state. Callers
// hold mu.
func (m *Mock) soleVaultLocked() (string, error) {
	vaults := m.vaults.SortedValues()

	switch len(vaults) {
	case 0:
		return "", cerrors.New(cerrors.InvalidArgument, "no vault exists to serve this key request")
	case 1:
		return vaults[0].ID, nil
	default:
		return "", cerrors.New(cerrors.InvalidArgument,
			"several vaults share this address, so it cannot tell which vault a key request is for: "+
				"start serve with --oci-kms-endpoint-domain to give each vault its own managementEndpoint host "+
				"(raw HTTP callers may pass ?vaultId= instead)")
	}
}

// deletionTime validates a caller-supplied deletion time against OCI's window
// and returns it, defaulting to the far end when the caller names none.
func (m *Mock) deletionTime(at string, minDays int) (string, error) {
	now := m.opts.Clock.Now().UTC()

	if at == "" {
		return now.Add(maxDeletionDays * day).Format(timeFormat), nil
	}

	when, err := time.Parse(timeFormat, at)
	if err != nil {
		return "", cerrors.Newf(cerrors.InvalidArgument, "timeOfDeletion %q is not an RFC3339 timestamp", at)
	}

	when = when.UTC()

	if when.Before(now.Add(time.Duration(minDays)*day)) || when.After(now.Add(maxDeletionDays*day)) {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"timeOfDeletion must fall between %d and %d days from now", minDays, maxDeletionDays)
	}

	return when.Format(timeFormat), nil
}

// earliestDeletion is the soonest OCI permits a resource to be deleted, which
// is what the portable delete asks for.
func (m *Mock) earliestDeletion(minDays int) string {
	return m.opts.Clock.Now().UTC().Add(time.Duration(minDays) * day).Format(timeFormat)
}

// compartmentOr falls back to the configured default compartment.
func (m *Mock) compartmentOr(compartmentID string) string {
	if compartmentID == "" {
		return m.opts.CompartmentID
	}

	return compartmentID
}

// endpointLabelLen is how much of an OCID's unique suffix a vault's endpoint
// label keeps; real OCI labels are short too.
const endpointLabelLen = 13

// EndpointLabel returns the short host label a vault's endpoints carry: the
// tail of its OCID's unique suffix.
func EndpointLabel(vaultID string) string {
	suffix := vaultID[strings.LastIndex(vaultID, ".")+1:]
	if len(suffix) > endpointLabelLen {
		suffix = suffix[len(suffix)-endpointLabelLen:]
	}

	return strings.ToLower(suffix)
}

// VaultIDByLabel resolves an endpoint label back to its vault.
func (m *Mock) VaultIDByLabel(label string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, v := range m.vaults.SortedValues() {
		if EndpointLabel(v.ID) == label {
			return v.ID, true
		}
	}

	return "", false
}

// toVaultInfo projects stored vault state.
func (*Mock) toVaultInfo(v *vaultData) VaultInfo {
	return VaultInfo{
		ID:             v.ID,
		CompartmentID:  v.Scope.Compartment,
		DisplayName:    v.DisplayName,
		VaultType:      v.VaultType,
		WrappingKeyID:  v.WrappingKeyID,
		EndpointLabel:  EndpointLabel(v.ID),
		LifecycleState: v.LifecycleState,
		TimeCreated:    v.TimeCreated,
		TimeOfDeletion: v.TimeOfDeletion,
		FreeformTags:   copyTags(v.FreeformTags),
	}
}

// copyTags returns an independent copy of a tag map, never nil.
func copyTags(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}
