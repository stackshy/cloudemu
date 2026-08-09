package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateCustomVerificationEmailTemplate registers a custom verification template.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) CreateCustomVerificationEmailTemplate(
	_ context.Context, in driver.CustomVerificationEmailTemplateInput,
) error {
	if in.TemplateName == "" {
		return cerrors.New(cerrors.InvalidArgument, "TemplateName is required")
	}

	t := &driver.CustomVerificationEmailTemplate{
		TemplateName:       in.TemplateName,
		FromEmailAddress:   in.FromEmailAddress,
		TemplateSubject:    in.TemplateSubject,
		TemplateContent:    in.TemplateContent,
		SuccessRedirectURL: in.SuccessRedirectURL,
		FailureRedirectURL: in.FailureRedirectURL,
		CreatedAt:          m.now(),
	}

	if !m.cvTemplates.SetIfAbsent(in.TemplateName, t) {
		return cerrors.Newf(cerrors.AlreadyExists, "custom verification template %q already exists", in.TemplateName)
	}

	return nil
}

// GetCustomVerificationEmailTemplate returns a custom verification template.
func (m *Mock) GetCustomVerificationEmailTemplate(
	_ context.Context, name string,
) (*driver.CustomVerificationEmailTemplate, error) {
	t, ok := m.cvTemplates.Get(name)
	if !ok {
		return nil, errCVTemplateNotFound(name)
	}

	out := *t

	return &out, nil
}

// UpdateCustomVerificationEmailTemplate replaces a custom verification template.
//
//nolint:gocritic // in is passed by value to match the driver interface.
func (m *Mock) UpdateCustomVerificationEmailTemplate(
	_ context.Context, in driver.CustomVerificationEmailTemplateInput,
) error {
	ok := m.cvTemplates.Update(in.TemplateName, func(t *driver.CustomVerificationEmailTemplate) *driver.CustomVerificationEmailTemplate {
		t.FromEmailAddress = in.FromEmailAddress
		t.TemplateSubject = in.TemplateSubject
		t.TemplateContent = in.TemplateContent
		t.SuccessRedirectURL = in.SuccessRedirectURL
		t.FailureRedirectURL = in.FailureRedirectURL

		return t
	})
	if !ok {
		return errCVTemplateNotFound(in.TemplateName)
	}

	return nil
}

// DeleteCustomVerificationEmailTemplate removes a custom verification template.
func (m *Mock) DeleteCustomVerificationEmailTemplate(_ context.Context, name string) error {
	if !m.cvTemplates.Delete(name) {
		return errCVTemplateNotFound(name)
	}

	return nil
}

// ListCustomVerificationEmailTemplates returns all custom verification templates.
func (m *Mock) ListCustomVerificationEmailTemplates(
	_ context.Context,
) ([]driver.CustomVerificationEmailTemplate, error) {
	all := m.cvTemplates.SortedValues()
	out := make([]driver.CustomVerificationEmailTemplate, 0, len(all))

	for _, t := range all {
		out = append(out, *t)
	}

	return out, nil
}

// SendCustomVerificationEmail records a verification email send and returns a
// generated MessageId. The template must exist.
func (m *Mock) SendCustomVerificationEmail(
	_ context.Context, templateName, emailAddress, configSet string,
) (string, error) {
	if _, ok := m.cvTemplates.Get(templateName); !ok {
		return "", errCVTemplateNotFound(templateName)
	}

	if emailAddress == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "EmailAddress is required")
	}

	if configSet != "" && !m.configSetExists(configSet) {
		return "", errConfigSetNotFound(configSet)
	}

	msgID := idgen.GenerateID("")

	m.sentMu.Lock()
	m.sent = append(m.sent, driver.SentMessage{
		MessageID:            msgID,
		ToAddresses:          []string{emailAddress},
		ConfigurationSetName: configSet,
		TemplateName:         templateName,
		SentAt:               m.now(),
	})
	m.sentMu.Unlock()

	return msgID, nil
}

func errCVTemplateNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "custom verification template %q does not exist", name)
}
