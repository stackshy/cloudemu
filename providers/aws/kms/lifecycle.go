package kms

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const symmetricKeyBytes = 32 // AES-256

// CreateKey creates a customer master key, generating its material.
//
//nolint:gocritic // in is the public CreateKey input, taken by value to match the driver API
func (m *Mock) CreateKey(_ context.Context, in driver.CreateKeyInput) (*driver.KeyMetadata, error) {
	usage := in.KeyUsage
	if usage == "" {
		usage = driver.UsageEncryptDecrypt
	}

	spec := in.KeySpec
	if spec == "" {
		spec = driver.SpecSymmetricDefault
	}

	if err := validateSpecUsage(spec, usage); err != nil {
		return nil, err
	}

	origin := in.Origin
	if origin == "" {
		origin = driver.OriginAWSKMS
	}

	id := newKeyID()
	now := m.now()

	kd := &keyData{
		meta: driver.KeyMetadata{
			KeyID:        id,
			ARN:          m.keyARN(id),
			AWSAccountID: m.opts.AccountID,
			Description:  in.Description,
			Enabled:      true,
			KeyUsage:     usage,
			KeyState:     driver.StateEnabled,
			KeySpec:      spec,
			Origin:       origin,
			KeyManager:   driver.ManagerCustomer,
			MultiRegion:  in.MultiRegion,
			CreationDate: now,
		},
		tags:     copyTags(in.Tags),
		policies: map[string]string{driver.DefaultPolicyName: defaultKeyPolicy(in.Policy, m.opts.AccountID)},
	}

	// EXTERNAL-origin keys have no material until it is imported; they start
	// PendingImport and disabled.
	switch {
	case origin == driver.OriginExternal:
		kd.meta.Enabled = false
		kd.meta.KeyState = driver.StatePendingImport
	case isAsymmetricSpec(spec):
		priv, err := generateAsymmetric(spec)
		if err != nil {
			return nil, err
		}

		kd.privKey = priv
	default:
		mat, err := generateMaterial(spec)
		if err != nil {
			return nil, err
		}

		kd.material = mat
	}

	m.keys.Set(id, kd)

	out := kd.meta

	return &out, nil
}

// generateMaterial produces raw key bytes for symmetric and HMAC specs.
// Asymmetric material is generated in the crypto layer.
func generateMaterial(spec string) ([]byte, error) {
	var size int

	switch spec {
	case driver.SpecSymmetricDefault:
		size = symmetricKeyBytes
	case driver.SpecHMAC256:
		size = 32
	case driver.SpecHMAC384:
		size = 48
	case driver.SpecHMAC512:
		size = 64
	default:
		// Asymmetric specs are handled by the crypto layer; nothing to do here.
		return nil, nil
	}

	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return nil, errors.Newf(errors.Internal, "generate key material: %v", err)
	}

	return buf, nil
}

// validateSpecUsage rejects spec/usage combinations KMS does not allow.
func validateSpecUsage(spec, usage string) error {
	switch {
	case spec == driver.SpecSymmetricDefault && usage != driver.UsageEncryptDecrypt:
		return errors.Newf(errors.InvalidArgument,
			"symmetric keys only support ENCRYPT_DECRYPT, got %q", usage)
	case strings.HasPrefix(spec, "HMAC_") && usage != driver.UsageGenerateVerifyMac:
		return errors.Newf(errors.InvalidArgument,
			"HMAC keys only support GENERATE_VERIFY_MAC, got %q", usage)
	case strings.HasPrefix(spec, "RSA_") && usage != driver.UsageEncryptDecrypt && usage != driver.UsageSignVerify:
		return errors.Newf(errors.InvalidArgument,
			"RSA keys support ENCRYPT_DECRYPT or SIGN_VERIFY, got %q", usage)
	case strings.HasPrefix(spec, "ECC_") && usage != driver.UsageSignVerify:
		return errors.Newf(errors.InvalidArgument,
			"ECC keys only support SIGN_VERIFY, got %q", usage)
	default:
		return nil
	}
}

// DescribeKey returns the metadata of a key referenced by ID/ARN/alias.
func (m *Mock) DescribeKey(_ context.Context, keyID string) (*driver.KeyMetadata, error) {
	kd, err := m.getKey(keyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	out := kd.meta

	return &out, nil
}

// ListKeys returns every key's metadata.
func (m *Mock) ListKeys(_ context.Context) ([]driver.KeyMetadata, error) {
	all := m.keys.All()
	out := make([]driver.KeyMetadata, 0, len(all))

	for _, kd := range all {
		kd.mu.RLock()
		out = append(out, kd.meta)
		kd.mu.RUnlock()
	}

	return out, nil
}

// EnableKey marks a key enabled.
func (m *Mock) EnableKey(_ context.Context, keyID string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.KeyState == driver.StatePendingDeletion {
			return errors.Newf(errors.FailedPrecondition, "key %q is pending deletion", keyID)
		}

		kd.meta.Enabled = true
		kd.meta.KeyState = driver.StateEnabled

		return nil
	})
}

// DisableKey marks a key disabled.
func (m *Mock) DisableKey(_ context.Context, keyID string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.KeyState == driver.StatePendingDeletion {
			return errors.Newf(errors.FailedPrecondition, "key %q is pending deletion", keyID)
		}

		kd.meta.Enabled = false
		kd.meta.KeyState = driver.StateDisabled

		return nil
	})
}

// UpdateKeyDescription changes a key's description.
func (m *Mock) UpdateKeyDescription(_ context.Context, keyID, description string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		kd.meta.Description = description

		return nil
	})
}

// ScheduleKeyDeletion moves a key to PendingDeletion with a deletion date.
func (m *Mock) ScheduleKeyDeletion(
	_ context.Context, keyID string, pendingWindowDays int32,
) (*driver.KeyMetadata, error) {
	days := int(pendingWindowDays)
	if days == 0 {
		days = defaultPendingWindowDays
	}

	if days < minPendingWindowDays || days > maxPendingWindowDays {
		return nil, errors.Newf(errors.InvalidArgument,
			"pending window must be %d-%d days, got %d", minPendingWindowDays, maxPendingWindowDays, days)
	}

	var out driver.KeyMetadata

	err := m.mutateKey(keyID, func(kd *keyData) error {
		kd.meta.Enabled = false
		kd.meta.KeyState = driver.StatePendingDeletion
		kd.meta.DeletionDate = m.now().Add(time.Duration(days) * hoursPerDay * time.Hour)
		out = kd.meta

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// CancelKeyDeletion returns a PendingDeletion key to Disabled.
func (m *Mock) CancelKeyDeletion(_ context.Context, keyID string) (*driver.KeyMetadata, error) {
	var out driver.KeyMetadata

	err := m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.KeyState != driver.StatePendingDeletion {
			return errors.Newf(errors.FailedPrecondition, "key %q is not pending deletion", keyID)
		}

		kd.meta.KeyState = driver.StateDisabled
		kd.meta.DeletionDate = time.Time{}
		out = kd.meta

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &out, nil
}

// mutateKey resolves a key and runs fn under its write lock.
func (m *Mock) mutateKey(keyID string, fn func(*keyData) error) error {
	kd, err := m.getKey(keyID)
	if err != nil {
		return err
	}

	kd.mu.Lock()
	defer kd.mu.Unlock()

	return fn(kd)
}

func copyTags(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
