package kms

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	// defaultDestroyScheduledDuration is the DESTROY_SCHEDULED dwell time a
	// CryptoKey carries when create omits destroyScheduledDuration (24h).
	defaultDestroyScheduledDuration = "86400s"
	// purposeEncryptDecrypt is the only purpose whose keys carry a primary
	// version (cryptoKeys.encrypt uses it); every other purpose omits primary.
	purposeEncryptDecrypt = "ENCRYPT_DECRYPT"
	purposeUnspecified    = "CRYPTO_KEY_PURPOSE_UNSPECIFIED"
	// defaultProtectionLevel is applied when a versionTemplate omits it.
	defaultProtectionLevel = "SOFTWARE"

	stateEnabled          = "ENABLED"
	stateDisabled         = "DISABLED"
	stateDestroyed        = "DESTROYED"
	stateDestroyScheduled = "DESTROY_SCHEDULED"
)

// keyRingModel is a keyRing plus its crypto keys and IAM policy.
type keyRingModel struct {
	name       string
	createTime time.Time
	cryptoKeys map[string]*cryptoKeyModel // keyed by short crypto-key id
	iam        iamState
}

// cryptoKeyModel is a crypto key plus its versions.
type cryptoKeyModel struct {
	name                     string
	purpose                  string
	createTime               time.Time
	nextRotationTime         string
	rotationPeriod           string
	protectionLevel          string
	algorithm                string
	labels                   map[string]string
	importOnly               bool
	destroyScheduledDuration string
	cryptoKeyBackend         string
	primaryID                string
	versions                 map[string]*versionModel // keyed by short version id
	nextVersion              int
	iam                      iamState
}

// versionModel is a single crypto-key version.
type versionModel struct {
	id               string
	state            string
	protectionLevel  string
	algorithm        string
	createTime       time.Time
	destroyTime      string
	destroyEventTime string
}

// store is the in-memory Cloud KMS control-plane backing state. Cloud KMS has
// no portable driver in cloudemu, so — like the project-IAM and Cloud Billing
// handlers — the handler owns its state here, keyed by full resource name.
type store struct {
	mu       sync.RWMutex
	clock    config.Clock
	keyRings map[string]*keyRingModel // keyed by full keyRing resource name
}

func newStore(clock config.Clock) *store {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &store{clock: clock, keyRings: make(map[string]*keyRingModel)}
}

// --- key ring operations ---

func (s *store) createKeyRing(rt *route) (keyRingJSON, error) {
	name := keyRingName(rt.project, rt.location, rt.keyRing)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.keyRings[name]; ok {
		return keyRingJSON{}, cerrors.Newf(cerrors.AlreadyExists, "KeyRing %s already exists", name)
	}

	kr := &keyRingModel{name: name, createTime: s.clock.Now(), cryptoKeys: map[string]*cryptoKeyModel{}}
	s.keyRings[name] = kr

	return toKeyRingJSON(kr), nil
}

func (s *store) getKeyRing(rt *route) (keyRingJSON, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	kr, err := s.findKeyRing(rt)
	if err != nil {
		return keyRingJSON{}, err
	}

	return toKeyRingJSON(kr), nil
}

func (s *store) listKeyRings(rt *route) listKeyRingsResponse {
	prefix := keyRingName(rt.project, rt.location, "")

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]keyRingJSON, 0)

	for name, kr := range s.keyRings {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, toKeyRingJSON(kr))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return listKeyRingsResponse{KeyRings: out, TotalSize: len(out)}
}

// --- crypto key operations ---

func (s *store) createCryptoKey(rt *route, cfg *cryptoKeyConfig, skipInitialVersion bool) (cryptoKeyJSON, error) {
	name := cryptoKeyName(rt.project, rt.location, rt.keyRing, cfg.id)

	s.mu.Lock()
	defer s.mu.Unlock()

	kr, err := s.findKeyRing(rt)
	if err != nil {
		return cryptoKeyJSON{}, err
	}

	if _, ok := kr.cryptoKeys[cfg.id]; ok {
		return cryptoKeyJSON{}, cerrors.Newf(cerrors.AlreadyExists, "CryptoKey %s already exists", name)
	}

	now := s.clock.Now()

	ck := &cryptoKeyModel{
		name:                     name,
		purpose:                  cfg.purpose,
		createTime:               now,
		rotationPeriod:           cfg.rotationPeriod,
		nextRotationTime:         cfg.nextRotationTime,
		protectionLevel:          cfg.protectionLevel,
		algorithm:                cfg.algorithm,
		labels:                   cfg.labels,
		importOnly:               cfg.importOnly,
		destroyScheduledDuration: cfg.destroyScheduledDuration,
		cryptoKeyBackend:         cfg.cryptoKeyBackend,
		versions:                 map[string]*versionModel{},
		nextVersion:              1,
	}

	// nextRotationTime is derived from the rotation period when a rotation is
	// configured but the caller left the timestamp unset.
	if ck.rotationPeriod != "" && ck.nextRotationTime == "" {
		if d, ok := parseDurationSeconds(ck.rotationPeriod); ok {
			ck.nextRotationTime = rfc3339(now.Add(d))
		}
	}

	if !skipInitialVersion && !cfg.importOnly {
		v := ck.newVersion(now, stateEnabled)
		if ck.purpose == purposeEncryptDecrypt {
			ck.primaryID = v.id
		}
	}

	kr.cryptoKeys[cfg.id] = ck

	return toCryptoKeyJSON(ck), nil
}

func (s *store) getCryptoKey(rt *route) (cryptoKeyJSON, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return cryptoKeyJSON{}, err
	}

	return toCryptoKeyJSON(ck), nil
}

func (s *store) listCryptoKeys(rt *route) (listCryptoKeysResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	kr, err := s.findKeyRing(rt)
	if err != nil {
		return listCryptoKeysResponse{}, err
	}

	out := make([]cryptoKeyJSON, 0, len(kr.cryptoKeys))
	for _, ck := range kr.cryptoKeys {
		out = append(out, toCryptoKeyJSON(ck))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return listCryptoKeysResponse{CryptoKeys: out, TotalSize: len(out)}, nil
}

func (s *store) patchCryptoKey(rt *route, patch *cryptoKeyPatch) (cryptoKeyJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return cryptoKeyJSON{}, err
	}

	if patch.setLabels {
		ck.labels = patch.labels
	}

	if patch.setRotationPeriod {
		ck.rotationPeriod = patch.rotationPeriod
	}

	if patch.setNextRotationTime {
		ck.nextRotationTime = patch.nextRotationTime
	}

	if patch.setProtectionLevel {
		ck.protectionLevel = patch.protectionLevel
	}

	if patch.setAlgorithm {
		ck.algorithm = patch.algorithm
	}

	return toCryptoKeyJSON(ck), nil
}

func (s *store) updatePrimaryVersion(rt *route, versionID string) (cryptoKeyJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return cryptoKeyJSON{}, err
	}

	if ck.purpose != purposeEncryptDecrypt {
		return cryptoKeyJSON{}, cerrors.New(cerrors.InvalidArgument,
			"UpdateCryptoKeyPrimaryVersion is only valid for keys with purpose ENCRYPT_DECRYPT")
	}

	v, ok := ck.versions[versionID]
	if !ok {
		return cryptoKeyJSON{}, cerrors.Newf(cerrors.NotFound, "CryptoKeyVersion %s not found", versionID)
	}

	if v.state != stateEnabled {
		return cryptoKeyJSON{}, cerrors.New(cerrors.FailedPrecondition,
			"the primary version must be ENABLED")
	}

	ck.primaryID = versionID

	return toCryptoKeyJSON(ck), nil
}

// --- version operations ---

func (s *store) createVersion(rt *route, state string) (versionJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return versionJSON{}, err
	}

	if state == "" {
		state = stateEnabled
	}

	v := ck.newVersion(s.clock.Now(), state)

	return toVersionJSON(ck.name, v), nil
}

func (s *store) getVersion(rt *route) (versionJSON, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ck, v, err := s.findVersion(rt)
	if err != nil {
		return versionJSON{}, err
	}

	return toVersionJSON(ck.name, v), nil
}

func (s *store) listVersions(rt *route) (listVersionsResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return listVersionsResponse{}, err
	}

	out := make([]versionJSON, 0, len(ck.versions))
	for _, v := range ck.versions {
		out = append(out, toVersionJSON(ck.name, v))
	}

	// Newest-first, matching real Cloud KMS list ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })

	return listVersionsResponse{CryptoKeyVersions: out, TotalSize: len(out)}, nil
}

func (s *store) patchVersion(rt *route, state string) (versionJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, v, err := s.findVersion(rt)
	if err != nil {
		return versionJSON{}, err
	}

	// Only ENABLED<->DISABLED are user-settable via patch.
	switch state {
	case stateEnabled, stateDisabled:
		if v.state != stateEnabled && v.state != stateDisabled {
			return versionJSON{}, cerrors.Newf(cerrors.FailedPrecondition,
				"cannot move version from %s to %s", v.state, state)
		}

		v.state = state
	case "":
		// no-op: state not in mask
	default:
		return versionJSON{}, cerrors.Newf(cerrors.InvalidArgument, "state %s is not user-settable", state)
	}

	return toVersionJSON(ck.name, v), nil
}

func (s *store) destroyVersion(rt *route) (versionJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, v, err := s.findVersion(rt)
	if err != nil {
		return versionJSON{}, err
	}

	if v.state != stateEnabled && v.state != stateDisabled {
		return versionJSON{}, cerrors.Newf(cerrors.FailedPrecondition,
			"CryptoKeyVersion in state %s cannot be destroyed", v.state)
	}

	now := s.clock.Now()
	v.state = stateDestroyScheduled

	if d, ok := parseDurationSeconds(ck.destroyScheduledDuration); ok {
		v.destroyTime = rfc3339(now.Add(d))
	} else {
		v.destroyTime = rfc3339(now)
	}

	if ck.primaryID == v.id {
		ck.primaryID = ""
	}

	return toVersionJSON(ck.name, v), nil
}

func (s *store) restoreVersion(rt *route) (versionJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, v, err := s.findVersion(rt)
	if err != nil {
		return versionJSON{}, err
	}

	if v.state != stateDestroyScheduled {
		return versionJSON{}, cerrors.Newf(cerrors.FailedPrecondition,
			"only a DESTROY_SCHEDULED version can be restored, not one in state %s", v.state)
	}

	v.state = stateDisabled
	v.destroyTime = ""

	return toVersionJSON(ck.name, v), nil
}

// --- internal lookups (callers hold s.mu) ---

func (s *store) findKeyRing(rt *route) (*keyRingModel, error) {
	kr, ok := s.keyRings[keyRingName(rt.project, rt.location, rt.keyRing)]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "KeyRing %s not found", rt.keyRing)
	}

	return kr, nil
}

func (s *store) findCryptoKey(rt *route) (*cryptoKeyModel, error) {
	kr, err := s.findKeyRing(rt)
	if err != nil {
		return nil, err
	}

	ck, ok := kr.cryptoKeys[rt.cryptoKey]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "CryptoKey %s not found", rt.cryptoKey)
	}

	return ck, nil
}

func (s *store) findVersion(rt *route) (*cryptoKeyModel, *versionModel, error) {
	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return nil, nil, err
	}

	v, ok := ck.versions[rt.version]
	if !ok {
		return nil, nil, cerrors.Newf(cerrors.NotFound, "CryptoKeyVersion %s not found", rt.version)
	}

	return ck, v, nil
}

// newVersion appends a fresh ENABLED-by-default version to the crypto key and
// returns it. Callers hold s.mu.
func (ck *cryptoKeyModel) newVersion(now time.Time, state string) *versionModel {
	id := strconv.Itoa(ck.nextVersion)
	ck.nextVersion++

	v := &versionModel{
		id:              id,
		state:           state,
		protectionLevel: ck.protectionLevel,
		algorithm:       ck.algorithm,
		createTime:      now,
	}
	ck.versions[id] = v

	return v
}
