package kms

import (
	"context"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const (
	defaultRotationPeriodDays = 365
	minRotationPeriodDays     = 90
	maxRotationPeriodDays     = 2560
	hoursPerDayMgmt           = 24
)

// defaultKeyPolicy returns the supplied policy, or a root-account default when
// empty (mirroring the policy KMS attaches when none is given).
func defaultKeyPolicy(supplied, accountID string) string {
	if supplied != "" {
		return supplied
	}

	return fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"Enable IAM User Permissions",`+
		`"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::%s:root"},"Action":"kms:*","Resource":"*"}]}`, accountID)
}

// CreateGrant delegates key operations to a principal and returns the grant's
// ID and an opaque grant token.
//
//nolint:gocritic // in is the public CreateGrant input, taken by value to match the driver API
func (m *Mock) CreateGrant(_ context.Context, in driver.CreateGrantInput) (string, string, error) {
	kd, err := m.getKey(in.KeyID)
	if err != nil {
		return "", "", err
	}

	if in.GranteePrincipal == "" {
		return "", "", errors.New(errors.InvalidArgument, "GranteePrincipal is required")
	}

	if len(in.Operations) == 0 {
		return "", "", errors.New(errors.InvalidArgument, "at least one grant operation is required")
	}

	grantID := idgen.GenerateID("")
	token := "grant-token-" + idgen.GenerateID("")

	kd.mu.RLock()
	keyID := kd.meta.KeyID
	kd.mu.RUnlock()

	m.grants.Set(grantID, &driver.Grant{
		GrantID:           grantID,
		GrantToken:        token,
		KeyID:             keyID,
		Name:              in.Name,
		GranteePrincipal:  in.GranteePrincipal,
		RetiringPrincipal: in.RetiringPrincipal,
		IssuingAccount:    m.opts.AccountID,
		Operations:        append([]string(nil), in.Operations...),
		Constraints:       in.Constraints,
		CreationDate:      m.now(),
	})

	return grantID, token, nil
}

// ListGrants lists the grants on a key.
func (m *Mock) ListGrants(_ context.Context, keyID string) ([]driver.Grant, error) {
	id, err := m.resolveKeyID(keyID)
	if err != nil {
		return nil, err
	}

	if !m.keys.Has(id) {
		return nil, errors.Newf(errors.NotFound, "key %q not found", keyID)
	}

	all := m.grants.All()
	out := make([]driver.Grant, 0, len(all))

	for _, g := range all {
		if g.KeyID == id {
			out = append(out, *g)
		}
	}

	return out, nil
}

// RevokeGrant deletes a grant by key + grant ID.
func (m *Mock) RevokeGrant(_ context.Context, keyID, grantID string) error {
	id, err := m.resolveKeyID(keyID)
	if err != nil {
		return err
	}

	g, ok := m.grants.Get(grantID)
	if !ok || g.KeyID != id {
		return errors.Newf(errors.NotFound, "grant %q not found on key %q", grantID, keyID)
	}

	m.grants.Delete(grantID)

	return nil
}

// RetireGrant retires a grant identified by grant token, or by key + grant ID.
func (m *Mock) RetireGrant(_ context.Context, grantToken, keyID, grantID string) error {
	if grantToken != "" {
		for gid, g := range m.grants.All() {
			if g.GrantToken == grantToken {
				m.grants.Delete(gid)

				return nil
			}
		}

		return errors.Newf(errors.NotFound, "no grant matches the supplied token")
	}

	id, err := m.resolveKeyID(keyID)
	if err != nil {
		return err
	}

	g, ok := m.grants.Get(grantID)
	if !ok || g.KeyID != id {
		return errors.Newf(errors.NotFound, "grant %q not found on key %q", grantID, keyID)
	}

	m.grants.Delete(grantID)

	return nil
}

// ListRetirableGrants lists grants whose retiring principal matches.
func (m *Mock) ListRetirableGrants(_ context.Context, retiringPrincipal string) ([]driver.Grant, error) {
	all := m.grants.All()
	out := make([]driver.Grant, 0)

	for _, g := range all {
		if g.RetiringPrincipal == retiringPrincipal {
			out = append(out, *g)
		}
	}

	return out, nil
}

// EnableKeyRotation turns on automatic rotation for a symmetric key.
func (m *Mock) EnableKeyRotation(_ context.Context, keyID string, rotationPeriodDays int32) error {
	days := rotationPeriodDays
	if days == 0 {
		days = defaultRotationPeriodDays
	}

	if days < minRotationPeriodDays || days > maxRotationPeriodDays {
		return errors.Newf(errors.InvalidArgument,
			"rotation period must be %d-%d days", minRotationPeriodDays, maxRotationPeriodDays)
	}

	return m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.KeySpec != driver.SpecSymmetricDefault {
			return errors.New(errors.InvalidArgument, "only symmetric keys support rotation")
		}

		kd.rotationEnabled = true
		kd.rotationPeriodDays = days

		return nil
	})
}

// DisableKeyRotation turns off automatic rotation.
func (m *Mock) DisableKeyRotation(_ context.Context, keyID string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		kd.rotationEnabled = false

		return nil
	})
}

// GetKeyRotationStatus reports a key's rotation configuration.
func (m *Mock) GetKeyRotationStatus(_ context.Context, keyID string) (*driver.RotationStatus, error) {
	kd, err := m.getKey(keyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	st := &driver.RotationStatus{
		KeyID:                 kd.meta.KeyID,
		Enabled:               kd.rotationEnabled,
		RotationPeriodDays:    kd.rotationPeriodDays,
		OnDemandRotationCount: kd.onDemandCount,
	}

	if kd.rotationEnabled {
		period := kd.rotationPeriodDays
		if period == 0 {
			period = defaultRotationPeriodDays
		}

		st.NextRotationDate = m.now().Add(time.Duration(period) * hoursPerDayMgmt * time.Hour)
	}

	return st, nil
}

// ListKeyRotations lists a key's past rotations.
func (m *Mock) ListKeyRotations(_ context.Context, keyID string) ([]driver.RotationEvent, error) {
	kd, err := m.getKey(keyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	return append([]driver.RotationEvent(nil), kd.rotations...), nil
}

// RotateKeyOnDemand rotates the key material now and records the rotation.
func (m *Mock) RotateKeyOnDemand(_ context.Context, keyID string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		if kd.meta.KeySpec != driver.SpecSymmetricDefault {
			return errors.New(errors.InvalidArgument, "only symmetric keys support on-demand rotation")
		}

		mat, err := generateMaterial(kd.meta.KeySpec)
		if err != nil {
			return err
		}

		// New material re-keys future Encrypt calls; existing ciphertext blobs
		// remain decryptable because they embed their own key version's bytes
		// only implicitly — the emulator keeps a single active material, which
		// is sufficient for the local-dev rotation semantics we surface.
		kd.material = mat
		kd.onDemandCount++
		kd.rotations = append(kd.rotations, driver.RotationEvent{
			KeyID: kd.meta.KeyID, RotationDate: m.now(), RotationType: driver.RotationOnDemand,
		})

		return nil
	})
}

// GetKeyPolicy returns a key's policy document.
func (m *Mock) GetKeyPolicy(_ context.Context, keyID, policyName string) (string, error) {
	name := policyName
	if name == "" {
		name = driver.DefaultPolicyName
	}

	kd, err := m.getKey(keyID)
	if err != nil {
		return "", err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	doc, ok := kd.policies[name]
	if !ok {
		return "", errors.Newf(errors.NotFound, "policy %q not found", name)
	}

	return doc, nil
}

// PutKeyPolicy replaces a key's policy document.
func (m *Mock) PutKeyPolicy(_ context.Context, keyID, policyName, policy string) error {
	name := policyName
	if name == "" {
		name = driver.DefaultPolicyName
	}

	if name != driver.DefaultPolicyName {
		return errors.Newf(errors.InvalidArgument, "the only supported policy name is %q", driver.DefaultPolicyName)
	}

	if policy == "" {
		return errors.New(errors.InvalidArgument, "policy document is required")
	}

	return m.mutateKey(keyID, func(kd *keyData) error {
		kd.policies[name] = policy

		return nil
	})
}

// ListKeyPolicies lists a key's policy names (always just "default").
func (m *Mock) ListKeyPolicies(_ context.Context, keyID string) ([]string, error) {
	kd, err := m.getKey(keyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	out := make([]string, 0, len(kd.policies))
	for name := range kd.policies {
		out = append(out, name)
	}

	return out, nil
}
