package datacatalog

import dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"

// cloneRaw deep-copies an opaque JSON block so a stored value is never aliased
// by one handed back to a caller. An empty block clones to nil.
func cloneRaw(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}

	out := make([]byte, len(in))
	copy(out, in)

	return out
}

// cloneStrSlice deep-copies a string slice; an empty slice clones to nil.
func cloneStrSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

// cloneEntry returns a deep copy of e.
func cloneEntry(e *dcdriver.Entry) dcdriver.Entry {
	out := *e
	out.Schema = cloneRaw(e.Schema)
	out.GcsFilesetSpec = cloneRaw(e.GcsFilesetSpec)

	return out
}

// cloneField returns a deep copy of one tag template field.
func cloneField(f *dcdriver.TagTemplateField) dcdriver.TagTemplateField {
	out := *f
	out.EnumValues = cloneStrSlice(f.EnumValues)

	return out
}

// cloneFields deep-copies a tag template's field map; an empty map clones to nil.
func cloneFields(in map[string]dcdriver.TagTemplateField) map[string]dcdriver.TagTemplateField {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]dcdriver.TagTemplateField, len(in))
	for k, v := range in {
		out[k] = cloneField(&v)
	}

	return out
}

// cloneTagTemplate returns a deep copy of tt.
func cloneTagTemplate(tt *dcdriver.TagTemplate) dcdriver.TagTemplate {
	out := *tt
	out.Fields = cloneFields(tt.Fields)

	return out
}

// cloneTagFieldValue returns a deep copy of one tag field value, duplicating
// every set pointer so the stored value shares no memory with the returned one.
func cloneTagFieldValue(v dcdriver.TagFieldValue) dcdriver.TagFieldValue {
	out := v

	if v.StringValue != nil {
		s := *v.StringValue
		out.StringValue = &s
	}

	if v.BoolValue != nil {
		b := *v.BoolValue
		out.BoolValue = &b
	}

	if v.DoubleValue != nil {
		d := *v.DoubleValue
		out.DoubleValue = &d
	}

	if v.TimestampValue != nil {
		t := *v.TimestampValue
		out.TimestampValue = &t
	}

	if v.RichtextValue != nil {
		r := *v.RichtextValue
		out.RichtextValue = &r
	}

	if v.EnumValue != nil {
		e := *v.EnumValue
		out.EnumValue = &e
	}

	return out
}

// cloneTagFields deep-copies a tag's field map; an empty map clones to nil.
func cloneTagFields(in map[string]dcdriver.TagFieldValue) map[string]dcdriver.TagFieldValue {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]dcdriver.TagFieldValue, len(in))
	for k, v := range in {
		out[k] = cloneTagFieldValue(v)
	}

	return out
}

// cloneTag returns a deep copy of t.
func cloneTag(t *dcdriver.Tag) dcdriver.Tag {
	out := *t
	out.Fields = cloneTagFields(t.Fields)

	return out
}
