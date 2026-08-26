package vault

import (
	"slices"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// Key protection modes.
const (
	ProtectionModeHSM      = "HSM"
	ProtectionModeSoftware = "SOFTWARE"
)

// Key algorithms.
const (
	AlgorithmAES   = "AES"
	AlgorithmRSA   = "RSA"
	AlgorithmECDSA = "ECDSA"
)

// Permitted key lengths in bytes, per algorithm.
//
//nolint:gochecknoglobals // a lookup table, read-only after init.
var keyLengths = map[string][]int{
	AlgorithmAES:   {16, 24, 32},
	AlgorithmRSA:   {256, 384, 512},
	AlgorithmECDSA: {32, 48, 66},
}

// KeyShape is the algorithm and size of a master encryption key.
type KeyShape struct {
	Algorithm string
	Length    int
	// CurveID names the curve an ECDSA key uses; empty for AES and RSA.
	CurveID string
}

// KeySpec describes a master encryption key to create.
type KeySpec struct {
	CompartmentID string
	VaultID       string
	DisplayName   string
	Shape         KeyShape
	// ProtectionMode is HSM or SOFTWARE; empty means HSM, OCI's default.
	ProtectionMode string
	FreeformTags   map[string]string
}

// KeyInfo describes a master encryption key.
type KeyInfo struct {
	ID                string
	CompartmentID     string
	VaultID           string
	DisplayName       string
	Shape             KeyShape
	ProtectionMode    string
	LifecycleState    string
	CurrentKeyVersion string
	TimeCreated       string
	TimeOfDeletion    string
	FreeformTags      map[string]string
}

// KeyVersionInfo describes one version of a master encryption key. Rotating a
// key is creating a new version of it.
type KeyVersionInfo struct {
	ID             string
	KeyID          string
	VaultID        string
	CompartmentID  string
	LifecycleState string
	TimeCreated    string
}

type keyData struct {
	ID             string
	VaultID        string
	DisplayName    string
	Shape          KeyShape
	ProtectionMode string
	LifecycleState string
	TimeCreated    string
	TimeOfDeletion string
	CurrentVersion string
	Scope          scope.Scope
	FreeformTags   map[string]string
}

type keyVersionData struct {
	ID          string
	KeyID       string
	VaultID     string
	TimeCreated string
	Scope       scope.Scope
}

// CreateKey creates a master encryption key in a vault, along with its first
// key version.
func (m *Mock) CreateKey(spec *KeySpec) (*KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if spec.DisplayName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "displayName is required")
	}

	if spec.VaultID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "vaultId is required")
	}

	if err := m.requireActiveVaultLocked(spec.VaultID); err != nil {
		return nil, err
	}

	mode, err := protectionMode(spec.ProtectionMode)
	if err != nil {
		return nil, err
	}

	if err := validateShape(spec.Shape); err != nil {
		return nil, err
	}

	k := m.newKeyLocked(spec, mode)

	info := toKeyInfo(k)

	return &info, nil
}

// newKeyLocked stores a key built from spec and mints its first version.
func (m *Mock) newKeyLocked(spec *KeySpec, mode string) *keyData {
	id := m.newOCID(typeKey)
	k := &keyData{
		ID:             id,
		VaultID:        spec.VaultID,
		DisplayName:    spec.DisplayName,
		Shape:          spec.Shape,
		ProtectionMode: mode,
		LifecycleState: StateActive,
		TimeCreated:    m.now(),
		Scope:          scope.Scope{Compartment: m.compartmentOr(spec.CompartmentID)},
		FreeformTags:   copyTags(spec.FreeformTags),
	}

	m.keys.Set(id, k)
	k.CurrentVersion = m.newKeyVersionLocked(k).ID

	return k
}

// newKeyVersionLocked mints a key version and points the key at it.
func (m *Mock) newKeyVersionLocked(k *keyData) *keyVersionData {
	kv := &keyVersionData{
		ID:          m.newOCID(typeKeyVersion),
		KeyID:       k.ID,
		VaultID:     k.VaultID,
		TimeCreated: m.now(),
		Scope:       k.Scope,
	}

	m.keyVersions.Set(kv.ID, kv)

	return kv
}

// GetKey returns a key by OCID.
func (m *Mock) GetKey(id string) (*KeyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	k, err := m.keyLocked(id)
	if err != nil {
		return nil, err
	}

	info := toKeyInfo(k)

	return &info, nil
}

// ListKeys returns the keys in a compartment, further filtered to one vault
// when vaultID is non-empty, ordered by OCID.
func (m *Mock) ListKeys(compartmentID, vaultID string) ([]KeyInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	out := make([]KeyInfo, 0, m.keys.Len())

	for _, k := range m.keys.SortedValues() {
		if !k.Scope.Matches(filter) || (vaultID != "" && k.VaultID != vaultID) {
			continue
		}

		out = append(out, toKeyInfo(k))
	}

	return out, nil
}

// UpdateKey replaces a key's display name and freeform tags.
func (m *Mock) UpdateKey(id string, upd Update) (*KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, err := m.keyLocked(id)
	if err != nil {
		return nil, err
	}

	if upd.DisplayName != nil {
		k.DisplayName = *upd.DisplayName
	}

	if upd.FreeformTags != nil {
		k.FreeformTags = copyTags(upd.FreeformTags)
	}

	info := toKeyInfo(k)

	return &info, nil
}

// ScheduleKeyDeletion marks a key for deletion at the given time, which must
// fall between 7 and 30 days out.
func (m *Mock) ScheduleKeyDeletion(id, at string) (*KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, err := m.keyLocked(id)
	if err != nil {
		return nil, err
	}

	if k.LifecycleState == StatePendingDeletion {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "key %s is already scheduled for deletion", id)
	}

	when, err := m.deletionTime(at, minKeyDeletionDays)
	if err != nil {
		return nil, err
	}

	k.LifecycleState = StatePendingDeletion
	k.TimeOfDeletion = when

	info := toKeyInfo(k)

	return &info, nil
}

// CancelKeyDeletion returns a key scheduled for deletion to ACTIVE.
func (m *Mock) CancelKeyDeletion(id string) (*KeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, err := m.keyLocked(id)
	if err != nil {
		return nil, err
	}

	if k.LifecycleState != StatePendingDeletion {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "key %s is not scheduled for deletion", id)
	}

	k.LifecycleState = StateActive
	k.TimeOfDeletion = ""

	info := toKeyInfo(k)

	return &info, nil
}

// ChangeKeyCompartment moves a key to another compartment.
func (m *Mock) ChangeKeyCompartment(id, compartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	k, err := m.keyLocked(id)
	if err != nil {
		return err
	}

	k.Scope = scope.Scope{Compartment: compartmentID}

	return nil
}

// KeyCompartment returns the compartment a key lives in, for the work request
// the wire layer records.
func (m *Mock) KeyCompartment(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	k, ok := m.keys.Get(id)
	if !ok {
		return ""
	}

	return k.Scope.Compartment
}

// CreateKeyVersion rotates a key: it mints a new version and makes it the
// key's current one. Earlier versions stay readable, as in real OCI.
func (m *Mock) CreateKeyVersion(keyID string) (*KeyVersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, err := m.keyLocked(keyID)
	if err != nil {
		return nil, err
	}

	if k.LifecycleState != StateActive {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "key %s is %s", keyID, k.LifecycleState)
	}

	kv := m.newKeyVersionLocked(k)
	k.CurrentVersion = kv.ID

	info := toKeyVersionInfo(kv)

	return &info, nil
}

// GetKeyVersion returns one version of a key.
func (m *Mock) GetKeyVersion(keyID, versionID string) (*KeyVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.keyLocked(keyID); err != nil {
		return nil, err
	}

	kv, ok := m.keyVersions.Get(versionID)
	if !ok || kv.KeyID != keyID {
		return nil, cerrors.Newf(cerrors.NotFound, "key version %s not found for key %s", versionID, keyID)
	}

	info := toKeyVersionInfo(kv)

	return &info, nil
}

// ListKeyVersions returns every version of a key, oldest first. Real OCI takes
// no compartmentId here — the key already names one.
func (m *Mock) ListKeyVersions(keyID string) ([]KeyVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, err := m.keyLocked(keyID); err != nil {
		return nil, err
	}

	out := make([]KeyVersionInfo, 0, m.keyVersions.Len())

	for _, kv := range m.keyVersions.SortedValues() {
		if kv.KeyID != keyID {
			continue
		}

		out = append(out, toKeyVersionInfo(kv))
	}

	return out, nil
}

// keyLocked reads a key, reporting OCI's not-found for an unknown OCID.
func (m *Mock) keyLocked(id string) (*keyData, error) {
	k, ok := m.keys.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "key %s not found", id)
	}

	return k, nil
}

// protectionMode validates the mode a key is stored under.
func protectionMode(mode string) (string, error) {
	switch mode {
	case "":
		return ProtectionModeHSM, nil
	case ProtectionModeHSM, ProtectionModeSoftware:
		return mode, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"protectionMode %q is not one of %s, %s", mode, ProtectionModeHSM, ProtectionModeSoftware)
	}
}

// validateShape checks a key's algorithm, length and curve.
func validateShape(shape KeyShape) error {
	lengths, ok := keyLengths[shape.Algorithm]
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument,
			"keyShape.algorithm %q is not one of %s, %s, %s",
			shape.Algorithm, AlgorithmAES, AlgorithmRSA, AlgorithmECDSA)
	}

	if !slices.Contains(lengths, shape.Length) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"keyShape.length %d is not one of %v for algorithm %s", shape.Length, lengths, shape.Algorithm)
	}

	if shape.Algorithm == AlgorithmECDSA && shape.CurveID == "" {
		return cerrors.New(cerrors.InvalidArgument, "keyShape.curveId is required for an ECDSA key")
	}

	if shape.Algorithm != AlgorithmECDSA && shape.CurveID != "" {
		return cerrors.Newf(cerrors.InvalidArgument, "keyShape.curveId does not apply to a %s key", shape.Algorithm)
	}

	return nil
}

func toKeyInfo(k *keyData) KeyInfo {
	return KeyInfo{
		ID:                k.ID,
		CompartmentID:     k.Scope.Compartment,
		VaultID:           k.VaultID,
		DisplayName:       k.DisplayName,
		Shape:             k.Shape,
		ProtectionMode:    k.ProtectionMode,
		LifecycleState:    k.LifecycleState,
		CurrentKeyVersion: k.CurrentVersion,
		TimeCreated:       k.TimeCreated,
		TimeOfDeletion:    k.TimeOfDeletion,
		FreeformTags:      copyTags(k.FreeformTags),
	}
}

func toKeyVersionInfo(kv *keyVersionData) KeyVersionInfo {
	return KeyVersionInfo{
		ID:             kv.ID,
		KeyID:          kv.KeyID,
		VaultID:        kv.VaultID,
		CompartmentID:  kv.Scope.Compartment,
		LifecycleState: StateActive,
		TimeCreated:    kv.TimeCreated,
	}
}
