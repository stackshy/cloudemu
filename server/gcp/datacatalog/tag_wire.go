package datacatalog

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

// tagBody is the wire shape of a tag create/patch request.
type tagBody struct {
	Name     string             `json:"name"`
	Template string             `json:"template"`
	Column   string             `json:"column"`
	Fields   map[string]tagFldB `json:"fields"`
}

// tagFldB is the wire shape of one tag field value. Exactly one value pointer is
// set on the wire, selecting the field's type.
type tagFldB struct {
	StringValue    *string      `json:"stringValue"`
	BoolValue      *bool        `json:"boolValue"`
	DoubleValue    *float64     `json:"doubleValue"`
	TimestampValue *string      `json:"timestampValue"`
	RichtextValue  *string      `json:"richtextValue"`
	EnumValue      *enumValLitB `json:"enumValue"`
}

// enumValLitB is the wire shape of a tag field's enum value.
type enumValLitB struct {
	DisplayName string `json:"displayName"`
}

// toValue converts a wire tag field into a driver value.
func (b *tagFldB) toValue() dcdriver.TagFieldValue {
	v := dcdriver.TagFieldValue{
		StringValue: b.StringValue, BoolValue: b.BoolValue, DoubleValue: b.DoubleValue,
		TimestampValue: b.TimestampValue, RichtextValue: b.RichtextValue,
	}

	if b.EnumValue != nil {
		e := b.EnumValue.DisplayName
		v.EnumValue = &e
	}

	return v
}

// toFields converts a wire tag field map into a driver field map.
func (b *tagBody) toFields() map[string]dcdriver.TagFieldValue {
	if len(b.Fields) == 0 {
		return nil
	}

	out := make(map[string]dcdriver.TagFieldValue, len(b.Fields))
	for id, f := range b.Fields {
		out[id] = f.toValue()
	}

	return out
}

// tagFieldValueJSON renders one tag field value plus its output-only
// displayName/order.
func tagFieldValueJSON(v *dcdriver.TagFieldValue) map[string]any {
	m := map[string]any{}

	switch {
	case v.StringValue != nil:
		m["stringValue"] = *v.StringValue
	case v.BoolValue != nil:
		m["boolValue"] = *v.BoolValue
	case v.DoubleValue != nil:
		m["doubleValue"] = *v.DoubleValue
	case v.TimestampValue != nil:
		m["timestampValue"] = *v.TimestampValue
	case v.RichtextValue != nil:
		m["richtextValue"] = *v.RichtextValue
	case v.EnumValue != nil:
		m["enumValue"] = map[string]any{"displayName": *v.EnumValue}
	}

	putIfSet(m, "displayName", v.DisplayName)

	if v.Order != 0 {
		m["order"] = v.Order
	}

	return m
}

// tagJSON renders a tag as datacatalog/v1 wire JSON, including its
// server-generated name and derived templateDisplayName.
func tagJSON(t *dcdriver.Tag) map[string]any {
	m := map[string]any{
		"name":     tagResourceName(t),
		"template": t.Template,
	}
	putIfSet(m, "templateDisplayName", t.TemplateDisplayName)
	putIfSet(m, "column", t.Column)

	if len(t.Fields) > 0 {
		fields := make(map[string]any, len(t.Fields))
		for id, v := range t.Fields {
			fields[id] = tagFieldValueJSON(&v)
		}

		m["fields"] = fields
	}

	return m
}

// tagResourceName builds a tag's full name, handling the entry-group-level case
// (empty entry).
func tagResourceName(t *dcdriver.Tag) string {
	if t.Entry == "" {
		return egResourceName(t.Project, t.Location, t.EntryGroup) + "/" + tagsSeg + "/" + t.ID
	}

	return entryResourceName(t.Project, t.Location, t.EntryGroup, t.Entry) + "/" + tagsSeg + "/" + t.ID
}

func (h *Handler) createTag(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagBody
	if !decodeBody(w, r, &body) {
		return
	}

	t, err := h.db.CreateTag(r.Context(), &dcdriver.TagConfig{
		Project: rt.project, Location: rt.location, EntryGroup: rt.eg, Entry: rt.entry,
		Template: body.Template, Column: body.Column, Fields: body.toFields(),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagJSON(t))
}

func (h *Handler) listTags(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListTags(r.Context(), &dcdriver.TagParent{
		Project: rt.project, Location: rt.location, EntryGroup: rt.eg, Entry: rt.entry,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, tagJSON(&all[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"tags": items})
}

func (h *Handler) patchTag(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagBody
	if !decodeBody(w, r, &body) {
		return
	}

	t, err := h.db.PatchTag(r.Context(), &dcdriver.TagConfig{
		Project: rt.project, Location: rt.location, EntryGroup: rt.eg, Entry: rt.entry, ID: rt.name,
		Template: body.Template, Column: body.Column, Fields: body.toFields(),
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagJSON(t))
}

func (h *Handler) deleteTag(w http.ResponseWriter, r *http.Request, rt *route) {
	err := h.db.DeleteTag(r.Context(), &dcdriver.TagParent{
		Project: rt.project, Location: rt.location, EntryGroup: rt.eg, Entry: rt.entry,
	}, rt.name)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}
