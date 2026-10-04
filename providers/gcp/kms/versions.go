package kms

import (
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func versionName(keyName, id string) string {
	return keyName + "/cryptoKeyVersions/" + id
}

// readCopy strips key material from a version handed to the wire layer.
func readCopy(in *Version) Version {
	v := *in
	v.Secret, v.PrivateKey = nil, nil

	return v
}

func (m *Mock) findVersion(ref *Ref) (CryptoKey, Version, error) {
	ck, err := m.findKey(ref)
	if err != nil {
		return CryptoKey{}, Version{}, err
	}

	v, ok := m.versions.Get(versionName(ck.Name, ref.Version))
	if !ok {
		return CryptoKey{}, Version{}, notFound("CryptoKeyVersion", ref.Version)
	}

	return ck, v, nil
}

// UpdateCryptoKeyPrimaryVersion makes versionID the primary of an ENCRYPT_DECRYPT key.
func (m *Mock) UpdateCryptoKeyPrimaryVersion(ref *Ref, versionID string) (CryptoKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return CryptoKey{}, err
	}

	if ck.Purpose != PurposeEncryptDecrypt {
		return CryptoKey{}, cerrors.New(cerrors.InvalidArgument,
			"UpdateCryptoKeyPrimaryVersion is only valid for keys with purpose ENCRYPT_DECRYPT")
	}

	v, ok := m.versions.Get(versionName(ck.Name, versionID))
	if !ok {
		return CryptoKey{}, notFound("CryptoKeyVersion", versionID)
	}

	if v.State != StateEnabled {
		return CryptoKey{}, cerrors.New(cerrors.FailedPrecondition, "the primary version must be ENABLED")
	}

	ck.PrimaryID = versionID
	m.keys.Set(ck.Name, ck)

	return m.withPrimary(&ck), nil
}

// CreateCryptoKeyVersion adds a version to ref's key in state (ENABLED when empty).
func (m *Mock) CreateCryptoKeyVersion(ref *Ref, state string) (keyName string, v Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return "", Version{}, err
	}

	if state == "" {
		state = StateEnabled
	}

	v = m.newVersion(&ck, m.clock.Now(), state)
	m.keys.Set(ck.Name, ck)

	return ck.Name, v, nil
}

// GetCryptoKeyVersion returns ref's version.
func (m *Mock) GetCryptoKeyVersion(ref *Ref) (keyName string, v Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, v, err := m.findVersion(ref)

	return ck.Name, readCopy(&v), err
}

// ListCryptoKeyVersions returns ref's key versions newest first, matching real Cloud KMS.
func (m *Mock) ListCryptoKeyVersions(ref *Ref) (keyName string, out []Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return "", nil, err
	}

	prefix := ck.Name + "/cryptoKeyVersions/"
	all := m.versions.Filter(func(k string, _ Version) bool { return strings.HasPrefix(k, prefix) })

	out = make([]Version, 0, len(all))

	for k := range all {
		v := all[k]
		out = append(out, readCopy(&v))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })

	return ck.Name, out, nil
}

// UpdateCryptoKeyVersion moves a version between ENABLED and DISABLED; empty state is a
// no-op.
func (m *Mock) UpdateCryptoKeyVersion(ref *Ref, state string) (keyName string, v Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, v, err := m.findVersion(ref)
	if err != nil {
		return "", Version{}, err
	}

	switch state {
	case StateEnabled, StateDisabled:
		if v.State != StateEnabled && v.State != StateDisabled {
			return "", Version{}, cerrors.Newf(cerrors.FailedPrecondition,
				"cannot move version from %s to %s", v.State, state)
		}

		v.State = state
	case "":
	default:
		return "", Version{}, cerrors.Newf(cerrors.InvalidArgument, "state %s is not user-settable", state)
	}

	m.versions.Set(versionName(ck.Name, v.ID), v)

	return ck.Name, readCopy(&v), nil
}

// DestroyCryptoKeyVersion schedules destruction after the key's
// destroyScheduledDuration; a destroyed primary leaves the key without one.
func (m *Mock) DestroyCryptoKeyVersion(ref *Ref) (keyName string, v Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, v, err := m.findVersion(ref)
	if err != nil {
		return "", Version{}, err
	}

	if v.State != StateEnabled && v.State != StateDisabled {
		return "", Version{}, cerrors.Newf(cerrors.FailedPrecondition,
			"CryptoKeyVersion in state %s cannot be destroyed", v.State)
	}

	now := m.clock.Now()
	v.State = StateDestroyScheduled
	v.DestroyTime = RFC3339(now)

	if d, ok := ParseDurationSeconds(ck.DestroyScheduledDuration); ok {
		v.DestroyTime = RFC3339(now.Add(d))
	}

	m.versions.Set(versionName(ck.Name, v.ID), v)

	if ck.PrimaryID == v.ID {
		ck.PrimaryID = ""
		m.keys.Set(ck.Name, ck)
	}

	return ck.Name, readCopy(&v), nil
}

// RestoreCryptoKeyVersion returns a DESTROY_SCHEDULED version to DISABLED.
func (m *Mock) RestoreCryptoKeyVersion(ref *Ref) (keyName string, v Version, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, v, err := m.findVersion(ref)
	if err != nil {
		return "", Version{}, err
	}

	if v.State != StateDestroyScheduled {
		return "", Version{}, cerrors.Newf(cerrors.FailedPrecondition,
			"only a DESTROY_SCHEDULED version can be restored, not one in state %s", v.State)
	}

	v.State, v.DestroyTime = StateDisabled, ""
	m.versions.Set(versionName(ck.Name, v.ID), v)

	return ck.Name, readCopy(&v), nil
}
