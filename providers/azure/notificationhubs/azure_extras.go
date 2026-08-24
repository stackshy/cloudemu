package notificationhubs

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Compile-time check that Mock implements the Azure-only optional surface.
var _ driver.AzureNotificationHubs = (*Mock)(nil)

const sasCompositeSep = "\x00"

// sasRuleKey builds the composite store key for a SAS rule.
func sasRuleKey(resourceKey, ruleName string) string {
	return resourceKey + sasCompositeSep + ruleName
}

// regKey builds the composite store key for a device registration.
func regKey(hubKey, registrationID string) string {
	return hubKey + sasCompositeSep + registrationID
}

// deterministicKey derives a stable base64 256-bit key from its inputs so
// repeated ListKeys calls return the same value without persisting the key at
// create time.
func deterministicKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, sasCompositeSep)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// --- namespace SKU ---

// SetNamespaceMeta records the namespace SKU.
func (m *Mock) SetNamespaceMeta(_ context.Context, namespace string, meta driver.AzureNamespaceMeta) error {
	if namespace == "" {
		return errors.New(errors.InvalidArgument, "namespace is required")
	}

	m.nsMeta.Set(namespace, meta)

	return nil
}

// GetNamespaceMeta returns the namespace SKU, or a zero value when unset.
func (m *Mock) GetNamespaceMeta(_ context.Context, namespace string) (driver.AzureNamespaceMeta, error) {
	meta, ok := m.nsMeta.Get(namespace)
	if !ok {
		return driver.AzureNamespaceMeta{}, nil
	}

	return meta, nil
}

// --- SAS authorization rules ---

// PutSASRule creates or updates a Shared Access authorization rule. Keys are
// generated deterministically on first create and preserved on update.
func (m *Mock) PutSASRule(
	_ context.Context, resourceKey, ruleName string, rule driver.AzureSASRule,
) (driver.AzureSASRule, error) {
	if ruleName == "" {
		return driver.AzureSASRule{}, errors.New(errors.InvalidArgument, "authorization rule name is required")
	}

	key := sasRuleKey(resourceKey, ruleName)

	if existing, ok := m.sasRules.Get(key); ok {
		existing.Rights = rule.Rights
		m.sasRules.Set(key, existing)

		return existing, nil
	}

	rule.PrimaryKey = deterministicKey(resourceKey, ruleName, "primary")
	rule.SecondaryKey = deterministicKey(resourceKey, ruleName, "secondary")
	m.sasRules.Set(key, rule)

	return rule, nil
}

// GetSASRule returns a Shared Access authorization rule.
func (m *Mock) GetSASRule(_ context.Context, resourceKey, ruleName string) (driver.AzureSASRule, error) {
	rule, ok := m.sasRules.Get(sasRuleKey(resourceKey, ruleName))
	if !ok {
		return driver.AzureSASRule{}, errors.Newf(errors.NotFound, "authorization rule %q not found", ruleName)
	}

	return rule, nil
}

// ListSASRules returns every authorization rule on the resource, keyed by rule
// name.
func (m *Mock) ListSASRules(_ context.Context, resourceKey string) (map[string]driver.AzureSASRule, error) {
	prefix := resourceKey + sasCompositeSep

	out := make(map[string]driver.AzureSASRule)
	for _, k := range m.sasRules.Keys() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		rule, ok := m.sasRules.Get(k)
		if !ok {
			continue
		}

		out[strings.TrimPrefix(k, prefix)] = rule
	}

	return out, nil
}

// DeleteSASRule removes a Shared Access authorization rule.
func (m *Mock) DeleteSASRule(_ context.Context, resourceKey, ruleName string) error {
	if !m.sasRules.Delete(sasRuleKey(resourceKey, ruleName)) {
		return errors.Newf(errors.NotFound, "authorization rule %q not found", ruleName)
	}

	return nil
}

// --- data-plane device registrations ---

// CreateRegistration stores a new device registration under the hub, assigning
// a registration id and ETag.
func (m *Mock) CreateRegistration(
	_ context.Context, hubKey string, reg driver.AzureRegistration,
) (driver.AzureRegistration, error) {
	if reg.RegistrationID == "" {
		reg.RegistrationID = idgen.GenerateID("")
	}

	reg.ETag = "1"
	m.registrations.Set(regKey(hubKey, reg.RegistrationID), reg)

	return reg, nil
}

// GetRegistration returns a device registration by id.
func (m *Mock) GetRegistration(
	_ context.Context, hubKey, registrationID string,
) (driver.AzureRegistration, error) {
	reg, ok := m.registrations.Get(regKey(hubKey, registrationID))
	if !ok {
		return driver.AzureRegistration{}, errors.Newf(errors.NotFound, "registration %q not found", registrationID)
	}

	return reg, nil
}

// ListRegistrations returns every registration under the hub.
func (m *Mock) ListRegistrations(_ context.Context, hubKey string) ([]driver.AzureRegistration, error) {
	prefix := hubKey + sasCompositeSep

	var out []driver.AzureRegistration

	for _, k := range m.registrations.Keys() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}

		if reg, ok := m.registrations.Get(k); ok {
			out = append(out, reg)
		}
	}

	return out, nil
}

// DeleteRegistration removes a device registration.
func (m *Mock) DeleteRegistration(_ context.Context, hubKey, registrationID string) error {
	if !m.registrations.Delete(regKey(hubKey, registrationID)) {
		return errors.Newf(errors.NotFound, "registration %q not found", registrationID)
	}

	return nil
}
