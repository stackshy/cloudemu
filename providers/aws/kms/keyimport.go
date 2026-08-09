package kms

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SHA-1 is required by the RSAES_OAEP_SHA_1 wrapping algorithm KMS defines
	"crypto/sha256"
	"crypto/x509"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const (
	importParamsValidHours = 24
	wrapRSA2048            = 2048
	wrapRSA3072            = 3072
	wrapRSA4096            = 4096
)

// wrappingKeySpecValid reports whether spec is a supported wrapping key spec,
// returning a validation error otherwise. Its bool result lets callers validate
// up front without generating a key.
func wrappingKeySpecValid(spec string) (bool, error) {
	switch spec {
	case driver.WrapKeySpecRSA2048, "", driver.WrapKeySpecRSA3072, driver.WrapKeySpecRSA4096:
		return true, nil
	default:
		return false, errors.Newf(errors.InvalidArgument, "unsupported WrappingKeySpec %q", spec)
	}
}

// newWrappingKey generates the RSA wrapping key pair for a WrappingKeySpec.
// The sizes are passed as literals (never below 2048) so the minimum-key-size
// guarantee is statically verifiable rather than hidden behind a variable.
func newWrappingKey(spec string) (*rsa.PrivateKey, error) {
	switch spec {
	case driver.WrapKeySpecRSA2048, "":
		return rsa.GenerateKey(rand.Reader, wrapRSA2048)
	case driver.WrapKeySpecRSA3072:
		return rsa.GenerateKey(rand.Reader, wrapRSA3072)
	case driver.WrapKeySpecRSA4096:
		return rsa.GenerateKey(rand.Reader, wrapRSA4096)
	default:
		return nil, errors.Newf(errors.InvalidArgument, "unsupported WrappingKeySpec %q", spec)
	}
}

// GetParametersForImport mints an RSA wrapping key pair and import token for an
// EXTERNAL-origin key, returning the public key the caller uses to wrap its
// material.
func (m *Mock) GetParametersForImport(
	_ context.Context, in driver.GetParametersForImportInput,
) (*driver.GetParametersForImportOutput, error) {
	if _, err := wrappingKeySpecValid(in.WrappingKeySpec); err != nil {
		return nil, err
	}

	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	if kd.meta.Origin != driver.OriginExternal {
		return nil, errors.New(errors.InvalidArgument, "GetParametersForImport requires an EXTERNAL-origin key")
	}

	wrapKey, err := newWrappingKey(in.WrappingKeySpec)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "wrapping key: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&wrapKey.PublicKey)
	if err != nil {
		return nil, errors.Newf(errors.Internal, "marshal wrapping public key: %v", err)
	}

	token := []byte("import-token-" + idgen.GenerateID(""))
	kd.importWrappingKey = wrapKey
	kd.importToken = token

	return &driver.GetParametersForImportOutput{
		KeyID:             kd.meta.KeyID,
		ImportToken:       token,
		PublicKey:         pubDER,
		ParametersValidTo: m.now().Add(importParamsValidHours * time.Hour),
	}, nil
}

// ImportKeyMaterial unwraps and installs externally-supplied key material.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API
func (m *Mock) ImportKeyMaterial(_ context.Context, in driver.ImportKeyMaterialInput) error {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return err
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	if kd.importWrappingKey == nil || string(kd.importToken) != string(in.ImportToken) {
		return errors.New(errors.InvalidArgument, "invalid or expired import token; call GetParametersForImport first")
	}

	// The wrapping algorithm is inferred from what the material decrypts under;
	// default OAEP-SHA-256. KMS carries it in GetParametersForImport, but the
	// import request itself doesn't, so try OAEP then PKCS1v15.
	material, err := m.unwrapMaterial(kd.importWrappingKey, in.EncryptedKeyMaterial)
	if err != nil {
		return err
	}

	kd.material = material
	kd.meta.Enabled = true
	kd.meta.KeyState = driver.StateEnabled
	kd.importWrappingKey = nil
	kd.importToken = nil

	if in.ExpirationModel == driver.ExpirationKeyMaterialExpires {
		kd.meta.ValidTo = in.ValidTo
	} else {
		kd.meta.ValidTo = time.Time{}
	}

	return nil
}

func (*Mock) unwrapMaterial(wrapKey *rsa.PrivateKey, wrapped []byte) ([]byte, error) {
	if mat, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, wrapKey, wrapped, nil); err == nil {
		return mat, nil
	}

	//nolint:gosec // SHA-1 is mandated by the RSAES_OAEP_SHA_1 wrapping algorithm
	if mat, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, wrapKey, wrapped, nil); err == nil {
		return mat, nil
	}

	mat, err := rsa.DecryptPKCS1v15(rand.Reader, wrapKey, wrapped)
	if err != nil {
		return nil, errors.New(errors.InvalidArgument, "could not unwrap key material with any supported algorithm")
	}

	return mat, nil
}

// DeleteImportedKeyMaterial removes imported material, returning the key to
// PendingImport.
func (m *Mock) DeleteImportedKeyMaterial(_ context.Context, keyID string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.Origin != driver.OriginExternal {
			return errors.New(errors.InvalidArgument, "key material can only be deleted for EXTERNAL-origin keys")
		}

		kd.material = nil
		kd.meta.Enabled = false
		kd.meta.KeyState = driver.StatePendingImport
		kd.meta.ValidTo = time.Time{}

		return nil
	})
}

// ReplicateKey creates a replica of a multi-region key in another region.
// The emulator runs a single region per provider instance, so the replica is
// modeled as a sibling key record sharing the primary's material; cross-region
// lookup is not otherwise observable.
func (m *Mock) ReplicateKey(_ context.Context, in driver.ReplicateKeyInput) (*driver.ReplicateKeyOutput, error) {
	if in.ReplicaRegion == "" {
		return nil, errors.New(errors.InvalidArgument, "ReplicaRegion is required")
	}

	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return nil, err
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	if !kd.meta.MultiRegion {
		return nil, errors.New(errors.InvalidArgument, "only multi-region keys can be replicated")
	}

	kd.meta.ReplicaRegions = appendUnique(kd.meta.ReplicaRegions, in.ReplicaRegion)
	if kd.meta.PrimaryRegion == "" {
		kd.meta.PrimaryRegion = m.opts.Region
	}

	desc := in.Description
	if desc == "" {
		desc = kd.meta.Description
	}

	replica := kd.meta
	replica.Description = desc
	replica.MultiRegionKeyType = "REPLICA"
	replica.ARN = idgen.AWSARN("kms", in.ReplicaRegion, m.opts.AccountID, "key/"+kd.meta.KeyID)
	replica.ReplicaRegions = nil

	policy := defaultKeyPolicy(in.Policy, m.opts.AccountID)

	tags := in.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	return &driver.ReplicateKeyOutput{ReplicaKeyMetadata: &replica, ReplicaPolicy: policy, ReplicaTags: tags}, nil
}

// UpdatePrimaryRegion changes which region is primary for a multi-region key.
func (m *Mock) UpdatePrimaryRegion(_ context.Context, keyID, primaryRegion string) error {
	if primaryRegion == "" {
		return errors.New(errors.InvalidArgument, "PrimaryRegion is required")
	}

	return m.mutateKey(keyID, func(kd *keyData) error {
		if !kd.meta.MultiRegion {
			return errors.New(errors.InvalidArgument, "only multi-region keys have a primary region")
		}

		kd.meta.PrimaryRegion = primaryRegion

		return nil
	})
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}

	return append(s, v)
}
