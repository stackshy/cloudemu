package kms

import (
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// usableVersion returns version id of ck after checking the key purpose and
// the ENABLED state, generating the key material on first use. Callers hold
// s.mu for writing.
func usableVersion(ck *cryptoKeyModel, id string, purposes ...string) (*versionModel, error) {
	if !slices.Contains(purposes, ck.purpose) {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"%s has purpose %s; this operation requires %s", ck.name, ck.purpose, strings.Join(purposes, " or "))
	}

	v, ok := ck.versions[id]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "CryptoKeyVersion %s/cryptoKeyVersions/%s not found", ck.name, id)
	}

	if v.state != stateEnabled {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"%s/cryptoKeyVersions/%s is not enabled, current state is: %s", ck.name, id, v.state)
	}

	if err := v.ensureMaterial(); err != nil {
		return nil, err
	}

	return v, nil
}

// withVersion runs fn on the route's version once it is usable for one of
// purposes.
func (s *store) withVersion(rt *route, fn func(name string, v *versionModel) error, purposes ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return err
	}

	v, err := usableVersion(ck, rt.version, purposes...)
	if err != nil {
		return err
	}

	return fn(versionName(ck, v.id), v)
}

// encrypt seals plaintext under the route's version, or under the key's
// primary version when the route names the crypto key.
func (s *store) encrypt(rt *route, plaintext, aad []byte) (opResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return opResult{}, err
	}

	id := rt.version
	if rt.kind == kindCryptoKey {
		id = ck.primaryID
	}

	if id == "" {
		return opResult{}, cerrors.Newf(cerrors.FailedPrecondition, "%s has no primary version", ck.name)
	}

	v, err := usableVersion(ck, id, purposeEncryptDecrypt)
	if err != nil {
		return opResult{}, err
	}

	ct, err := v.seal(plaintext, aad)

	return opResult{name: versionName(ck, id), protectionLevel: v.protectionLevel, out: ct}, err
}

// decrypt opens a ciphertext produced by encrypt. It uses the version id the
// ciphertext carries, so versions rotated out of primary still decrypt.
func (s *store) decrypt(rt *route, ct, aad []byte) (opResult, error) {
	id, ok := ciphertextVersion(ct)
	if !ok {
		return opResult{}, errInvalidCiphertext()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ck, err := s.findCryptoKey(rt)
	if err != nil {
		return opResult{}, err
	}

	if _, exists := ck.versions[id]; !exists && ck.purpose == purposeEncryptDecrypt {
		return opResult{}, errInvalidCiphertext()
	}

	v, err := usableVersion(ck, id, purposeEncryptDecrypt)
	if err != nil {
		return opResult{}, err
	}

	pt, err := v.open(ct, aad)

	return opResult{
		name: versionName(ck, id), protectionLevel: v.protectionLevel,
		out: pt, usedPrimary: id == ck.primaryID,
	}, err
}

// opResult is what a data-plane call hands back to the wire layer.
type opResult struct {
	name            string
	protectionLevel string
	out             []byte
	usedPrimary     bool
}

func versionName(ck *cryptoKeyModel, id string) string {
	return ck.name + "/cryptoKeyVersions/" + id
}
