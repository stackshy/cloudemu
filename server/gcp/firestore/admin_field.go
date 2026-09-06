package firestore

import (
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// TTL configuration state. CloudEmu applies TTL synchronously, so an enabled
// ttlConfig reports ACTIVE immediately (real Firestore transitions through
// CREATING first).
const ttlStateActive = "ACTIVE"

// defaultFieldPath is the collection-group default field ("*"), whose index
// config every unconfigured field in the group inherits from.
const defaultFieldPath = "*"

// defaultCollectionGroup is the special collection group holding the database's
// default field indexing settings.
const defaultCollectionGroup = "__default__"

// fieldIndexRec is one index within a field's IndexConfig: a query scope plus
// the ordered/array field specs (reusing indexFieldRec from the index surface).
type fieldIndexRec struct {
	queryScope string
	fields     []indexFieldRec
}

// fieldRecord is the stored per-field index/TTL configuration for one
// collectionGroups/{cg}/fields/{fieldPath}. A field is stored only once it is
// explicitly configured; an unconfigured field is synthesized on read as
// inheriting the ancestor (__default__) config.
type fieldRecord struct {
	project        string
	database       string
	collGroup      string
	fieldPath      string
	hasIndexConfig bool // an explicit indexConfig was set (even if its index list is empty)
	indexes        []fieldIndexRec
	ttlEnabled     bool
}

// parseFieldPath parses .../collectionGroups/{cg}/fields[/{fieldPath}] into p. A
// field path is a single URL segment (dots and the "*" wildcard do not split
// it). It returns false for any malformed shape.
func parseFieldPath(parts []string, p *adminPath) bool {
	const (
		collLen  = 7 // projects/p/databases/db/collectionGroups/cg/fields
		fieldLen = 8 // projects/p/databases/db/collectionGroups/cg/fields/{f}
		kind     = 6
	)

	if len(parts) != collLen && len(parts) != fieldLen {
		return false
	}

	if parts[kind] != segFields {
		return false
	}

	p.collGroup = parts[5]

	if len(parts) == collLen {
		p.isFieldColl = true
		return true
	}

	p.field = parts[7]

	return true
}

// fieldKey is the store key (and resource name) for a field resource.
func fieldKey(project, database, collGroup, fieldPath string) string {
	return "projects/" + project + "/databases/" + database +
		"/collectionGroups/" + collGroup + "/fields/" + fieldPath
}

// serveFieldCollection handles the fields collection: GET (list). Fields have no
// create verb — they come into existence via patch.
func (h *AdminHandler) serveFieldCollection(w http.ResponseWriter, r *http.Request, p *adminPath) {
	if r.Method != http.MethodGet {
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
		return
	}

	h.listFields(w, p)
}

// serveFieldResource handles a single field: GET and PATCH.
func (h *AdminHandler) serveFieldResource(w http.ResponseWriter, r *http.Request, p *adminPath) {
	switch r.Method {
	case http.MethodGet:
		h.getField(w, p)
	case http.MethodPatch:
		h.patchField(w, r, p)
	default:
		gcprest.WriteError(w, http.StatusMethodNotAllowed, "methodNotAllowed", "method not allowed")
	}
}

// getField implements FirestoreAdmin.GetField. A field with no stored
// configuration is not an error: real Firestore returns the field showing that
// it inherits the ancestor (__default__) config, so an unconfigured field is
// synthesized here rather than 404'd.
func (h *AdminHandler) getField(w http.ResponseWriter, p *adminPath) {
	rec, ok := h.fields.Get(fieldKey(p.project, p.database, p.collGroup, p.field))
	if !ok {
		rec = fieldRecord{project: p.project, database: p.database, collGroup: p.collGroup, fieldPath: p.field}
	}

	gcprest.WriteJSON(w, http.StatusOK, renderField(&rec))
}

// listFields implements FirestoreAdmin.ListFields for a collection group. Only
// fields with an explicit (non-default) configuration are stored, which matches
// the real API's default of listing fields that have been configured.
//
//nolint:dupl // structurally parallel to listIndexes (prefix scan of a distinct store); merging would couple unrelated resources.
func (h *AdminHandler) listFields(w http.ResponseWriter, p *adminPath) {
	prefix := "projects/" + p.project + "/databases/" + p.database +
		"/collectionGroups/" + p.collGroup + "/fields/"

	var out []map[string]any

	for _, key := range h.fields.Keys() {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		if rec, ok := h.fields.Get(key); ok {
			out = append(out, renderField(&rec))
		}
	}

	gcprest.WriteJSON(w, http.StatusOK, map[string]any{"fields": out})
}

// patchField implements FirestoreAdmin.UpdateField (an LRO). The database must
// exist. Fields named in updateMask (indexConfig / ttlConfig) — or, absent a
// mask, every one present in the body — are applied. Clearing indexConfig
// reverts the field to the ancestor config; the operation resolves done:true
// with the updated Field.
func (h *AdminHandler) patchField(w http.ResponseWriter, r *http.Request, p *adminPath) {
	if !h.databases.Has(dbKey(p.project, p.database)) {
		gcprest.WriteCErr(w, cerrors.Newf(cerrors.NotFound, "database %q not found", p.database))
		return
	}

	body, ok := decodeAdminBody(w, r)
	if !ok {
		return
	}

	mask := splitMask(r.URL.Query().Get("updateMask"))

	key := fieldKey(p.project, p.database, p.collGroup, p.field)

	updated, err := h.applyFieldPatch(key, p, body, mask)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	metadata := map[string]any{
		"@type": "type.googleapis.com/google.firestore.admin.v1.FieldOperationMetadata",
		"field": key,
	}
	h.writeDoneOperationMeta(w, p.project, p.database, fieldAnyResponse(&updated), metadata)
}

// applyFieldPatch mutates (or creates) the stored field under key per body+mask
// and returns the new record, as a single store Update so concurrent patches
// don't lose writes.
func (h *AdminHandler) applyFieldPatch(
	key string, p *adminPath, body map[string]any, mask map[string]bool,
) (fieldRecord, error) {
	current, ok := h.fields.Get(key)
	if !ok {
		current = fieldRecord{project: p.project, database: p.database, collGroup: p.collGroup, fieldPath: p.field}
	}

	next, err := patchFieldRecord(&current, body, mask)
	if err != nil {
		return fieldRecord{}, err
	}

	h.fields.Set(key, next)

	return next, nil
}

// patchFieldRecord applies indexConfig and ttlConfig from body (guided by mask,
// or all present fields when mask is nil) to a copy of cur and returns it.
func patchFieldRecord(cur *fieldRecord, body map[string]any, mask map[string]bool) (fieldRecord, error) {
	next := *cur

	if maskWants(mask, "indexConfig", body) {
		if cfg, ok := body["indexConfig"].(map[string]any); ok {
			idxs, err := parseFieldIndexes(cfg["indexes"])
			if err != nil {
				return fieldRecord{}, err
			}

			next.hasIndexConfig = true
			next.indexes = idxs
		} else {
			// Cleared indexConfig: revert to the ancestor (default) config.
			next.hasIndexConfig = false
			next.indexes = nil
		}
	}

	if maskWants(mask, "ttlConfig", body) {
		raw, present := body["ttlConfig"]
		next.ttlEnabled = present && raw != nil
	}

	return next, nil
}

// parseFieldIndexes normalizes the indexes array of a field's IndexConfig. Each
// entry is an Index with its own queryScope and ordered/array fields.
func parseFieldIndexes(raw any) ([]fieldIndexRec, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, nil
	}

	out := make([]fieldIndexRec, 0, len(list))

	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, cerrors.New(cerrors.InvalidArgument, "invalid index config index")
		}

		fi := fieldIndexRec{queryScope: queryScopeCollection}

		if rawScope, present := m["queryScope"]; present {
			v, err := enumValue(rawScope, queryScopeByInt, "queryScope")
			if err != nil {
				return nil, err
			}

			fi.queryScope = v
		}

		fields, err := parseIndexFields(m["fields"])
		if err != nil {
			return nil, err
		}

		fi.fields = fields
		out = append(out, fi)
	}

	return out, nil
}

// renderField builds the JSON map for a Field resource. A field with no explicit
// indexConfig reports that it inherits the ancestor (__default__) config.
func renderField(rec *fieldRecord) map[string]any {
	out := map[string]any{
		"name": fieldKey(rec.project, rec.database, rec.collGroup, rec.fieldPath),
	}

	if rec.hasIndexConfig {
		idxs := make([]map[string]any, 0, len(rec.indexes))
		for i := range rec.indexes {
			idxs = append(idxs, renderFieldIndex(&rec.indexes[i]))
		}

		out["indexConfig"] = map[string]any{
			"indexes":            idxs,
			"usesAncestorConfig": false,
		}
	} else {
		out["indexConfig"] = map[string]any{
			"usesAncestorConfig": true,
			"ancestorField":      fieldKey(rec.project, rec.database, defaultCollectionGroup, defaultFieldPath),
		}
	}

	if rec.ttlEnabled {
		out["ttlConfig"] = map[string]any{"state": ttlStateActive}
	}

	return out
}

// renderFieldIndex builds the JSON map for one Index embedded in a field's
// IndexConfig (no standalone name; state is READY since CloudEmu builds
// synchronously).
func renderFieldIndex(fi *fieldIndexRec) map[string]any {
	return map[string]any{
		"queryScope": fi.queryScope,
		"fields":     renderIndexFields(fi.fields),
		"state":      indexStateReady,
	}
}

// fieldAnyResponse wraps a rendered field as an Operation response Any.
func fieldAnyResponse(rec *fieldRecord) map[string]any {
	out := renderField(rec)
	out["@type"] = "type.googleapis.com/google.firestore.admin.v1.Field"

	return out
}
