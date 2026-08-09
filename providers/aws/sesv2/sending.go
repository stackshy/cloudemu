package sesv2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// SendEmail validates the from-identity and recipients, records the message,
// and returns a generated MessageId.
//
//nolint:gocritic // in is a large value struct; kept by value to match the driver interface.
func (m *Mock) SendEmail(_ context.Context, in driver.SendEmailInput) (string, error) {
	if in.FromAddress == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "FromEmailAddress is required")
	}

	if err := m.checkFromIdentity(in.FromAddress); err != nil {
		return "", err
	}

	if len(in.ToAddresses)+len(in.CcAddresses)+len(in.BccAddresses) == 0 {
		return "", cerrors.New(cerrors.InvalidArgument, "at least one destination is required")
	}

	if in.ConfigurationSetName != "" && !m.configSetExists(in.ConfigurationSetName) {
		return "", errConfigSetNotFound(in.ConfigurationSetName)
	}

	if in.TemplateName != "" {
		if _, err := m.getTemplate(in.TemplateName); err != nil {
			return "", err
		}
	}

	msgID := idgen.GenerateID("")

	msg := driver.SentMessage{
		MessageID:            msgID,
		FromAddress:          in.FromAddress,
		ToAddresses:          append([]string(nil), in.ToAddresses...),
		CcAddresses:          append([]string(nil), in.CcAddresses...),
		BccAddresses:         append([]string(nil), in.BccAddresses...),
		Subject:              in.Subject,
		ConfigurationSetName: in.ConfigurationSetName,
		TemplateName:         in.TemplateName,
		SentAt:               m.now(),
	}

	m.sentMu.Lock()
	m.sent = append(m.sent, msg)
	m.sentMu.Unlock()

	return msgID, nil
}

// checkFromIdentity ensures the sending identity (the address, or its domain) is
// a verified identity.
func (m *Mock) checkFromIdentity(from string) error {
	if d, ok := m.identities.Get(from); ok {
		return verifiedForSending(d)
	}

	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain := from[at+1:]
		if d, ok := m.identities.Get(domain); ok {
			return verifiedForSending(d)
		}
	}

	return cerrors.Newf(cerrors.NotFound,
		"email address %q is not verified. The following identities failed the check: %s", from, from)
}

func verifiedForSending(d *identityData) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.id.VerifiedForSendingStatus {
		return cerrors.Newf(cerrors.FailedPrecondition, "identity %q is not verified for sending", d.id.Name)
	}

	return nil
}

// SendBulkEmail sends one templated message per entry, validating the shared
// from-identity, template, and configuration set once, then recording an
// accepted message per entry and returning the per-entry result.
//
//nolint:gocritic // in is a large value struct; kept by value to match the driver interface.
func (m *Mock) SendBulkEmail(_ context.Context, in driver.SendBulkEmailInput) ([]driver.BulkEmailResult, error) {
	if in.FromAddress == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "FromEmailAddress is required")
	}

	if err := m.checkFromIdentity(in.FromAddress); err != nil {
		return nil, err
	}

	if in.TemplateName != "" {
		if _, err := m.getTemplate(in.TemplateName); err != nil {
			return nil, err
		}
	}

	if in.ConfigurationSetName != "" && !m.configSetExists(in.ConfigurationSetName) {
		return nil, errConfigSetNotFound(in.ConfigurationSetName)
	}

	results := make([]driver.BulkEmailResult, 0, len(in.Entries))

	for i := range in.Entries {
		results = append(results, m.recordBulkEntry(&in, &in.Entries[i]))
	}

	return results, nil
}

func (m *Mock) recordBulkEntry(in *driver.SendBulkEmailInput, e *driver.BulkEmailEntry) driver.BulkEmailResult {
	msgID := idgen.GenerateID("")

	m.sentMu.Lock()
	m.sent = append(m.sent, driver.SentMessage{
		MessageID:            msgID,
		FromAddress:          in.FromAddress,
		ToAddresses:          append([]string(nil), e.ToAddresses...),
		CcAddresses:          append([]string(nil), e.CcAddresses...),
		BccAddresses:         append([]string(nil), e.BccAddresses...),
		ConfigurationSetName: in.ConfigurationSetName,
		TemplateName:         in.TemplateName,
		SentAt:               m.now(),
	})
	m.sentMu.Unlock()

	return driver.BulkEmailResult{Status: "SUCCESS", MessageID: msgID}
}

// ListSentMessages returns a copy of all recorded sent messages.
func (m *Mock) ListSentMessages(_ context.Context) []driver.SentMessage {
	m.sentMu.RLock()
	defer m.sentMu.RUnlock()

	out := make([]driver.SentMessage, len(m.sent))
	copy(out, m.sent)

	return out
}
