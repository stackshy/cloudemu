package datacatalog

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

// tagTemplateBody is the wire shape of a tag template create/patch request.
type tagTemplateBody struct {
	Name        string                       `json:"name"`
	DisplayName string                       `json:"displayName"`
	Fields      map[string]tagTemplateFieldB `json:"fields"`
}

// tagTemplateFieldB is the wire shape of one tag template field.
type tagTemplateFieldB struct {
	Name        string     `json:"name"`
	DisplayName string     `json:"displayName"`
	Description string     `json:"description"`
	IsRequired  bool       `json:"isRequired"`
	Order       int64      `json:"order"`
	Type        fieldTypeB `json:"type"`
}

// fieldTypeB is the wire shape of a tag template field's type.
type fieldTypeB struct {
	PrimitiveType string     `json:"primitiveType"`
	EnumType      *enumTypeB `json:"enumType"`
}

// enumTypeB is the wire shape of an enum field type.
type enumTypeB struct {
	AllowedValues []enumValueB `json:"allowedValues"`
}

// enumValueB is one allowed enum value.
type enumValueB struct {
	DisplayName string `json:"displayName"`
}

// toField converts a wire field body into a driver field.
func (b *tagTemplateFieldB) toField() dcdriver.TagTemplateField {
	f := dcdriver.TagTemplateField{
		DisplayName: b.DisplayName, Description: b.Description,
		IsRequired: b.IsRequired, Order: b.Order,
		PrimitiveType: b.Type.PrimitiveType,
	}

	if b.Type.EnumType != nil {
		f.EnumValues = make([]string, 0, len(b.Type.EnumType.AllowedValues))
		for _, v := range b.Type.EnumType.AllowedValues {
			f.EnumValues = append(f.EnumValues, v.DisplayName)
		}
	}

	return f
}

// toFields converts a wire field map into a driver field map.
func (b *tagTemplateBody) toFields() map[string]dcdriver.TagTemplateField {
	if len(b.Fields) == 0 {
		return nil
	}

	out := make(map[string]dcdriver.TagTemplateField, len(b.Fields))
	for id, f := range b.Fields {
		out[id] = f.toField()
	}

	return out
}

// fieldTypeJSON renders a field's type as wire JSON.
func fieldTypeJSON(f *dcdriver.TagTemplateField) map[string]any {
	if len(f.EnumValues) > 0 {
		allowed := make([]map[string]any, 0, len(f.EnumValues))
		for _, v := range f.EnumValues {
			allowed = append(allowed, map[string]any{"displayName": v})
		}

		return map[string]any{"enumType": map[string]any{"allowedValues": allowed}}
	}

	return map[string]any{"primitiveType": f.PrimitiveType}
}

// tagTemplateFieldJSON renders one tag template field, including its computed
// resource name.
func tagTemplateFieldJSON(ttName, id string, f *dcdriver.TagTemplateField) map[string]any {
	m := map[string]any{
		"name": ttName + "/" + fieldsSeg + "/" + id,
		"type": fieldTypeJSON(f),
	}
	putIfSet(m, "displayName", f.DisplayName)
	putIfSet(m, "description", f.Description)

	if f.IsRequired {
		m["isRequired"] = true
	}

	if f.Order != 0 {
		m["order"] = f.Order
	}

	return m
}

// tagTemplateJSON renders a tag template as datacatalog/v1 wire JSON.
func tagTemplateJSON(tt *dcdriver.TagTemplate) map[string]any {
	name := ttResourceName(tt.Project, tt.Location, tt.ID)
	m := map[string]any{"name": name}
	putIfSet(m, "displayName", tt.DisplayName)

	if len(tt.Fields) > 0 {
		fields := make(map[string]any, len(tt.Fields))
		for id, f := range tt.Fields {
			fields[id] = tagTemplateFieldJSON(name, id, &f)
		}

		m["fields"] = fields
	}

	return m
}

func (h *Handler) createTagTemplate(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagTemplateBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("tagTemplateId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	tt, err := h.db.CreateTagTemplate(r.Context(), &dcdriver.TagTemplateConfig{
		Project: rt.project, Location: rt.location, ID: id,
		DisplayName: body.DisplayName, Fields: body.toFields(),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagTemplateJSON(tt))
}

func (h *Handler) getTagTemplate(w http.ResponseWriter, r *http.Request, rt *route) {
	tt, err := h.db.GetTagTemplate(r.Context(), rt.project, rt.location, rt.tt)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagTemplateJSON(tt))
}

func (h *Handler) patchTagTemplate(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagTemplateBody
	if !decodeBody(w, r, &body) {
		return
	}

	tt, err := h.db.PatchTagTemplate(r.Context(), &dcdriver.TagTemplateConfig{
		Project: rt.project, Location: rt.location, ID: rt.tt,
		DisplayName: body.DisplayName, Fields: body.toFields(),
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagTemplateJSON(tt))
}

func (h *Handler) deleteTagTemplate(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteTagTemplate(r.Context(), rt.project, rt.location, rt.tt, forceParam(r)); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) createTagTemplateField(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagTemplateFieldB
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("tagTemplateFieldId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	tt, err := h.db.CreateTagTemplateField(r.Context(), &dcdriver.TagTemplateFieldConfig{
		Project: rt.project, Location: rt.location, Template: rt.tt, FieldID: id, Field: body.toField(),
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeField(w, tt, id)
}

func (h *Handler) patchTagTemplateField(w http.ResponseWriter, r *http.Request, rt *route) {
	var body tagTemplateFieldB
	if !decodeBody(w, r, &body) {
		return
	}

	tt, err := h.db.PatchTagTemplateField(r.Context(), &dcdriver.TagTemplateFieldConfig{
		Project: rt.project, Location: rt.location, Template: rt.tt, FieldID: rt.field, Field: body.toField(),
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	writeField(w, tt, rt.field)
}

func (h *Handler) deleteTagTemplateField(w http.ResponseWriter, r *http.Request, rt *route) {
	err := h.db.DeleteTagTemplateField(r.Context(), rt.project, rt.location, rt.tt, rt.field, forceParam(r))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// writeField emits the single field id from the returned template, matching the
// field sub-API's response shape (a TagTemplateField, not the whole template).
func writeField(w http.ResponseWriter, tt *dcdriver.TagTemplate, id string) {
	f, ok := tt.Fields[id]
	if !ok {
		gcprest.WriteError(w, http.StatusInternalServerError, "internal", "field missing after write")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, tagTemplateFieldJSON(ttResourceName(tt.Project, tt.Location, tt.ID), id, &f))
}
