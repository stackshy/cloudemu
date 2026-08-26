package vault

import (
	"slices"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Secret version stages. A version carries several at once: the newest is
// always LATEST, and CURRENT is the one a bundle read resolves to by default.
const (
	StageCurrent    = "CURRENT"
	StagePending    = "PENDING"
	StageLatest     = "LATEST"
	StagePrevious   = "PREVIOUS"
	StageDeprecated = "DEPRECATED"
)

// stageOrder is the order stages are reported in, so a version's stage list is
// deterministic.
//
//nolint:gochecknoglobals // a lookup table, read-only after init.
var stageOrder = []string{StageCurrent, StagePending, StageLatest, StagePrevious, StageDeprecated}

// SecretVersionInfo describes one version of a secret, without its content.
type SecretVersionInfo struct {
	SecretID       string
	VersionNumber  int64
	Name           string
	Stages         []string
	TimeCreated    string
	TimeOfDeletion string
}

// SecretBundle is a secret version together with its content, which is what
// the secret-retrieval data plane serves.
type SecretBundle struct {
	SecretID       string
	VersionNumber  int64
	VersionName    string
	Stages         []string
	Content        []byte
	TimeCreated    string
	TimeOfDeletion string
}

// BundleSelector picks the version a bundle read returns. At most one field
// may be set; none means the CURRENT version.
type BundleSelector struct {
	VersionNumber *int64
	VersionName   string
	Stage         string
}

type versionData struct {
	Number         int64
	Name           string
	Content        []byte
	Stages         []string
	TimeCreated    string
	TimeOfDeletion string
}

// ListOCISecretVersions returns every version of a secret, oldest first. Real
// OCI takes no compartmentId here — the secret already names one.
func (m *Mock) ListOCISecretVersions(secretID string) ([]SecretVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretLocked(secretID)
	if err != nil {
		return nil, err
	}

	out := make([]SecretVersionInfo, 0, len(s.Versions))
	for _, v := range s.Versions {
		out = append(out, toVersionInfo(s.ID, v))
	}

	return out, nil
}

// GetOCISecretVersion returns one version of a secret, without its content.
func (m *Mock) GetOCISecretVersion(secretID string, number int64) (*SecretVersionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretLocked(secretID)
	if err != nil {
		return nil, err
	}

	v, err := versionByNumber(s, number)
	if err != nil {
		return nil, err
	}

	info := toVersionInfo(s.ID, v)

	return &info, nil
}

// ScheduleSecretVersionDeletion marks one version for deletion. OCI refuses to
// schedule the CURRENT version, which would leave the secret unreadable.
func (m *Mock) ScheduleSecretVersionDeletion(secretID string, number int64, at string) (*SecretVersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.secretLocked(secretID)
	if err != nil {
		return nil, err
	}

	v, err := versionByNumber(s, number)
	if err != nil {
		return nil, err
	}

	if slices.Contains(v.Stages, StageCurrent) {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"version %d is the CURRENT version of secret %s", number, secretID)
	}

	if v.TimeOfDeletion != "" {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"version %d of secret %s is already scheduled for deletion", number, secretID)
	}

	when, err := m.deletionTime(at, minSecretDeletionDays)
	if err != nil {
		return nil, err
	}

	v.TimeOfDeletion = when

	info := toVersionInfo(s.ID, v)

	return &info, nil
}

// CancelSecretVersionDeletion clears a version's scheduled deletion.
func (m *Mock) CancelSecretVersionDeletion(secretID string, number int64) (*SecretVersionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, err := m.secretLocked(secretID)
	if err != nil {
		return nil, err
	}

	v, err := versionByNumber(s, number)
	if err != nil {
		return nil, err
	}

	if v.TimeOfDeletion == "" {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"version %d of secret %s is not scheduled for deletion", number, secretID)
	}

	v.TimeOfDeletion = ""

	info := toVersionInfo(s.ID, v)

	return &info, nil
}

// GetSecretBundle returns the content of the version sel picks.
func (m *Mock) GetSecretBundle(secretID string, sel BundleSelector) (*SecretBundle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretLocked(secretID)
	if err != nil {
		return nil, err
	}

	return selectBundle(s, sel)
}

// GetSecretBundleByName returns a bundle addressed by vault and secret name,
// which is how the data plane's getByName action addresses a secret.
func (m *Mock) GetSecretBundleByName(vaultID, name string, sel BundleSelector) (*SecretBundle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, err := m.secretByVaultAndNameLocked(vaultID, name)
	if err != nil {
		return nil, err
	}

	return selectBundle(s, sel)
}

// ListSecretBundleVersions returns the versions the data plane can serve,
// oldest first.
func (m *Mock) ListSecretBundleVersions(secretID string) ([]SecretVersionInfo, error) {
	return m.ListOCISecretVersions(secretID)
}

// selectBundle resolves sel against a secret's versions.
func selectBundle(s *secretData, sel BundleSelector) (*SecretBundle, error) {
	v, err := selectVersion(s, sel)
	if err != nil {
		return nil, err
	}

	return &SecretBundle{
		SecretID:       s.ID,
		VersionNumber:  v.Number,
		VersionName:    v.Name,
		Stages:         slices.Clone(v.Stages),
		Content:        slices.Clone(v.Content),
		TimeCreated:    v.TimeCreated,
		TimeOfDeletion: v.TimeOfDeletion,
	}, nil
}

// selectVersion picks the version a selector names, rejecting a selector that
// names more than one way to find it.
func selectVersion(s *secretData, sel BundleSelector) (*versionData, error) {
	given := 0
	if sel.VersionNumber != nil {
		given++
	}

	if sel.VersionName != "" {
		given++
	}

	if sel.Stage != "" {
		given++
	}

	if given > 1 {
		return nil, cerrors.New(cerrors.InvalidArgument,
			"at most one of versionNumber, secretVersionName and stage may be given")
	}

	switch {
	case sel.VersionNumber != nil:
		return versionByNumber(s, *sel.VersionNumber)
	case sel.VersionName != "":
		return versionByName(s, sel.VersionName)
	case sel.Stage != "":
		return versionByStage(s, sel.Stage)
	default:
		return versionByStage(s, StageCurrent)
	}
}

func versionByNumber(s *secretData, number int64) (*versionData, error) {
	for _, v := range s.Versions {
		if v.Number == number {
			return v, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "version %d of secret %s not found", number, s.ID)
}

func versionByName(s *secretData, name string) (*versionData, error) {
	for _, v := range s.Versions {
		if v.Name != "" && v.Name == name {
			return v, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "version %q of secret %s not found", name, s.ID)
}

func versionByStage(s *secretData, stage string) (*versionData, error) {
	if !slices.Contains(stageOrder, stage) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "stage %q is not one of %v", stage, stageOrder)
	}

	for _, v := range s.Versions {
		if slices.Contains(v.Stages, stage) {
			return v, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "secret %s has no version in stage %s", s.ID, stage)
}

// addVersionLocked appends a version and restages the ones already there.
func (m *Mock) addVersionLocked(s *secretData, content []byte, name, stage string) *versionData {
	v := &versionData{
		Number:      s.NextVersion,
		Name:        name,
		Content:     slices.Clone(content),
		TimeCreated: m.now(),
	}
	s.NextVersion++

	// LATEST always follows the newest version.
	for _, ex := range s.Versions {
		ex.Stages = withoutStage(ex.Stages, StageLatest)
	}

	if stage == StagePending {
		demotePending(s)

		v.Stages = []string{StagePending, StageLatest}
	} else {
		demoteCurrent(s)

		v.Stages = []string{StageCurrent, StageLatest}
		s.CurrentVersion = v.Number
	}

	s.Versions = append(s.Versions, v)
	s.TimeUpdated = v.TimeCreated

	return v
}

// promoteVersion makes an existing version the CURRENT one, which is how OCI
// finishes a rotation staged as PENDING.
func promoteVersion(s *secretData, number int64) error {
	v, err := versionByNumber(s, number)
	if err != nil {
		return err
	}

	if v.TimeOfDeletion != "" {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"version %d of secret %s is scheduled for deletion", number, s.ID)
	}

	if slices.Contains(v.Stages, StageCurrent) {
		return nil
	}

	latest := slices.Contains(v.Stages, StageLatest)

	demoteCurrent(s)

	v.Stages = []string{StageCurrent}
	if latest {
		v.Stages = append(v.Stages, StageLatest)
	}

	s.CurrentVersion = number

	return nil
}

// demoteCurrent slides the stage ladder down one rung: CURRENT becomes
// PREVIOUS and PREVIOUS becomes DEPRECATED.
func demoteCurrent(s *secretData) {
	for _, ex := range s.Versions {
		switch {
		case slices.Contains(ex.Stages, StageCurrent):
			ex.Stages = restage(ex.Stages, StageCurrent, StagePrevious)
		case slices.Contains(ex.Stages, StagePrevious):
			ex.Stages = restage(ex.Stages, StagePrevious, StageDeprecated)
		}
	}
}

// demotePending deprecates the version already staged PENDING; OCI holds at
// most one.
func demotePending(s *secretData) {
	for _, ex := range s.Versions {
		if slices.Contains(ex.Stages, StagePending) {
			ex.Stages = restage(ex.Stages, StagePending, StageDeprecated)
		}
	}
}

// newVersionStage validates the stage a newly written version enters. OCI
// admits only CURRENT and PENDING there; the rest are reached by being
// displaced.
func newVersionStage(stage string) (string, error) {
	switch stage {
	case "", StageCurrent:
		return StageCurrent, nil
	case StagePending:
		return StagePending, nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"a new version may enter stage %s or %s, not %q", StageCurrent, StagePending, stage)
	}
}

// versionNameTaken reports whether a secret already has a version by this name.
func versionNameTaken(s *secretData, name string) bool {
	if name == "" {
		return false
	}

	for _, v := range s.Versions {
		if v.Name == name {
			return true
		}
	}

	return false
}

// restage swaps one stage for another, keeping the rest in canonical order.
func restage(stages []string, from, to string) []string {
	next := withoutStage(stages, from)

	return sortStages(append(next, to))
}

// withoutStage removes a stage from a version's list.
func withoutStage(stages []string, stage string) []string {
	out := make([]string, 0, len(stages))

	for _, s := range stages {
		if s != stage {
			out = append(out, s)
		}
	}

	return out
}

// sortStages puts a stage list in canonical order.
func sortStages(stages []string) []string {
	out := make([]string, 0, len(stages))

	for _, want := range stageOrder {
		if slices.Contains(stages, want) {
			out = append(out, want)
		}
	}

	return out
}

func toVersionInfo(secretID string, v *versionData) SecretVersionInfo {
	return SecretVersionInfo{
		SecretID:       secretID,
		VersionNumber:  v.Number,
		Name:           v.Name,
		Stages:         slices.Clone(v.Stages),
		TimeCreated:    v.TimeCreated,
		TimeOfDeletion: v.TimeOfDeletion,
	}
}
