package datacatalog

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

// maxBodyBytes caps a decoded request body.
const maxBodyBytes = 8 << 20

// resource-name builders (server side) — must match the driver's stable names.
func egResourceName(project, location, eg string) string {
	return "projects/" + project + "/locations/" + location + "/" + entryGroupsSeg + "/" + eg
}

func entryResourceName(project, location, eg, entry string) string {
	return egResourceName(project, location, eg) + "/" + entriesSeg + "/" + entry
}

func ttResourceName(project, location, tt string) string {
	return "projects/" + project + "/locations/" + location + "/" + tagTemplatesSeg + "/" + tt
}

// decodeBody reads and unmarshals the request body into v, tolerating an empty
// body. It writes a 400 and returns false on a malformed body.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "reading request body: "+err.Error())
		return false
	}

	if len(raw) == 0 {
		return true
	}

	if err := json.Unmarshal(raw, v); err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "malformed JSON body: "+err.Error())
		return false
	}

	return true
}

// parseMask splits a comma-separated updateMask query param into field paths.
func parseMask(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// forceParam reports whether the request carries force=true.
func forceParam(r *http.Request) bool {
	return r.URL.Query().Get("force") == "true"
}

// lastSegment returns the trailing path segment of a resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}

	return name
}

// entryGroupBody is the wire shape of an entry group create/patch request.
type entryGroupBody struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// entryBody is the wire shape of an entry create/patch request. Schema and
// gcsFilesetSpec are opaque blocks captured verbatim so they round-trip.
type entryBody struct {
	Name                string          `json:"name"`
	DisplayName         string          `json:"displayName"`
	Description         string          `json:"description"`
	Type                string          `json:"type"`
	UserSpecifiedType   string          `json:"userSpecifiedType"`
	UserSpecifiedSystem string          `json:"userSpecifiedSystem"`
	LinkedResource      string          `json:"linkedResource"`
	Schema              json.RawMessage `json:"schema"`
	GcsFilesetSpec      json.RawMessage `json:"gcsFilesetSpec"`
}

// entryGroupJSON renders an entry group as datacatalog/v1 wire JSON.
func entryGroupJSON(eg *dcdriver.EntryGroup) map[string]any {
	m := map[string]any{"name": egResourceName(eg.Project, eg.Location, eg.ID)}
	putIfSet(m, "displayName", eg.DisplayName)
	putIfSet(m, "description", eg.Description)

	return m
}

// entryJSON renders an entry as datacatalog/v1 wire JSON. Zero-value fields are
// omitted, matching the real API, so an unset field never drifts against a
// Terraform config that also leaves it unset. integratedSystem is output-only
// and unset for user-specified entries, so it is never emitted.
func entryJSON(e *dcdriver.Entry) map[string]any {
	m := map[string]any{"name": entryResourceName(e.Project, e.Location, e.EntryGroup, e.ID)}
	putIfSet(m, "displayName", e.DisplayName)
	putIfSet(m, "description", e.Description)
	putIfSet(m, "type", e.Type)
	putIfSet(m, "userSpecifiedType", e.UserSpecifiedType)
	putIfSet(m, "userSpecifiedSystem", e.UserSpecifiedSystem)
	putIfSet(m, "linkedResource", e.LinkedResource)

	if len(e.Schema) > 0 {
		m["schema"] = e.Schema
	}

	if len(e.GcsFilesetSpec) > 0 {
		m["gcsFilesetSpec"] = e.GcsFilesetSpec
	}

	return m
}

// putIfSet adds key→val to m only when val is non-empty.
func putIfSet(m map[string]any, key, val string) {
	if val != "" {
		m[key] = val
	}
}

func (h *Handler) createEntryGroup(w http.ResponseWriter, r *http.Request, rt *route) {
	var body entryGroupBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("entryGroupId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	eg, err := h.db.CreateEntryGroup(r.Context(), &dcdriver.EntryGroupConfig{
		Project: rt.project, Location: rt.location, ID: id,
		DisplayName: body.DisplayName, Description: body.Description,
	})
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryGroupJSON(eg))
}

func (h *Handler) getEntryGroup(w http.ResponseWriter, r *http.Request, rt *route) {
	eg, err := h.db.GetEntryGroup(r.Context(), rt.project, rt.location, rt.eg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryGroupJSON(eg))
}

func (h *Handler) listEntryGroups(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListEntryGroups(r.Context(), rt.project, rt.location)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, entryGroupJSON(&all[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"entryGroups": items})
}

func (h *Handler) patchEntryGroup(w http.ResponseWriter, r *http.Request, rt *route) {
	var body entryGroupBody
	if !decodeBody(w, r, &body) {
		return
	}

	eg, err := h.db.PatchEntryGroup(r.Context(), &dcdriver.EntryGroupConfig{
		Project: rt.project, Location: rt.location, ID: rt.eg,
		DisplayName: body.DisplayName, Description: body.Description,
	}, parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryGroupJSON(eg))
}

func (h *Handler) deleteEntryGroup(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteEntryGroup(r.Context(), rt.project, rt.location, rt.eg); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) createEntry(w http.ResponseWriter, r *http.Request, rt *route) {
	var body entryBody
	if !decodeBody(w, r, &body) {
		return
	}

	id := r.URL.Query().Get("entryId")
	if id == "" {
		id = lastSegment(body.Name)
	}

	e, err := h.db.CreateEntry(r.Context(), entryConfig(rt, id, &body))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryJSON(e))
}

func (h *Handler) getEntry(w http.ResponseWriter, r *http.Request, rt *route) {
	e, err := h.db.GetEntry(r.Context(), rt.project, rt.location, rt.eg, rt.entry)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryJSON(e))
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request, rt *route) {
	all, err := h.db.ListEntries(r.Context(), rt.project, rt.location, rt.eg)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	items := make([]map[string]any, 0, len(all))
	for i := range all {
		items = append(items, entryJSON(&all[i]))
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"entries": items})
}

func (h *Handler) patchEntry(w http.ResponseWriter, r *http.Request, rt *route) {
	var body entryBody
	if !decodeBody(w, r, &body) {
		return
	}

	e, err := h.db.PatchEntry(r.Context(), entryConfig(rt, rt.entry, &body),
		parseMask(r.URL.Query().Get("updateMask")))
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, entryJSON(e))
}

func (h *Handler) deleteEntry(w http.ResponseWriter, r *http.Request, rt *route) {
	if err := h.db.DeleteEntry(r.Context(), rt.project, rt.location, rt.eg, rt.entry); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// entryConfig builds an EntryConfig from a parsed route, an id, and a body.
func entryConfig(rt *route, id string, body *entryBody) *dcdriver.EntryConfig {
	return &dcdriver.EntryConfig{
		Project: rt.project, Location: rt.location, EntryGroup: rt.eg, ID: id,
		DisplayName:         body.DisplayName,
		Description:         body.Description,
		Type:                body.Type,
		UserSpecifiedType:   body.UserSpecifiedType,
		UserSpecifiedSystem: body.UserSpecifiedSystem,
		LinkedResource:      body.LinkedResource,
		Schema:              body.Schema,
		GcsFilesetSpec:      body.GcsFilesetSpec,
	}
}
