package cognito

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Default password-policy values for an API-created user pool.
const (
	defaultMinimumLength     = 8
	defaultTempPasswordValid = 7
	maxPoolNameLen           = 128
)

// CreateUserPool creates a user pool, generating its id and ARN, seeding the 20
// default schema attributes, and materializing the real Cognito defaults
// (password policy, MFA OFF, deletion protection INACTIVE, ESSENTIALS tier).
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) CreateUserPool(_ context.Context, in driver.CreateUserPoolInput) (*driver.UserPool, error) {
	if in.Name == "" || len(in.Name) > maxPoolNameLen {
		return nil, invalidParameter("user pool name %q is invalid", in.Name)
	}

	id := newPoolID(m.opts.Region)
	now := m.now()

	pool := driver.UserPool{
		ID:                     id,
		Name:                   in.Name,
		ARN:                    m.userPoolARN(id),
		Policies:               resolvePolicies(in.Policies),
		MFAConfiguration:       orDefault(in.MFAConfiguration, driver.MFAConfigurationOff),
		DeletionProtection:     orDefault(in.DeletionProtection, driver.DeletionProtectionInactive),
		UserPoolTier:           driver.UserPoolTierEssentials,
		EstimatedNumberOfUsers: 0,
		SchemaAttributes:       mergeSchema(in.SchemaAttributes),
		AutoVerifiedAttributes: copyStringSlice(in.AutoVerifiedAttributes),
		AliasAttributes:        copyStringSlice(in.AliasAttributes),
		UsernameAttributes:     copyStringSlice(in.UsernameAttributes),
		CreationDate:           now,
		LastModifiedDate:       now,
	}

	// Tags live only in the ARN-keyed side map (the source of truth for the tag
	// operations); the stored pool never carries them, and reads refresh Tags
	// from the side map so the two never diverge.
	m.userPools.Set(id, copyUserPool(pool))

	if len(in.UserPoolTags) > 0 {
		m.storeTags(pool.ARN, in.UserPoolTags)
	}

	out := copyUserPool(pool)
	out.Tags = m.currentTags(pool.ARN)

	return &out, nil
}

// DescribeUserPool returns a deep copy of a user pool.
func (m *Mock) DescribeUserPool(_ context.Context, id string) (*driver.UserPool, error) {
	pool, ok := m.userPools.Get(id)
	if !ok {
		return nil, resourceNotFound("User pool %s does not exist.", id)
	}

	out := copyUserPool(pool)
	out.Tags = m.currentTags(pool.ARN)

	return &out, nil
}

// UpdateUserPool applies the mutable pool settings. A nil/empty field is left
// unchanged; a non-nil UserPoolTags replaces the pool's tag set.
//
//nolint:gocritic // hugeParam: taken by value to match the driver interface
func (m *Mock) UpdateUserPool(_ context.Context, in driver.UpdateUserPoolInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(in.ID)
	if !ok {
		return resourceNotFound("User pool %s does not exist.", in.ID)
	}

	pool = copyUserPool(pool)

	if in.Policies != nil {
		pool.Policies = resolvePolicies(in.Policies)
	}

	if in.MFAConfiguration != "" {
		pool.MFAConfiguration = in.MFAConfiguration
	}

	if in.DeletionProtection != "" {
		pool.DeletionProtection = in.DeletionProtection
	}

	if in.AutoVerifiedAttributes != nil {
		pool.AutoVerifiedAttributes = copyStringSlice(in.AutoVerifiedAttributes)
	}

	if in.UserPoolTags != nil {
		m.replaceTags(pool.ARN, in.UserPoolTags)
	}

	pool.LastModifiedDate = m.now()
	m.userPools.Set(in.ID, pool)

	return nil
}

// DeleteUserPool removes a user pool along with its clients, domains, and tags.
func (m *Mock) DeleteUserPool(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool, ok := m.userPools.Get(id)
	if !ok {
		return resourceNotFound("User pool %s does not exist.", id)
	}

	for _, key := range m.clients.Keys() {
		if c, ok := m.clients.Get(key); ok && c.UserPoolID == id {
			m.clients.Delete(key)
		}
	}

	for _, domain := range m.domains.Keys() {
		if d, ok := m.domains.Get(domain); ok && d.UserPoolID == id {
			m.domains.Delete(domain)
		}
	}

	m.userPools.Delete(id)
	m.deleteTags(pool.ARN)

	return nil
}

// ListUserPools returns pool descriptions sorted by id.
func (m *Mock) ListUserPools(_ context.Context, page driver.Pagination) ([]driver.UserPoolDescription, string, error) {
	ids := sortedKeys(m.userPools.Keys())
	all := make([]driver.UserPoolDescription, 0, len(ids))

	for _, id := range ids {
		pool, ok := m.userPools.Get(id)
		if !ok {
			continue
		}

		all = append(all, driver.UserPoolDescription{
			ID:               pool.ID,
			Name:             pool.Name,
			CreationDate:     pool.CreationDate,
			LastModifiedDate: pool.LastModifiedDate,
		})
	}

	return paginate(all, page)
}

// resolvePolicies materializes the password-policy defaults over the caller's
// input. When no policy is supplied, the full default policy is returned.
func resolvePolicies(in *driver.Policies) driver.Policies {
	if in == nil {
		return driver.Policies{PasswordPolicy: defaultPasswordPolicy()}
	}

	pp := in.PasswordPolicy
	if pp.MinimumLength == 0 {
		pp.MinimumLength = defaultMinimumLength
	}

	if pp.TemporaryPasswordValidityDays == 0 {
		pp.TemporaryPasswordValidityDays = defaultTempPasswordValid
	}

	return driver.Policies{PasswordPolicy: pp}
}

// defaultPasswordPolicy is the policy an API-created pool gets when the caller
// supplies none: length 8, all character classes required, 7-day temp validity.
func defaultPasswordPolicy() driver.PasswordPolicy {
	return driver.PasswordPolicy{
		MinimumLength:                 defaultMinimumLength,
		RequireUppercase:              true,
		RequireLowercase:              true,
		RequireNumbers:                true,
		RequireSymbols:                true,
		TemporaryPasswordValidityDays: defaultTempPasswordValid,
	}
}

// orDefault returns v when non-empty, else def.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// mergeSchema returns the 20 default attributes followed by any caller-supplied
// custom attributes (prefixed "dev:" for developer-only, else "custom:"),
// matching how Cognito names non-standard attributes.
func mergeSchema(custom []driver.SchemaAttribute) []driver.SchemaAttribute {
	attrs := defaultSchemaAttributes()

	for _, a := range custom {
		if isStandardAttribute(a.Name) {
			continue
		}

		a.Name = customPrefix(a.DeveloperOnlyAttribute) + a.Name
		a.StringAttributeConstraints = copyStringConstraints(a.StringAttributeConstraints)
		a.NumberAttributeConstraints = copyNumberConstraints(a.NumberAttributeConstraints)
		attrs = append(attrs, a)
	}

	return attrs
}

// customPrefix returns the attribute-name prefix Cognito applies to a
// non-standard attribute.
func customPrefix(developerOnly bool) string {
	if developerOnly {
		return "dev:"
	}

	return "custom:"
}

// isStandardAttribute reports whether name is one of the 20 default attributes.
func isStandardAttribute(name string) bool {
	for _, a := range defaultSchemaAttributes() {
		if a.Name == name {
			return true
		}
	}

	return false
}
