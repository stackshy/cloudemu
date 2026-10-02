package compute

import (
	"encoding/json"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/projectctx"
)

// InstanceTemplate is the in-memory record of a global GCE instance template
// (compute#instanceTemplate). Templates are immutable in GCP, so the request
// body is kept as-is in Spec and echoed on read; only the fields the emulator
// computes (id, creation time) are modeled. Host-dependent links are built by
// the wire handler.
type InstanceTemplate struct {
	Project   string          `json:"project,omitempty"`
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
	t.Project = m.orDefault(t.Project)

	if !m.instTemplates.SetIfAbsent(projectctx.Key(t.Project, t.Name), t) {
		return cerrors.Newf(cerrors.AlreadyExists, "The resource 'global/instanceTemplates/%s' already exists", t.Name)
	}

	return nil
}

// GetInstanceTemplateGCP returns a template of project by name. An empty
// project is the default project.
func (m *Mock) GetInstanceTemplateGCP(project, name string) (InstanceTemplate, bool) {
	t, ok := m.instTemplates.Get(projectctx.Key(m.orDefault(project), name))
	if ok {
		t.Spec = append(json.RawMessage(nil), t.Spec...)
	}

	return t, ok
}

// ListInstanceTemplatesGCP returns every template of project, sorted by name.
func (m *Mock) ListInstanceTemplatesGCP(project string) []InstanceTemplate {
	all := m.instTemplates.All()
	out := make([]InstanceTemplate, 0, len(all))
	project = m.orDefault(project)

	for _, t := range all {
		if t.Project != project {
			continue
		}

		t.Spec = append(json.RawMessage(nil), t.Spec...)
		out = append(out, t)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// DeleteInstanceTemplateGCP removes a template. A template a managed instance
// group still uses cannot be deleted, as in GCP.
func (m *Mock) DeleteInstanceTemplateGCP(project, name string) error {
	project = m.orDefault(project)
	key := projectctx.Key(project, name)

	if _, ok := m.instTemplates.Get(key); !ok {
		return cerrors.Newf(cerrors.NotFound, "The resource 'global/instanceTemplates/%s' was not found", name)
	}

	migs := m.migs.All()
	for key := range migs {
		if migs[key].Project == project && migs[key].InstanceTemplate == name {
			return cerrors.Newf(cerrors.FailedPrecondition,
				"The instance_template resource '%s' is already being used by '%s'", name, migs[key].Name)
		}
	}

	m.instTemplates.Delete(key)

	return nil
}
