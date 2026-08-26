package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

func copyIdentity(id *driver.Identity) driver.Identity {
	out := *id
	out.Tags = copyTags(id.Tags)
	out.DkimTokens = append([]string(nil), id.DkimTokens...)
	out.Policies = copyTags(id.Policies)

	return out
}

// CreateEmailIdentity registers an identity. In the emulator an identity
// auto-verifies to SUCCESS immediately, and domains get Easy DKIM tokens.
func (m *Mock) CreateEmailIdentity(_ context.Context, in driver.CreateIdentityInput) (*driver.Identity, error) {
	if in.EmailIdentity == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "EmailIdentity is required")
	}

	isDomain := isDomainIdentity(in.EmailIdentity)

	id := driver.Identity{
		Name:                     in.EmailIdentity,
		Type:                     identityKind(isDomain),
		VerificationStatus:       driver.StatusSuccess,
		VerifiedForSendingStatus: true,
		FeedbackForwardingStatus: true,
		ConfigurationSetName:     in.ConfigurationSetName,
		DkimStatus:               driver.StatusSuccess,
		DkimSigningEnabled:       true,
		DkimSigningOrigin:        driver.DkimOriginAWSSES,
		CreatedAt:                m.now(),
		Tags:                     copyTags(in.Tags),
	}

	if isDomain {
		id.DkimTokens = dkimTokens(in.EmailIdentity)
		id.DkimSigningHostedZn = m.opts.Region
	}

	if !m.identities.SetIfAbsent(in.EmailIdentity, &identityData{id: id}) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "email identity %q already exists", in.EmailIdentity)
	}

	out := copyIdentity(&id)

	return &out, nil
}

func identityKind(isDomain bool) string {
	if isDomain {
		return driver.IdentityTypeDomain
	}

	return driver.IdentityTypeEmailAddress
}

// GetEmailIdentity returns the identity by name.
func (m *Mock) GetEmailIdentity(_ context.Context, name string) (*driver.Identity, error) {
	d, err := m.getIdentity(name)
	if err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := copyIdentity(&d.id)

	return &out, nil
}

// DeleteEmailIdentity removes an identity.
func (m *Mock) DeleteEmailIdentity(_ context.Context, name string) error {
	if !m.identities.Delete(name) {
		return errIdentityNotFound(name)
	}

	return nil
}

// ListEmailIdentities returns all identities ordered by name.
func (m *Mock) ListEmailIdentities(_ context.Context) ([]driver.Identity, error) {
	all := m.identities.SortedValues()
	out := make([]driver.Identity, 0, len(all))

	for _, d := range all {
		d.mu.RLock()
		out = append(out, copyIdentity(&d.id))
		d.mu.RUnlock()
	}

	return out, nil
}

// PutEmailIdentityDkimAttributes toggles DKIM signing for an identity.
func (m *Mock) PutEmailIdentityDkimAttributes(_ context.Context, name string, signingEnabled bool) error {
	d, err := m.getIdentity(name)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.id.DkimSigningEnabled = signingEnabled

	return nil
}

// PutEmailIdentityMailFromAttributes sets a custom MAIL FROM domain.
func (m *Mock) PutEmailIdentityMailFromAttributes(_ context.Context, name, mailFromDomain, behaviorOnMxFail string) error {
	d, err := m.getIdentity(name)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.id.MailFromDomain = mailFromDomain
	d.id.MailFromBehaviorOnMxFail = behaviorOnMxFail

	if mailFromDomain == "" {
		d.id.MailFromDomainStatus = ""
	} else {
		d.id.MailFromDomainStatus = driver.StatusSuccess
	}

	return nil
}

func (m *Mock) getIdentity(name string) (*identityData, error) {
	d, ok := m.identities.Get(name)
	if !ok {
		return nil, errIdentityNotFound(name)
	}

	return d, nil
}

func errIdentityNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "email identity %q does not exist", name)
}
