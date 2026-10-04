package kms

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	purposeAsymmetricSign    = "ASYMMETRIC_SIGN"
	purposeAsymmetricDecrypt = "ASYMMETRIC_DECRYPT"
	purposeMAC               = "MAC"

	minRandomBytes = 8
	maxRandomBytes = 1024
)

// Result is what a data-plane call hands back to the wire layer.
type Result struct {
	Name            string
	ProtectionLevel string
	Out             []byte
	UsedPrimary     bool
}

// usableVersion returns version id of ck after checking the key purpose and
// the ENABLED state, generating and storing the key material on first use.
// Callers hold m.mu.
func (m *Mock) usableVersion(ck *CryptoKey, id string, purposes ...string) (Version, error) {
	if !slices.Contains(purposes, ck.Purpose) {
		return Version{}, cerrors.Newf(cerrors.FailedPrecondition,
			"%s has purpose %s; this operation requires %s", ck.Name, ck.Purpose, strings.Join(purposes, " or "))
	}

	name := versionName(ck.Name, id)

	v, ok := m.versions.Get(name)
	if !ok {
		return Version{}, cerrors.Newf(cerrors.NotFound, "CryptoKeyVersion %s not found", name)
	}

	if v.State != StateEnabled {
		return Version{}, cerrors.Newf(cerrors.FailedPrecondition,
			"%s is not enabled, current state is: %s", name, v.State)
	}

	if v.Secret == nil && v.PrivateKey == nil {
		if err := v.ensureMaterial(); err != nil {
			return Version{}, err
		}

		m.versions.Set(name, v)
	}

	return v, nil
}

// usable resolves ref's version for one of purposes. Callers hold m.mu.
func (m *Mock) usable(ref *Ref, purposes ...string) (string, Version, error) {
	ck, err := m.findKey(ref)
	if err != nil {
		return "", Version{}, err
	}

	v, err := m.usableVersion(&ck, ref.Version, purposes...)

	return versionName(ck.Name, ref.Version), v, err
}

// Digest is an asymmetricSign request digest; the field matching the
// algorithm's hash is used.
type Digest struct {
	Sha256, Sha384, Sha512 []byte
}

// signInput resolves the bytes alg signs: the raw data for Ed25519 and
// RSA_SIGN_RAW_PKCS1_*, otherwise a digest (supplied, or computed from data)
// of the algorithm's hash.
func signInput(alg string, digest *Digest, data []byte) ([]byte, error) {
	if alg == algEd25519 || strings.HasPrefix(alg, "RSA_SIGN_RAW_PKCS1") {
		if len(data) == 0 {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "data is required for algorithm %s", alg)
		}

		return nil, nil
	}

	hash := hashFor(alg)

	if digest == nil {
		if len(data) == 0 {
			return nil, cerrors.New(cerrors.InvalidArgument, "one of digest or data is required")
		}

		hh := hash.New()
		hh.Write(data)

		return hh.Sum(nil), nil
	}

	d := map[crypto.Hash][]byte{crypto.SHA256: digest.Sha256, crypto.SHA384: digest.Sha384, crypto.SHA512: digest.Sha512}[hash]
	if len(d) != hash.Size() {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"digest must be a %d-byte %s digest for algorithm %s", hash.Size(), hash, alg)
	}

	return d, nil
}

// AsymmetricSign signs digest (or data) with ref's ASYMMETRIC_SIGN version.
func (m *Mock) AsymmetricSign(ref *Ref, digest *Digest, data []byte) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, v, err := m.usable(ref, purposeAsymmetricSign)
	if err != nil {
		return Result{}, err
	}

	in, err := signInput(v.Algorithm, digest, data)
	if err != nil {
		return Result{}, err
	}

	sig, err := v.sign(in, data)
	if err != nil {
		return Result{}, cerrors.Newf(cerrors.InvalidArgument, "sign: %v", err)
	}

	return Result{Name: name, ProtectionLevel: v.ProtectionLevel, Out: sig}, nil
}

// AsymmetricDecrypt opens an RSA-OAEP ciphertext with ref's ASYMMETRIC_DECRYPT
// version.
func (m *Mock) AsymmetricDecrypt(ref *Ref, ct []byte) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, v, err := m.usable(ref, purposeAsymmetricDecrypt)
	if err != nil {
		return Result{}, err
	}

	pt, err := v.decryptOAEP(ct)

	return Result{Name: name, ProtectionLevel: v.ProtectionLevel, Out: pt}, err
}

// PublicKey is a getPublicKey result.
type PublicKey struct {
	Name, Pem, Algorithm, ProtectionLevel string
}

// GetPublicKey returns the PEM public key of ref's asymmetric version.
func (m *Mock) GetPublicKey(ref *Ref) (PublicKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, v, err := m.usable(ref, purposeAsymmetricSign, purposeAsymmetricDecrypt)
	if err != nil {
		return PublicKey{}, err
	}

	p, err := v.publicKeyPEM()

	return PublicKey{Name: name, Pem: p, Algorithm: v.Algorithm, ProtectionLevel: v.ProtectionLevel}, err
}

// MacSign returns the HMAC of data under ref's MAC version.
func (m *Mock) MacSign(ref *Ref, data []byte) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, v, err := m.usable(ref, purposeMAC)
	if err != nil {
		return Result{}, err
	}

	return Result{Name: name, ProtectionLevel: v.ProtectionLevel, Out: v.mac(data)}, nil
}

// MacVerify reports whether mac is the HMAC of data under ref's MAC version.
func (m *Mock) MacVerify(ref *Ref, data, mac []byte) (Result, bool, error) {
	res, err := m.MacSign(ref, data)
	if err != nil {
		return Result{}, false, err
	}

	ok := hmac.Equal(res.Out, mac)
	res.Out = nil

	return res, ok, nil
}

// GenerateRandomBytes returns n random bytes; real Cloud KMS allows 8 to 1024.
func (*Mock) GenerateRandomBytes(n int) ([]byte, error) {
	if n < minRandomBytes || n > maxRandomBytes {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"lengthBytes must be between %d and %d", minRandomBytes, maxRandomBytes)
	}

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, cerrors.Newf(cerrors.Internal, "random: %v", err)
	}

	return b, nil
}

// Encrypt seals plaintext under ref's version when ref names one, or under
// the key's primary version otherwise.
func (m *Mock) Encrypt(ref *Ref, plaintext, aad []byte) (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return Result{}, err
	}

	id := ref.Version
	if id == "" {
		id = ck.PrimaryID
	}

	if id == "" {
		return Result{}, cerrors.Newf(cerrors.FailedPrecondition, "%s has no primary version", ck.Name)
	}

	v, err := m.usableVersion(&ck, id, PurposeEncryptDecrypt)
	if err != nil {
		return Result{}, err
	}

	ct, err := v.seal(plaintext, aad)

	return Result{Name: versionName(ck.Name, id), ProtectionLevel: v.ProtectionLevel, Out: ct}, err
}

// Decrypt opens a ciphertext produced by Encrypt. It uses the version id the
// ciphertext carries, so versions rotated out of primary still decrypt.
func (m *Mock) Decrypt(ref *Ref, ct, aad []byte) (Result, error) {
	id, ok := ciphertextVersion(ct)
	if !ok {
		return Result{}, errInvalidCiphertext()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ck, err := m.findKey(ref)
	if err != nil {
		return Result{}, err
	}

	if ck.Purpose == PurposeEncryptDecrypt && !m.versions.Has(versionName(ck.Name, id)) {
		return Result{}, errInvalidCiphertext()
	}

	v, err := m.usableVersion(&ck, id, PurposeEncryptDecrypt)
	if err != nil {
		return Result{}, err
	}

	pt, err := v.open(ct, aad)

	return Result{
		Name: versionName(ck.Name, id), ProtectionLevel: v.ProtectionLevel,
		Out: pt, UsedPrimary: id == ck.PrimaryID,
	}, err
}
