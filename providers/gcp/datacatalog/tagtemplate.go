package datacatalog

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

// primitiveTypes is the set of primitive_type values Data Catalog accepts for a
// tag template field.
//
//nolint:gochecknoglobals // immutable lookup set for field-type validation.
var primitiveTypes = map[string]struct{}{
	"DOUBLE": {}, "STRING": {}, "BOOL": {}, "TIMESTAMP": {}, "RICHTEXT": {},
}

// validateField reports whether a tag template field declares exactly one valid
// type: a recognized primitive_type, or an enum with at least one allowed value.
func validateField(id string, f *dcdriver.TagTemplateField) error {
	hasEnum := len(f.EnumValues) > 0
	_, hasPrimitive := primitiveTypes[f.PrimitiveType]

	if hasPrimitive == hasEnum {
		return cerrors.Newf(cerrors.InvalidArgument,
			"tag template field %q must declare exactly one of primitiveType or enumType", id)
	}

	return nil
}

// CreateTagTemplate provisions a new tag template. Every field must declare a
// valid type; a template must contain at least one field.
func (m *Mock) CreateTagTemplate(_ context.Context, cfg *dcdriver.TagTemplateConfig) (*dcdriver.TagTemplate, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "tagTemplateId is required")
	}

	if len(cfg.Fields) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "a tag template must contain at least one field")
	}

	for id, f := range cfg.Fields {
		if err := validateField(id, &f); err != nil {
			return nil, err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(cfg.Project, cfg.Location, cfg.ID)
	if m.tagTemplates.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "tag template %q already exists", key)
	}

	tt := dcdriver.TagTemplate{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID,
		DisplayName: cfg.DisplayName,
		Fields:      cloneFields(cfg.Fields),
	}
	m.tagTemplates.Set(key, tt)

	out := cloneTagTemplate(&tt)

	return &out, nil
}

// GetTagTemplate returns a tag template by identity.
func (m *Mock) GetTagTemplate(_ context.Context, project, location, id string) (*dcdriver.TagTemplate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tt, ok := m.tagTemplates.Get(ttName(project, location, id))
	if !ok {
		return nil, notFound("tag template", ttName(project, location, id))
	}

	out := cloneTagTemplate(&tt)

	return &out, nil
}

// PatchTagTemplate applies a masked update. Only displayName is mutable at the
// template level; field changes go through the fields sub-collection.
func (m *Mock) PatchTagTemplate(_ context.Context, cfg *dcdriver.TagTemplateConfig, mask []string) (*dcdriver.TagTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(cfg.Project, cfg.Location, cfg.ID)

	tt, ok := m.tagTemplates.Get(key)
	if !ok {
		return nil, notFound("tag template", key)
	}

	if masked(mask, maskDisplayName) {
		tt.DisplayName = cfg.DisplayName
	}

	m.tagTemplates.Set(key, tt)

	out := cloneTagTemplate(&tt)

	return &out, nil
}

// DeleteTagTemplate removes a tag template. Without force, a template that still
// has dependent tags is rejected; with force, those tags are removed too.
func (m *Mock) DeleteTagTemplate(_ context.Context, project, location, id string, force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(project, location, id)
	if !m.tagTemplates.Has(key) {
		return notFound("tag template", key)
	}

	dependents := m.tagsUsingTemplate(key)
	if len(dependents) > 0 && !force {
		return cerrors.Newf(cerrors.FailedPrecondition,
			"tag template %q still has %d dependent tag(s); set force=true to delete", key, len(dependents))
	}

	for _, tagKey := range dependents {
		m.tags.Delete(tagKey)
	}

	m.tagTemplates.Delete(key)

	return nil
}

// CreateTagTemplateField adds a new field to an existing template.
func (m *Mock) CreateTagTemplateField(_ context.Context, cfg *dcdriver.TagTemplateFieldConfig) (*dcdriver.TagTemplate, error) {
	if cfg.FieldID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "tagTemplateFieldId is required")
	}

	if err := validateField(cfg.FieldID, &cfg.Field); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(cfg.Project, cfg.Location, cfg.Template)

	tt, ok := m.tagTemplates.Get(key)
	if !ok {
		return nil, notFound("tag template", key)
	}

	if _, exists := tt.Fields[cfg.FieldID]; exists {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "tag template field %q already exists", cfg.FieldID)
	}

	tt.Fields = cloneFields(tt.Fields)
	tt.Fields[cfg.FieldID] = cloneField(&cfg.Field)
	m.tagTemplates.Set(key, tt)

	out := cloneTagTemplate(&tt)

	return &out, nil
}

// PatchTagTemplateField updates an existing field's mutable attributes.
func (m *Mock) PatchTagTemplateField(
	_ context.Context, cfg *dcdriver.TagTemplateFieldConfig, mask []string,
) (*dcdriver.TagTemplate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(cfg.Project, cfg.Location, cfg.Template)

	tt, ok := m.tagTemplates.Get(key)
	if !ok {
		return nil, notFound("tag template", key)
	}

	cur, exists := tt.Fields[cfg.FieldID]
	if !exists {
		return nil, notFound("tag template field", key+"/"+tagTemplateFields+"/"+cfg.FieldID)
	}

	applyFieldMask(&cur, &cfg.Field, mask)

	tt.Fields = cloneFields(tt.Fields)
	tt.Fields[cfg.FieldID] = cur
	m.tagTemplates.Set(key, tt)

	out := cloneTagTemplate(&tt)

	return &out, nil
}

// DeleteTagTemplateField removes one field from a template. A template must keep
// at least one field; force is accepted for parity but a field with dependent
// tag values still requires it.
func (m *Mock) DeleteTagTemplateField(_ context.Context, project, location, template, fieldID string, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := ttName(project, location, template)

	tt, ok := m.tagTemplates.Get(key)
	if !ok {
		return notFound("tag template", key)
	}

	if _, exists := tt.Fields[fieldID]; !exists {
		return notFound("tag template field", key+"/"+tagTemplateFields+"/"+fieldID)
	}

	if len(tt.Fields) == 1 {
		return cerrors.New(cerrors.FailedPrecondition, "cannot delete the last field of a tag template")
	}

	tt.Fields = cloneFields(tt.Fields)
	delete(tt.Fields, fieldID)
	m.tagTemplates.Set(key, tt)

	return nil
}

// applyFieldMask folds the masked mutable attributes from src into dst. An empty
// mask replaces every mutable attribute (masked treats it as a full update).
func applyFieldMask(dst, src *dcdriver.TagTemplateField, mask []string) {
	if masked(mask, maskDisplayName) {
		dst.DisplayName = src.DisplayName
	}

	if masked(mask, maskDescription) {
		dst.Description = src.Description
	}

	if masked(mask, "isRequired") {
		dst.IsRequired = src.IsRequired
	}

	if masked(mask, "type") {
		dst.PrimitiveType = src.PrimitiveType
		dst.EnumValues = cloneStrSlice(src.EnumValues)
	}

	if masked(mask, "order") {
		dst.Order = src.Order
	}
}

// tagsUsingTemplate returns the store keys of every tag whose template is name.
// The caller holds m.mu.
func (m *Mock) tagsUsingTemplate(name string) []string {
	var out []string

	for _, k := range m.tags.Keys() {
		t, ok := m.tags.Get(k)
		if ok && t.Template == name {
			out = append(out, k)
		}
	}

	return out
}
