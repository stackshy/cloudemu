package compute

import (
	"encoding/json"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// InstanceTemplate is the in-memory record of a global GCE instance template
// (compute#instanceTemplate). Templates are immutable in GCP, so the request
// body is kept as-is in Spec and echoed on read; only the fields the emulator
// computes (id, creation time) are modeled. Host-dependent links are built by
// the wire handler.
type InstanceTemplate struct {
	Name      string          `json:"name"`
	Spec      json.RawMessage `json:"spec,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
}

// CreateInstanceTemplateGCP stores a template, rejecting a duplicate name.
func (m *Mock) CreateInstanceTemplateGCP(t InstanceTemplate) error {
	if t.Name == "" {
		return cerrors.New(cerrors.InvalidArgument, "instance template name is required")
	}

	if t.CreatedAt == "" {
		t.CreatedAt = m.opts.Clock.Now().UTC().Format(timeFormat)
	}

	t.Spec = append(json.RawMessage(nil), t.Spec...)

	if !m.instTemplates.SetIfAbsent(t.Name, t) {
		return cerrors.Newf(cerrors.AlreadyExists, "The resource 'global/instanceTemplates/%s' already exists", t.Name)
	}

	return nil
}

// GetInstanceTemplateGCP returns a template by name.
func (m *Mock) GetInstanceTemplateGCP(name string) (InstanceTemplate, bool) {
	t, ok := m.instTemplates.Get(name)
	if ok {
		t.Spec = append(json.RawMessage(nil), t.Spec...)
	}

	return t, ok
}

// ListInstanceTemplatesGCP returns every template, sorted by name.
func (m *Mock) ListInstanceTemplatesGCP() []InstanceTemplate {
	all := m.instTemplates.All()
	out := make([]InstanceTemplate, 0, len(all))

	for _, t := range all {
		t.Spec = append(json.RawMessage(nil), t.Spec...)
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// DeleteInstanceTemplateGCP removes a template. A template a managed instance
// group still uses cannot be deleted, as in GCP.
func (m *Mock) DeleteInstanceTemplateGCP(name string) error {
	if _, ok := m.instTemplates.Get(name); !ok {
		return cerrors.Newf(cerrors.NotFound, "The resource 'global/instanceTemplates/%s' was not found", name)
	}

	migs := m.migs.All()
	for key := range migs {
		if migs[key].InstanceTemplate == name {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"The instance_template resource '%s' is already being used by '%s'", name, migs[key].Name)
		}
	}

	m.instTemplates.Delete(name)

	return nil
}
