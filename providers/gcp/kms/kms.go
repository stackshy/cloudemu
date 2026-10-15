// Package kms is the in-memory Cloud KMS (cloudkms.googleapis.com v1) mock:
// key rings, crypto keys and crypto-key versions, plus each version's real key
// material (AES and HMAC secrets, RSA/EC/Ed25519 private keys as PKCS#8 DER).
// All state lives in memstores and is Snapshottable, so serve --persist keeps
// keys and old ciphertexts stay decryptable across a restart.
package kms

import (
	"maps"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
)

const (
	// PurposeEncryptDecrypt is the only purpose whose keys carry a primary
	// version (cryptoKeys.encrypt uses it).
	PurposeEncryptDecrypt = "ENCRYPT_DECRYPT"

	StateEnabled          = "ENABLED"
	StateDisabled         = "DISABLED"
	StateDestroyed        = "DESTROYED"
	StateDestroyScheduled = "DESTROY_SCHEDULED"
)

// Ref addresses a key ring, crypto key or version by its path ids.
type Ref struct {
	Project, Location, KeyRing, CryptoKey, Version string
}

// RingName is the full key ring resource name.
func RingName(r *Ref) string {
	return "projects/" + r.Project + "/locations/" + r.Location + "/keyRings/" + r.KeyRing
}

// KeyName is the full crypto key resource name.
func KeyName(r *Ref) string {
	return RingName(r) + "/cryptoKeys/" + r.CryptoKey
}

// KeyRing is a Cloud KMS key ring.
type KeyRing struct {
	Name       string
	CreateTime time.Time
}

// CryptoKey is a Cloud KMS crypto key. Primary is filled on reads from the
// stored primary version and is not persisted.
type CryptoKey struct {
	Name                     string
	Purpose                  string
	CreateTime               time.Time
	NextRotationTime         string
	RotationPeriod           string
	ProtectionLevel          string
	Algorithm                string
	Labels                   map[string]string
	ImportOnly               bool
	DestroyScheduledDuration string
	CryptoKeyBackend         string
	PrimaryID                string
	NextVersion              int
	Primary                  *Version `json:"-"`
}

// Version is a crypto-key version with its key material. Secret is the AES or
// HMAC key; PrivateKey is a PKCS#8 DER private key for asymmetric algorithms.
// Both are generated on first data-plane use.
type Version struct {
	ID               string
	State            string
	ProtectionLevel  string
	Algorithm        string
	CreateTime       time.Time
	DestroyTime      string
	DestroyEventTime string
	Secret           []byte `json:",omitempty"`
	PrivateKey       []byte `json:",omitempty"`
}

// KeyConfig is the validated input for creating a crypto key.
type KeyConfig struct {
	ID                       string
	Purpose                  string
	RotationPeriod           string
	NextRotationTime         string
	ProtectionLevel          string
	Algorithm                string
	Labels                   map[string]string
	ImportOnly               bool
	DestroyScheduledDuration string
	CryptoKeyBackend         string
}

// KeyPatch is a crypto key update. Only non-nil fields are applied.
type KeyPatch struct {
	Labels           *map[string]string
	RotationPeriod   *string
	NextRotationTime *string
	ProtectionLevel  *string
	Algorithm        *string
}

// Mock is the Cloud KMS state.
type Mock struct {
	mu       sync.Mutex
	clock    config.Clock
	keyRings *memstore.Store[KeyRing]
	keys     *memstore.Store[CryptoKey]
	versions *memstore.Store[Version]
}

// New returns an empty Cloud KMS mock using opts.Clock (real clock when nil).
func New(opts *config.Options) *Mock {
	var clock config.Clock = config.RealClock{}
	if opts != nil && opts.Clock != nil {
		clock = opts.Clock
	}

	return &Mock{
		clock:    clock,
		keyRings: memstore.New[KeyRing](),
		keys:     memstore.New[CryptoKey](),
		versions: memstore.New[Version](),
	}
}

func notFound(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %s not found", kind, name)
}

// CreateKeyRing creates ref's key ring.
func (m *Mock) CreateKeyRing(ref *Ref) (KeyRing, error) {
	kr := KeyRing{Name: RingName(ref), CreateTime: m.clock.Now()}
	if !m.keyRings.SetIfAbsent(kr.Name, kr) {
		return KeyRing{}, cerrors.Newf(cerrors.AlreadyExists, "KeyRing %s already exists", kr.Name)
	}

	return kr, nil
}

// GetKeyRing returns ref's key ring.
func (m *Mock) GetKeyRing(ref *Ref) (KeyRing, error) {
	kr, ok := m.keyRings.Get(RingName(ref))
	if !ok {
		return KeyRing{}, notFound("KeyRing", ref.KeyRing)
	}

	return kr, nil
}

// ListKeyRings returns the key rings of ref's project and location by name.
func (m *Mock) ListKeyRings(ref *Ref) []KeyRing {
	prefix := strings.TrimSuffix(RingName(ref), ref.KeyRing)

	return sortedByName(m.keyRings.Filter(func(k string, _ KeyRing) bool {
		return strings.HasPrefix(k, prefix)
	}))
}

func sortedByName[V any](in map[string]V) []V {
	names := make([]string, 0, len(in))
	for k := range in {
		names = append(names, k)
	}

	sort.Strings(names)

	out := make([]V, 0, len(names))
	for _, k := range names {
		out = append(out, in[k])
	}

	return out
}

// withPrimary returns a read copy of ck with Primary filled. Callers hold m.mu.
func (m *Mock) withPrimary(in *CryptoKey) CryptoKey {
	ck := *in
	ck.Labels = maps.Clone(ck.Labels)
	ck.Primary = nil

	if ck.PrimaryID != "" {
		if v, ok := m.versions.Get(ck.Name + "/cryptoKeyVersions/" + ck.PrimaryID); ok {
			v.Secret, v.PrivateKey = nil, nil
			ck.Primary = &v
		}
	}

	return ck
}

// CreateCryptoKey creates a crypto key under ref's ring. Unless skipInitial or
// importOnly, version 1 is created ENABLED (and made primary for
// ENCRYPT_DECRYPT), matching real Cloud KMS.
func (m *Mock) CreateCryptoKey(ref *Ref, cfg *KeyConfig, skipInitial bool) (CryptoKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.keyRings.Has(RingName(ref)) {
		return CryptoKey{}, notFound("KeyRing", ref.KeyRing)
	}

	name := RingName(ref) + "/cryptoKeys/" + cfg.ID

	if m.keys.Has(name) {
		return CryptoKey{}, cerrors.Newf(cerrors.AlreadyExists, "CryptoKey %s already exists", name)
	}

	now := m.clock.Now()
	ck := CryptoKey{
		Name: name, Purpose: cfg.Purpose, CreateTime: now,
		RotationPeriod: cfg.RotationPeriod, NextRotationTime: cfg.NextRotationTime,
		ProtectionLevel: cfg.ProtectionLevel, Algorithm: cfg.Algorithm,
		Labels: maps.Clone(cfg.Labels), ImportOnly: cfg.ImportOnly,
		DestroyScheduledDuration: cfg.DestroyScheduledDuration,
		CryptoKeyBackend:         cfg.CryptoKeyBackend, NextVersion: 1,
	}

	// nextRotationTime is derived from the rotation period when a rotation is
	// configured but the caller left the timestamp unset.
	if ck.RotationPeriod != "" && ck.NextRotationTime == "" {
		if d, ok := ParseDurationSeconds(ck.RotationPeriod); ok {
			ck.NextRotationTime = RFC3339(now.Add(d))
		}
	}

	if !skipInitial && !cfg.ImportOnly {
		v := m.newVersion(&ck, now, StateEnabled)
		if ck.Purpose == PurposeEncryptDecrypt {
			ck.PrimaryID = v.ID
		}
	}

	m.keys.Set(name, ck)

	return m.withPrimary(&ck), nil
}

func (m *Mock) findKey(ref *Ref) (CryptoKey, error) {
	ck, ok := m.keys.Get(KeyName(ref))
	if !ok {
		if !m.keyRings.Has(RingName(ref)) {
			return CryptoKey{}, notFound("KeyRing", ref.KeyRing)
		}

		return CryptoKey{}, notFound("CryptoKey", ref.CryptoKey)
	}

	return ck, nil
}

// GetCryptoKey returns ref's crypto key.
func (m *Mock) GetCryptoKey(ref *Ref) (CryptoKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return CryptoKey{}, err
	}

	return m.withPrimary(&ck), nil
}

// ListCryptoKeys returns the crypto keys of ref's ring by name.
func (m *Mock) ListCryptoKeys(ref *Ref) ([]CryptoKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.keyRings.Has(RingName(ref)) {
		return nil, notFound("KeyRing", ref.KeyRing)
	}

	prefix := RingName(ref) + "/cryptoKeys/"
	keys := sortedByName(m.keys.Filter(func(k string, _ CryptoKey) bool { return strings.HasPrefix(k, prefix) }))

	for i := range keys {
		keys[i] = m.withPrimary(&keys[i])
	}

	return keys, nil
}

// UpdateCryptoKey applies p to ref's crypto key.
func (m *Mock) UpdateCryptoKey(ref *Ref, p *KeyPatch) (CryptoKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return CryptoKey{}, err
	}

	if p.Labels != nil {
		ck.Labels = maps.Clone(*p.Labels)
	}

	for dst, src := range map[*string]*string{
		&ck.RotationPeriod: p.RotationPeriod, &ck.NextRotationTime: p.NextRotationTime,
		&ck.ProtectionLevel: p.ProtectionLevel, &ck.Algorithm: p.Algorithm,
	} {
		if src != nil {
			*dst = *src
		}
	}

	m.keys.Set(ck.Name, ck)

	return m.withPrimary(&ck), nil
}

// newVersion stores a fresh version of ck (advancing ck.NextVersion; the
// caller stores ck). Callers hold m.mu.
func (m *Mock) newVersion(ck *CryptoKey, now time.Time, state string) Version {
	v := Version{
		ID: strconv.Itoa(ck.NextVersion), State: state,
		ProtectionLevel: ck.ProtectionLevel, Algorithm: ck.Algorithm, CreateTime: now,
	}
	ck.NextVersion++
	m.versions.Set(ck.Name+"/cryptoKeyVersions/"+v.ID, v)

	return v
}

// RFC3339 formats t as the UTC timestamp Cloud KMS emits.
func RFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// ParseDurationSeconds parses a protobuf Duration ("7776000s", "3.5s"). ok is
// false when d is not a valid seconds Duration.
func ParseDurationSeconds(d string) (time.Duration, bool) {
	s, ok := strings.CutSuffix(strings.TrimSpace(d), "s")
	if !ok || s == "" {
		return 0, false
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}

	return time.Duration(f * float64(time.Second)), true
}
