package vault

import (
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// SecretSpec describes a secret to create in a vault.
type SecretSpec struct {
	CompartmentID string
	VaultID       string
	// KeyID is the master encryption key the secret is encrypted under. OCI
	// requires one; the portable driver supplies the default vault's key.
	KeyID       string
	Name        string
	Description string
	Content     []byte
	// ContentName labels the first version; OCI's secretContent.name.
	ContentName  string
	FreeformTags map[string]string
}

// SecretUpdate carries the mutable fields of a secret. A nil field leaves the
// stored value alone.
type SecretUpdate struct {
	Description *string
	KeyID       string
	// CurrentVersionNumber promotes an existing version to CURRENT, which is
	// how OCI finishes a rotation staged as PENDING.
	CurrentVersionNumber *int64
	// Content adds a new version, entering Stage.
	Content     []byte
	ContentName string
	Stage       string
	// ContentGiven distinguishes an update carrying empty content from one
	// carrying none, so an empty secret value is storable.
	ContentGiven bool
	FreeformTags map[string]string
}

// SecretInfo describes a secret as the OCI Vault API reports it.
type SecretInfo struct {
	ID                   string
	CompartmentID        string
	VaultID              string
	KeyID                string
	Name                 string
	Description          string
	LifecycleState       string
	CurrentVersionNumber int64
	TimeCreated          string
	TimeOfDeletion       string
	FreeformTags         map[string]string
}

type secretData struct {
	ID             string
	VaultID        string
	KeyID          string
	Name           string
	Description    string
	LifecycleState string
	TimeCreated    string
	TimeUpdated    string
	TimeOfDeletion string
	CurrentVersion int64
	NextVersion    int64
	Versions       []*versionData
	Scope          scope.Scope
	FreeformTags   map[string]string
}

// CreateOCISecret creates a secret and its first version in a vault.
func (m *Mock) CreateOCISecret(spec SecretSpec) (*SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateSecretSpecLocked(spec); err != nil {
		return nil, err
	}

	s := m.newSecretLocked(spec)

	info := toSecretInfo(s)

	return &info, nil
}

// validateSecretSpecLocked checks a create against the vault, the key and the
// names already taken.
func (m *Mock) validateSecretSpecLocked(spec SecretSpec) error {
	if spec.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "secretName is required")
	}

	if spec.VaultID == "" {
		return cerrors.New(cerrors.InvalidArgument, "vaultId is required")
	}

	if spec.KeyID == "" {
		return cerrors.New(cerrors.InvalidArgument, "keyId is required")
	}

	if _, err := m.activeVaultLocked(spec.VaultID); err != nil {
		return err
	}

	k, err := m.keyLocked(spec.KeyID)
	if err != nil {
		return err
	}

	if k.VaultID != spec.VaultID {
		return cerrors.Newf(cerrors.InvalidArgument, "key %s does not belong to vault %s", spec.KeyID, spec.VaultID)
	}

	if _, ok := m.secretByNameLocked(spec.Name); ok {
		return cerrors.Newf(cerrors.AlreadyExists, "secret %q already exists", spec.Name)
	}

	return nil
}

// newSecretLocked stores a secret built from spec, with its first version
// staged CURRENT.
func (m *Mock) newSecretLocked(spec SecretSpec) *secretData {
	id := m.newOCID(typeSecret)
	s := &secretData{
		ID:             id,
		VaultID:        spec.VaultID,
		KeyID:          spec.KeyID,
		Name:           spec.Name,
		Description:    spec.Description,
		LifecycleState: StateActive,
		TimeCreated:    m.now(),
		TimeUpdated:    m.now(),
		NextVersion:    1,
		Scope:          scope.Scope{Compartment: m.compartmentOr(spec.CompartmentID)},
		FreeformTags:   copyTags(spec.FreeformTags),
	}

	m.secrets.Set(id, s)
	m.addVersionLocked(s, spec.Content, spec.ContentName, StageCurrent)

	return s
}

// GetOCISecret returns a secret by OCID.
func (m *Mock) GetOCISecret(id string) (*SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretLocked(id)
	if err != nil {
		return nil, err
	}

	info := toSecretInfo(s)

	return &info, nil
}

// GetOCISecretByName returns a secret by vault and name, OCI's getByName
// action. It takes no compartmentId: the vault already names one.
func (m *Mock) GetOCISecretByName(vaultID, name string) (*SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretByVaultAndNameLocked(vaultID, name)
	if err != nil {
		return nil, err
	}

	info := toSecretInfo(s)

	return &info, nil
}

// ListOCISecrets returns the secrets in a compartment, further filtered to one
// vault and one name when those are non-empty, ordered by OCID.
func (m *Mock) ListOCISecrets(compartmentID, vaultID, name string) ([]SecretInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filter := scope.Scope{Compartment: compartmentID}
	out := make([]SecretInfo, 0, m.secrets.Len())

	for _, s := range m.secrets.SortedValues() {
		if !s.Scope.Matches(filter) {
			continue
		}

		if (vaultID != "" && s.VaultID != vaultID) || (name != "" && s.Name != name) {
			continue
		}

		out = append(out, toSecretInfo(s))
	}

	return out, nil
}

// UpdateOCISecret applies a secret update: a new description or tag set, a
// re-key, a new version, or the promotion of an existing version to CURRENT.
func (m *Mock) UpdateOCISecret(id string, upd SecretUpdate) (*SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.secretLocked(id)
	if err != nil {
		return nil, err
	}

	if s.LifecycleState != StateActive {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "secret %s is %s", id, s.LifecycleState)
	}

	if err := m.rekeyLocked(s, upd.KeyID); err != nil {
		return nil, err
	}

	if err := m.applySecretVersionUpdateLocked(s, upd); err != nil {
		return nil, err
	}

	if upd.Description != nil {
		s.Description = *upd.Description
	}

	if upd.FreeformTags != nil {
		s.FreeformTags = copyTags(upd.FreeformTags)
	}

	s.TimeUpdated = m.now()

	info := toSecretInfo(s)

	return &info, nil
}

// applySecretVersionUpdateLocked adds the version an update carries and
// promotes the one it names.
func (m *Mock) applySecretVersionUpdateLocked(s *secretData, upd SecretUpdate) error {
	if upd.ContentGiven {
		stage, err := newVersionStage(upd.Stage)
		if err != nil {
			return err
		}

		if versionNameTaken(s, upd.ContentName) {
			return cerrors.Newf(cerrors.AlreadyExists,
				"secret %s already has a version named %q", s.ID, upd.ContentName)
		}

		m.addVersionLocked(s, upd.Content, upd.ContentName, stage)
	}

	if upd.CurrentVersionNumber != nil {
		return m.promoteVersionLocked(s, *upd.CurrentVersionNumber)
	}

	return nil
}

// rekeyLocked points a secret at another master encryption key in its vault.
func (m *Mock) rekeyLocked(s *secretData, keyID string) error {
	if keyID == "" || keyID == s.KeyID {
		return nil
	}

	k, err := m.keyLocked(keyID)
	if err != nil {
		return err
	}

	if k.VaultID != s.VaultID {
		return cerrors.Newf(cerrors.InvalidArgument, "key %s does not belong to vault %s", keyID, s.VaultID)
	}

	s.KeyID = keyID

	return nil
}

// ScheduleOCISecretDeletion marks a secret for deletion at the given time,
// which must fall between 1 and 30 days out. An empty time takes the far end,
// as real OCI does. Nothing reaps the secret: it stays PENDING_DELETION, and
// keeps its OCID and versions, until the deletion is cancelled.
func (m *Mock) ScheduleOCISecretDeletion(id, at string) (*SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.secretLocked(id)
	if err != nil {
		return nil, err
	}

	when, err := m.deletionTime(at, minSecretDeletionDays)
	if err != nil {
		return nil, err
	}

	if err := m.scheduleSecretLocked(s, when); err != nil {
		return nil, err
	}

	info := toSecretInfo(s)

	return &info, nil
}

// scheduleSecretLocked moves a secret into PENDING_DELETION.
func (m *Mock) scheduleSecretLocked(s *secretData, when string) error {
	if s.LifecycleState == StatePendingDeletion {
		return cerrors.Newf(cerrors.FailedPrecondition, "secret %s is already scheduled for deletion", s.ID)
	}

	s.LifecycleState = StatePendingDeletion
	s.TimeOfDeletion = when

	return nil
}

// CancelOCISecretDeletion returns a secret scheduled for deletion to ACTIVE.
// It fails if another secret has taken its name in the meantime.
func (m *Mock) CancelOCISecretDeletion(id string) (*SecretInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.secretLocked(id)
	if err != nil {
		return nil, err
	}

	if s.LifecycleState != StatePendingDeletion {
		return nil, cerrors.Newf(cerrors.FailedPrecondition, "secret %s is not scheduled for deletion", id)
	}

	if other, ok := m.secretByNameLocked(s.Name); ok && other.ID != s.ID {
		return nil, cerrors.Newf(cerrors.AlreadyExists,
			"secret %q was recreated as %s while this one was pending deletion", s.Name, other.ID)
	}

	s.LifecycleState = StateActive
	s.TimeOfDeletion = ""

	info := toSecretInfo(s)

	return &info, nil
}

// ChangeSecretCompartment moves a secret to another compartment.
func (m *Mock) ChangeSecretCompartment(id, compartmentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	s, err := m.secretLocked(id)
	if err != nil {
		return err
	}

	s.Scope = scope.Scope{Compartment: compartmentID}

	return nil
}

// SecretCompartment returns the compartment a secret lives in, for the work
// request the wire layer records.
func (m *Mock) SecretCompartment(id string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.secrets.Get(id)
	if !ok {
		return ""
	}

	return s.Scope.Compartment
}

// secretLocked reads a secret, reporting OCI's not-found for an unknown OCID.
func (m *Mock) secretLocked(id string) (*secretData, error) {
	s, ok := m.secrets.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "secret %s not found", id)
	}

	return s, nil
}

// secretByNameLocked finds a live secret by name. A secret pending deletion
// releases its name, so the portable driver can delete and recreate one; the
// pending secret stays reachable by OCID until its deletion is cancelled.
func (m *Mock) secretByNameLocked(name string) (*secretData, bool) {
	for _, s := range m.secrets.SortedValues() {
		if s.Name == name && s.LifecycleState == StateActive {
			return s, true
		}
	}

	return nil, false
}

// secretByVaultAndNameLocked resolves OCI's getByName addressing.
func (m *Mock) secretByVaultAndNameLocked(vaultID, name string) (*secretData, error) {
	if vaultID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "vaultId is required")
	}

	if name == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "secretName is required")
	}

	for _, s := range m.secrets.SortedValues() {
		if s.VaultID == vaultID && s.Name == name {
			return s, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "secret %q not found in vault %s", name, vaultID)
}

func toSecretInfo(s *secretData) SecretInfo {
	return SecretInfo{
		ID:                   s.ID,
		CompartmentID:        s.Scope.Compartment,
		VaultID:              s.VaultID,
		KeyID:                s.KeyID,
		Name:                 s.Name,
		Description:          s.Description,
		LifecycleState:       s.LifecycleState,
		CurrentVersionNumber: s.CurrentVersion,
		TimeCreated:          s.TimeCreated,
		TimeOfDeletion:       s.TimeOfDeletion,
		FreeformTags:         copyTags(s.FreeformTags),
	}
}
