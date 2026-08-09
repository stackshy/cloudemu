package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateEmailIdentityPolicy attaches a resource policy to an identity.
func (m *Mock) CreateEmailIdentityPolicy(_ context.Context, identity, policyName, policy string) error {
	d, err := m.getIdentity(identity)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.id.Policies[policyName]; ok {
		return cerrors.Newf(cerrors.AlreadyExists, "policy %q already exists", policyName)
	}

	if d.id.Policies == nil {
		d.id.Policies = make(map[string]string)
	}

	d.id.Policies[policyName] = policy

	return nil
}

// GetEmailIdentityPolicies returns all policies on an identity.
func (m *Mock) GetEmailIdentityPolicies(_ context.Context, identity string) (map[string]string, error) {
	d, err := m.getIdentity(identity)
	if err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := make(map[string]string, len(d.id.Policies))
	for k, v := range d.id.Policies {
		out[k] = v
	}

	return out, nil
}

// UpdateEmailIdentityPolicy replaces a policy on an identity.
func (m *Mock) UpdateEmailIdentityPolicy(_ context.Context, identity, policyName, policy string) error {
	d, err := m.getIdentity(identity)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.id.Policies == nil {
		d.id.Policies = make(map[string]string)
	}

	d.id.Policies[policyName] = policy

	return nil
}

// DeleteEmailIdentityPolicy removes a policy from an identity.
func (m *Mock) DeleteEmailIdentityPolicy(_ context.Context, identity, policyName string) error {
	d, err := m.getIdentity(identity)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.id.Policies, policyName)

	return nil
}

// PutEmailIdentityConfigurationSetAttributes sets the identity's config set.
func (m *Mock) PutEmailIdentityConfigurationSetAttributes(_ context.Context, identity, configSet string) error {
	d, err := m.getIdentity(identity)
	if err != nil {
		return err
	}

	if configSet != "" && !m.configSetExists(configSet) {
		return errConfigSetNotFound(configSet)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.id.ConfigurationSetName = configSet

	return nil
}

// PutEmailIdentityDkimSigningAttributes sets the DKIM origin and returns tokens.
func (m *Mock) PutEmailIdentityDkimSigningAttributes(_ context.Context, identity, origin string) ([]string, error) {
	d, err := m.getIdentity(identity)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if origin == "" {
		origin = driver.DkimOriginAWSSES
	}

	d.id.DkimSigningOrigin = origin
	d.id.DkimStatus = driver.StatusSuccess
	d.id.DkimTokens = dkimTokens(identity)

	return append([]string(nil), d.id.DkimTokens...), nil
}

// PutEmailIdentityFeedbackAttributes toggles feedback forwarding.
func (m *Mock) PutEmailIdentityFeedbackAttributes(_ context.Context, identity string, forwardingEnabled bool) error {
	d, err := m.getIdentity(identity)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.id.FeedbackForwardingStatus = forwardingEnabled

	return nil
}
