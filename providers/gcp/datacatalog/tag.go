package datacatalog

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

// CreateTag attaches a server-named tag to an entry (or entry group). The parent
// and the referenced template must exist, and every field value's type must
// match the template field's declared type.
func (m *Mock) CreateTag(_ context.Context, cfg *dcdriver.TagConfig) (*dcdriver.Tag, error) {
	if cfg.Template == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "template is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkTagParent(cfg.Project, cfg.Location, cfg.EntryGroup, cfg.Entry); err != nil {
		return nil, err
	}

	tt, ok := m.tagTemplates.Get(cfg.Template)
	if !ok {
		return nil, notFound("tag template", cfg.Template)
	}

	if err := validateTagFields(cfg.Fields, &tt); err != nil {
		return nil, err
	}

	tag := dcdriver.Tag{
		Project: cfg.Project, Location: cfg.Location, EntryGroup: cfg.EntryGroup, Entry: cfg.Entry,
		ID:       newTagID(),
		Template: cfg.Template,
		Column:   cfg.Column,
		Fields:   cloneTagFields(cfg.Fields),
	}
	m.tags.Set(tagName(tag.Project, tag.Location, tag.EntryGroup, tag.Entry, tag.ID), tag)

	out := resolveTag(&tag, &tt)

	return &out, nil
}

// ListTags returns every tag under a parent (an entry, or the entry group
// itself), id-ordered, with derived template-display fields populated.
func (m *Mock) ListTags(_ context.Context, parent *dcdriver.TagParent) ([]dcdriver.Tag, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := tagCollectionPrefix(parent)
	all := m.tags.SortedValues()
	out := make([]dcdriver.Tag, 0, len(all))

	for i := range all {
		key := tagName(all[i].Project, all[i].Location, all[i].EntryGroup, all[i].Entry, all[i].ID)
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		tt, _ := m.tagTemplates.Get(all[i].Template)
		out = append(out, resolveTag(&all[i], &tt))
	}

	return out, nil
}

// PatchTag applies a masked update over a tag's fields (the only mutable part;
// template and column are immutable). Updated values are re-validated.
func (m *Mock) PatchTag(_ context.Context, cfg *dcdriver.TagConfig, mask []string) (*dcdriver.Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tagName(cfg.Project, cfg.Location, cfg.EntryGroup, cfg.Entry, cfg.ID)

	tag, ok := m.tags.Get(key)
	if !ok {
		return nil, notFound("tag", key)
	}

	tt, ok := m.tagTemplates.Get(tag.Template)
	if !ok {
		return nil, notFound("tag template", tag.Template)
	}

	if masked(mask, "fields") {
		if err := validateTagFields(cfg.Fields, &tt); err != nil {
			return nil, err
		}

		tag.Fields = cloneTagFields(cfg.Fields)
	}

	m.tags.Set(key, tag)

	out := resolveTag(&tag, &tt)

	return &out, nil
}

// DeleteTag removes a single tag under a parent.
func (m *Mock) DeleteTag(_ context.Context, parent *dcdriver.TagParent, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tagName(parent.Project, parent.Location, parent.EntryGroup, parent.Entry, id)
	if !m.tags.Has(key) {
		return notFound("tag", key)
	}

	m.tags.Delete(key)

	return nil
}

// checkTagParent verifies the entry (or entry group) a tag attaches to exists.
// The caller holds m.mu.
func (m *Mock) checkTagParent(project, location, eg, entry string) error {
	if entry == "" {
		parent := egName(project, location, eg)
		if !m.entryGroups.Has(parent) {
			return notFound("entry group", parent)
		}

		return nil
	}

	parent := entryName(project, location, eg, entry)
	if !m.entries.Has(parent) {
		return notFound("entry", parent)
	}

	return nil
}

// tagCollectionPrefix builds the store-key prefix bounding one tag collection.
func tagCollectionPrefix(parent *dcdriver.TagParent) string {
	if parent.Entry == "" {
		return egName(parent.Project, parent.Location, parent.EntryGroup) + "/" + tagsColl + "/"
	}

	return entryName(parent.Project, parent.Location, parent.EntryGroup, parent.Entry) + "/" + tagsColl + "/"
}

// validateTagFields checks that every tag field references a template field and
// carries a value whose type matches the field's declared type, and that every
// required template field is present.
func validateTagFields(fields map[string]dcdriver.TagFieldValue, tt *dcdriver.TagTemplate) error {
	for name, v := range fields {
		def, ok := tt.Fields[name]
		if !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "field %q is not defined in tag template", name)
		}

		if err := checkValueType(name, &v, &def); err != nil {
			return err
		}
	}

	for id, def := range tt.Fields {
		if _, ok := fields[id]; def.IsRequired && !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "required tag field %q is missing", id)
		}
	}

	return nil
}

// checkValueType verifies a single tag field value matches its template field's
// declared type, and that an enum value is one of the allowed values.
func checkValueType(name string, v *dcdriver.TagFieldValue, def *dcdriver.TagTemplateField) error {
	if len(def.EnumValues) > 0 {
		return checkEnumValue(name, v, def)
	}

	wantSetter, ok := primitiveSetter(def.PrimitiveType, v)
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "field %q has unknown template type", name)
	}

	if !wantSetter {
		return cerrors.Newf(cerrors.InvalidArgument,
			"field %q value type does not match template type %s", name, def.PrimitiveType)
	}

	return nil
}

// primitiveSetter reports whether v carries a value of the primitive type prim,
// and whether prim is a recognized primitive type at all.
func primitiveSetter(prim string, v *dcdriver.TagFieldValue) (matches, known bool) {
	switch prim {
	case "STRING":
		return v.StringValue != nil, true
	case "DOUBLE":
		return v.DoubleValue != nil, true
	case "BOOL":
		return v.BoolValue != nil, true
	case "TIMESTAMP":
		return v.TimestampValue != nil, true
	case "RICHTEXT":
		return v.RichtextValue != nil, true
	default:
		return false, false
	}
}

// checkEnumValue verifies an enum-typed tag field carries an enum value listed
// in the template field's allowed values.
func checkEnumValue(name string, v *dcdriver.TagFieldValue, def *dcdriver.TagTemplateField) error {
	if v.EnumValue == nil {
		return cerrors.Newf(cerrors.InvalidArgument, "field %q requires an enumValue", name)
	}

	for _, allowed := range def.EnumValues {
		if allowed == *v.EnumValue {
			return nil
		}
	}

	return cerrors.Newf(cerrors.InvalidArgument, "field %q enum value %q is not an allowed value", name, *v.EnumValue)
}

// resolveTag returns a clone of t with output-only fields (templateDisplayName
// and each field's displayName/order) populated from the template tt.
func resolveTag(t *dcdriver.Tag, tt *dcdriver.TagTemplate) dcdriver.Tag {
	out := cloneTag(t)
	out.TemplateDisplayName = tt.DisplayName

	for name, v := range out.Fields {
		if def, ok := tt.Fields[name]; ok {
			v.DisplayName = def.DisplayName
			v.Order = def.Order
			out.Fields[name] = v
		}
	}

	return out
}
