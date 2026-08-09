package sesv2

import (
	"context"
	"encoding/json"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

func copyTemplate(t *driver.Template) driver.Template {
	out := *t
	out.Tags = copyTags(t.Tags)

	return out
}

// CreateEmailTemplate registers an email template.
func (m *Mock) CreateEmailTemplate(_ context.Context, in driver.TemplateInput) error {
	if in.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "TemplateName is required")
	}

	tpl := driver.Template{
		Name:      in.Name,
		Content:   in.Content,
		CreatedAt: m.now(),
		Tags:      copyTags(in.Tags),
	}

	if !m.templates.SetIfAbsent(in.Name, &templateData{tpl: tpl}) {
		return cerrors.Newf(cerrors.AlreadyExists, "email template %q already exists", in.Name)
	}

	return nil
}

// GetEmailTemplate returns a template by name.
func (m *Mock) GetEmailTemplate(_ context.Context, name string) (*driver.Template, error) {
	d, err := m.getTemplate(name)
	if err != nil {
		return nil, err
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	out := copyTemplate(&d.tpl)

	return &out, nil
}

// UpdateEmailTemplate replaces the content of an existing template.
func (m *Mock) UpdateEmailTemplate(_ context.Context, in driver.TemplateInput) error {
	d, err := m.getTemplate(in.Name)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.tpl.Content = in.Content

	return nil
}

// DeleteEmailTemplate removes a template.
func (m *Mock) DeleteEmailTemplate(_ context.Context, name string) error {
	if !m.templates.Delete(name) {
		return errTemplateNotFound(name)
	}

	return nil
}

// ListEmailTemplates returns all templates ordered by name.
func (m *Mock) ListEmailTemplates(_ context.Context) ([]driver.Template, error) {
	all := m.templates.SortedValues()
	out := make([]driver.Template, 0, len(all))

	for _, d := range all {
		d.mu.RLock()
		out = append(out, copyTemplate(&d.tpl))
		d.mu.RUnlock()
	}

	return out, nil
}

// TestRenderEmailTemplate renders a template against JSON template data by
// substituting {{key}} placeholders in the subject, HTML, and text parts and
// returns the combined rendered output.
func (m *Mock) TestRenderEmailTemplate(_ context.Context, name, templateData string) (string, error) {
	d, err := m.getTemplate(name)
	if err != nil {
		return "", err
	}

	d.mu.RLock()
	content := d.tpl.Content
	d.mu.RUnlock()

	vars, err := parseTemplateData(templateData)
	if err != nil {
		return "", err
	}

	subject := renderTemplate(content.Subject, vars)
	body := renderTemplate(content.HTML, vars)

	if body == "" {
		body = renderTemplate(content.Text, vars)
	}

	return "Subject: " + subject + "\n\n" + body, nil
}

func (m *Mock) getTemplate(name string) (*templateData, error) {
	d, ok := m.templates.Get(name)
	if !ok {
		return nil, errTemplateNotFound(name)
	}

	return d, nil
}

// parseTemplateData decodes the JSON template-data object into a string map.
func parseTemplateData(data string) (map[string]string, error) {
	if data == "" {
		return map[string]string{}, nil
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid template data: %v", err)
	}

	out := make(map[string]string, len(raw))

	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}

		if b, mErr := json.Marshal(v); mErr == nil {
			out[k] = string(b)
		}
	}

	return out, nil
}

// renderTemplate substitutes {{key}} placeholders with values.
func renderTemplate(tmpl string, vars map[string]string) string {
	for k, v := range vars {
		tmpl = strings.ReplaceAll(tmpl, "{{"+k+"}}", v)
	}

	return tmpl
}

func errTemplateNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "email template %q does not exist", name)
}
