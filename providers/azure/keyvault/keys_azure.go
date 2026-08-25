package keyvault

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"math/big"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

const (
	ktyRSA    = "RSA"
	ktyRSAHSM = "RSA-HSM"
	ktyEC     = "EC"
	ktyECHSM  = "EC-HSM"
	ktyOct    = "oct"
	ktyOctHSM = "oct-HSM"

	crvP256  = "P-256"
	crvP384  = "P-384"
	crvP521  = "P-521"
	crvP256K = "P-256K"

	defaultRSABits     = 2048
	defaultRSAExponent = 65537
	rsaBits3072        = 3072
	rsaBits4096        = 4096
	versionBytes       = 16
)

// keyVersion is one stored key version with its Key Vault attributes and the
// real private key material used for cryptographic operations.
type keyVersion struct {
	versionID string
	kty       string
	curve     string
	keyOps    []string
	rsaKey    *rsa.PrivateKey
	ecKey     *ecdsa.PrivateKey
	octKey    []byte
	tags      map[string]string
	enabled   bool
	expires   int64
	notBefore int64
	created   time.Time
	updated   time.Time
	current   bool
	managed   bool
}

type keyData struct {
	name           string
	versions       []keyVersion
	deletedAt      time.Time
	scheduledPurge time.Time
	// rotationPolicy is nil until UpdateKeyRotationPolicy is called, at which
	// point Key Vault starts returning it verbatim; GetKeyRotationPolicy
	// synthesizes an empty default policy while it is nil.
	rotationPolicy *driver.KVRotationPolicy
	mu             sync.RWMutex
}

func hexVersion() string {
	b := make([]byte, versionBytes)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// defaultKeyOps returns the operations Key Vault assigns when a create/import
// request omits key_ops: all six for RSA, sign/verify for EC.
func defaultKeyOps(kty string) []string {
	switch kty {
	case ktyEC, ktyECHSM:
		return []string{"sign", "verify"}
	case ktyOct, ktyOctHSM:
		return []string{"encrypt", "decrypt", "wrapKey", "unwrapKey"}
	default:
		return []string{"encrypt", "decrypt", "sign", "verify", "wrapKey", "unwrapKey"}
	}
}

// hsmVariant returns the HSM-protected kty for a software key type. Key Vault
// stores software and HSM keys identically here (single tier), but the returned
// kty must preserve the -HSM suffix the caller requested so GetKey/CreateKey
// round-trip the requested key type instead of silently downgrading it.
func hsmVariant(kty string) string {
	switch kty {
	case ktyRSA:
		return ktyRSAHSM
	case ktyEC:
		return ktyECHSM
	case ktyOct:
		return ktyOctHSM
	default:
		return kty
	}
}

func curveFor(name string) (elliptic.Curve, error) {
	switch name {
	case crvP256:
		return elliptic.P256(), nil
	case crvP384:
		return elliptic.P384(), nil
	case crvP521:
		return elliptic.P521(), nil
	case crvP256K:
		// secp256k1 is not in the Go standard library's crypto/elliptic.
		return nil, errors.Newf(errors.InvalidArgument, "curve %q is not supported", name)
	default:
		return nil, errors.Newf(errors.InvalidArgument, "curve %q is not supported", name)
	}
}

// CreateKey generates a new key (RSA or EC) with real key material and stores
// it as the current version, creating a new version if the name already exists.
func (m *Mock) CreateKey(_ context.Context, vault, name string, params *driver.KVCreateKeyParams) (*driver.KVKey, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "key name is required")
	}

	v, err := m.generateVersion(params)
	if err != nil {
		return nil, err
	}

	return storeVersion(m.vault(vault).keys, name, v)
}

func (m *Mock) generateVersion(params *driver.KVCreateKeyParams) (*keyVersion, error) {
	now := m.opts.Clock.Now().UTC()

	keyOps := params.KeyOps
	if len(keyOps) == 0 {
		keyOps = defaultKeyOps(params.Kty)
	}

	v := &keyVersion{
		versionID: hexVersion(),
		kty:       params.Kty,
		keyOps:    keyOps,
		tags:      copyTags(params.Tags),
		enabled:   params.Attributes.Enabled,
		expires:   params.Attributes.Expires,
		notBefore: params.Attributes.NotBefore,
		created:   now,
		updated:   now,
		current:   true,
	}

	switch params.Kty {
	case ktyRSA, ktyRSAHSM:
		if err := generateRSA(v, params); err != nil {
			return nil, err
		}
	case ktyEC, ktyECHSM:
		if err := generateEC(v, params); err != nil {
			return nil, err
		}
	default:
		return nil, errors.Newf(errors.InvalidArgument, "key type %q cannot be created on this vault", params.Kty)
	}

	return v, nil
}

func generateRSA(v *keyVersion, params *driver.KVCreateKeyParams) error {
	bits := params.KeySize
	if bits == 0 {
		bits = defaultRSABits
	}

	if bits != defaultRSABits && bits != rsaBits3072 && bits != rsaBits4096 {
		return errors.Newf(errors.InvalidArgument, "invalid RSA key size %d", bits)
	}

	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return errors.Newf(errors.Internal, "generate RSA key: %v", err)
	}

	if params.PublicExponent != 0 && params.PublicExponent != defaultRSAExponent {
		// Go's crypto/rsa always uses the exponent 65537; a caller-supplied
		// exponent that differs cannot be honored.
		return errors.Newf(errors.InvalidArgument, "public exponent %d is not supported", params.PublicExponent)
	}

	// v.kty carries the requested kty (RSA or RSA-HSM) from generateVersion;
	// the -HSM suffix is preserved so the returned key type is not downgraded.
	v.rsaKey = key

	return nil
}

func generateEC(v *keyVersion, params *driver.KVCreateKeyParams) error {
	curve, err := curveFor(params.Curve)
	if err != nil {
		return err
	}

	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return errors.Newf(errors.Internal, "generate EC key: %v", err)
	}

	// v.kty carries the requested kty (EC or EC-HSM) from generateVersion.
	v.curve = params.Curve
	v.ecKey = key

	return nil
}

// storeVersion appends v as the current version of name in store, creating
// the key if it does not yet exist. A soft-deleted name cannot be reused
// until recovered.
func storeVersion(store *memstore.Store[*keyData], name string, v *keyVersion) (*driver.KVKey, error) {
	if kd, ok := store.Get(name); ok {
		kd.mu.Lock()
		defer kd.mu.Unlock()

		if !kd.deletedAt.IsZero() {
			return nil, errors.Newf(errors.AlreadyExists, "key %q is in a deleted but recoverable state", name)
		}

		for i := range kd.versions {
			kd.versions[i].current = false
		}

		kd.versions = append(kd.versions, *v)
		kv := toKVKey(name, &kd.versions[len(kd.versions)-1])

		return &kv, nil
	}

	kd := &keyData{name: name, versions: []keyVersion{*v}}
	store.Set(name, kd)

	kv := toKVKey(name, &kd.versions[0])

	return &kv, nil
}

// ImportKey stores a caller-supplied key. RSA and EC keys are reconstructed
// from their JWK components; oct keys keep their raw bytes.
func (m *Mock) ImportKey(_ context.Context, vault, name string, params *driver.KVImportKeyParams) (*driver.KVKey, error) {
	if name == "" {
		return nil, errors.New(errors.InvalidArgument, "key name is required")
	}

	now := m.opts.Clock.Now().UTC()

	keyOps := params.Key.KeyOps
	if len(keyOps) == 0 {
		keyOps = defaultKeyOps(params.Key.Kty)
	}

	v := &keyVersion{
		versionID: hexVersion(),
		kty:       params.Key.Kty,
		keyOps:    keyOps,
		tags:      copyTags(params.Tags),
		enabled:   params.Attributes.Enabled,
		expires:   params.Attributes.Expires,
		notBefore: params.Attributes.NotBefore,
		created:   now,
		updated:   now,
		current:   true,
	}

	if err := importMaterial(v, &params.Key); err != nil {
		return nil, err
	}

	// An explicit hsm=true request imports the key as HSM-protected even when
	// the JWK kty is a software type; preserve that in the returned key type.
	if params.HSM {
		v.kty = hsmVariant(v.kty)
	}

	return storeVersion(m.vault(vault).keys, name, v)
}

func importMaterial(v *keyVersion, jwk *driver.KVImportJWK) error {
	// v.kty keeps the JWK's requested kty (including any -HSM suffix) so the
	// imported key type is returned as-is instead of being downgraded.
	switch jwk.Kty {
	case ktyRSA, ktyRSAHSM:
		return importRSA(v, jwk)
	case ktyEC, ktyECHSM:
		return importEC(v, jwk)
	case ktyOct, ktyOctHSM:
		v.octKey = append([]byte(nil), jwk.K...)

		return nil
	default:
		return errors.Newf(errors.InvalidArgument, "key type %q cannot be imported", jwk.Kty)
	}
}

func importRSA(v *keyVersion, jwk *driver.KVImportJWK) error {
	if len(jwk.N) == 0 || len(jwk.E) == 0 || len(jwk.D) == 0 {
		return errors.New(errors.InvalidArgument, "RSA import requires n, e and d")
	}

	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{
			N: new(big.Int).SetBytes(jwk.N),
			E: int(new(big.Int).SetBytes(jwk.E).Int64()),
		},
		D: new(big.Int).SetBytes(jwk.D),
	}

	if len(jwk.P) > 0 && len(jwk.Q) > 0 {
		key.Primes = []*big.Int{new(big.Int).SetBytes(jwk.P), new(big.Int).SetBytes(jwk.Q)}
	}

	if err := key.Validate(); err != nil {
		return errors.Newf(errors.InvalidArgument, "invalid RSA key material: %v", err)
	}

	key.Precompute()
	v.rsaKey = key

	return nil
}

func importEC(v *keyVersion, jwk *driver.KVImportJWK) error {
	curve, err := curveFor(jwk.Curve)
	if err != nil {
		return err
	}

	if len(jwk.X) == 0 || len(jwk.Y) == 0 || len(jwk.D) == 0 {
		return errors.New(errors.InvalidArgument, "EC import requires x, y and d")
	}

	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(jwk.X),
			Y:     new(big.Int).SetBytes(jwk.Y),
		},
		D: new(big.Int).SetBytes(jwk.D),
	}

	//nolint:staticcheck // IsOnCurve is the direct on-curve check for an ecdsa.PublicKey we reconstruct from JWK components
	if !curve.IsOnCurve(key.X, key.Y) {
		return errors.New(errors.InvalidArgument, "invalid EC key material: point is not on curve")
	}

	v.curve = jwk.Curve
	v.ecKey = key

	return nil
}
