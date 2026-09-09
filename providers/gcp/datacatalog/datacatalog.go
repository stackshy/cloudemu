// Package datacatalog provides an in-memory mock of the Google Cloud Data
// Catalog control plane (datacatalog.googleapis.com/v1). It models the nested
// registration resources — entry groups, their entries, each entry's tags, and
// the tag templates those tags reference — with synchronous REST CRUD (no
// long-running operations).
//
// The only computed fields are resource names (minted once, stable across
// reads), each tag template field's own name, and a tag's derived
// templateDisplayName / per-field displayName+order. Deleting a parent cascades
// to its descendants: removing an entry group removes its entries and every tag
// beneath it; removing an entry removes its tags; force-removing a tag template
// removes its dependent tags — matching real Data Catalog.
package datacatalog

import (
	"context"
	"strings"
	"sync"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

var _ dcdriver.DataCatalog = (*Mock)(nil)

const (
	entryGroupsColl   = "entryGroups"
	entriesColl       = "entries"
	tagsColl          = "tags"
	tagTemplatesColl  = "tagTemplates"
	tagTemplateFields = "fields"

	// mask field paths a Patch honors.
	maskDisplayName = "displayName"
	maskDescription = "description"
)

// Mock is the in-memory Data Catalog control-plane implementation. The four
// collections are separate stores, each keyed by its full GCP resource name, so
// a prefix scan cascades a parent delete to its descendants.
type Mock struct {
	mu sync.RWMutex

	entryGroups  *memstore.Store[dcdriver.EntryGroup]
	entries      *memstore.Store[dcdriver.Entry]
	tagTemplates *memstore.Store[dcdriver.TagTemplate]
	tags         *memstore.Store[dcdriver.Tag]

	opts *config.Options
}

// New creates a new Data Catalog mock.
func New(opts *config.Options) *Mock {
	return &Mock{
		entryGroups:  memstore.New[dcdriver.EntryGroup](),
		entries:      memstore.New[dcdriver.Entry](),
		tagTemplates: memstore.New[dcdriver.TagTemplate](),
		tags:         memstore.New[dcdriver.Tag](),
		opts:         opts,
	}
}

func locName(project, location string) string {
	return "projects/" + project + "/locations/" + location
}

func egName(project, location, eg string) string {
	return locName(project, location) + "/" + entryGroupsColl + "/" + eg
}

func entryName(project, location, eg, entry string) string {
	return egName(project, location, eg) + "/" + entriesColl + "/" + entry
}

func ttName(project, location, tt string) string {
	return locName(project, location) + "/" + tagTemplatesColl + "/" + tt
}

// tagName builds the full name of a tag. entry is empty when the tag hangs off
// the entry group itself.
func tagName(project, location, eg, entry, id string) string {
	if entry == "" {
		return egName(project, location, eg) + "/" + tagsColl + "/" + id
	}

	return entryName(project, location, eg, entry) + "/" + tagsColl + "/" + id
}

func notFound(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, name)
}

// CreateEntryGroup provisions a new entry group.
func (m *Mock) CreateEntryGroup(_ context.Context, cfg *dcdriver.EntryGroupConfig) (*dcdriver.EntryGroup, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "entryGroupId is required")
	}

	if cfg.Location == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "location is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := egName(cfg.Project, cfg.Location, cfg.ID)
	if m.entryGroups.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "entry group %q already exists", key)
	}

	eg := dcdriver.EntryGroup{
		Project: cfg.Project, Location: cfg.Location, ID: cfg.ID,
		DisplayName: cfg.DisplayName, Description: cfg.Description,
	}
	m.entryGroups.Set(key, eg)

	out := eg

	return &out, nil
}

// GetEntryGroup returns an entry group by identity.
func (m *Mock) GetEntryGroup(_ context.Context, project, location, id string) (*dcdriver.EntryGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	eg, ok := m.entryGroups.Get(egName(project, location, id))
	if !ok {
		return nil, notFound("entry group", egName(project, location, id))
	}

	out := eg

	return &out, nil
}

// ListEntryGroups returns every entry group in a project+location, id-ordered.
func (m *Mock) ListEntryGroups(_ context.Context, project, location string) ([]dcdriver.EntryGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := locName(project, location) + "/" + entryGroupsColl + "/"
	all := m.entryGroups.SortedValues()
	out := make([]dcdriver.EntryGroup, 0, len(all))

	for i := range all {
		if strings.HasPrefix(egName(all[i].Project, all[i].Location, all[i].ID), prefix) {
			out = append(out, all[i])
		}
	}

	return out, nil
}

// PatchEntryGroup applies a masked update over displayName/description.
func (m *Mock) PatchEntryGroup(_ context.Context, cfg *dcdriver.EntryGroupConfig, mask []string) (*dcdriver.EntryGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := egName(cfg.Project, cfg.Location, cfg.ID)

	eg, ok := m.entryGroups.Get(key)
	if !ok {
		return nil, notFound("entry group", key)
	}

	if masked(mask, maskDisplayName) {
		eg.DisplayName = cfg.DisplayName
	}

	if masked(mask, maskDescription) {
		eg.Description = cfg.Description
	}

	m.entryGroups.Set(key, eg)

	out := eg

	return &out, nil
}

// DeleteEntryGroup removes an entry group and cascades to its entries and every
// tag beneath it.
func (m *Mock) DeleteEntryGroup(_ context.Context, project, location, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := egName(project, location, id)
	if !m.entryGroups.Has(key) {
		return notFound("entry group", key)
	}

	m.entryGroups.Delete(key)
	deletePrefixed(m.entries, key+"/"+entriesColl+"/")
	deletePrefixed(m.tags, key+"/")

	return nil
}

// CreateEntry provisions a new entry under an existing entry group.
func (m *Mock) CreateEntry(_ context.Context, cfg *dcdriver.EntryConfig) (*dcdriver.Entry, error) {
	if cfg.ID == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "entryId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	parent := egName(cfg.Project, cfg.Location, cfg.EntryGroup)
	if !m.entryGroups.Has(parent) {
		return nil, notFound("entry group", parent)
	}

	key := entryName(cfg.Project, cfg.Location, cfg.EntryGroup, cfg.ID)
	if m.entries.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "entry %q already exists", key)
	}

	entry := entryFromConfig(cfg)
	m.entries.Set(key, entry)

	out := cloneEntry(&entry)

	return &out, nil
}

// GetEntry returns an entry by identity.
func (m *Mock) GetEntry(_ context.Context, project, location, entryGroup, id string) (*dcdriver.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := entryName(project, location, entryGroup, id)

	entry, ok := m.entries.Get(key)
	if !ok {
		return nil, notFound("entry", key)
	}

	out := cloneEntry(&entry)

	return &out, nil
}

// ListEntries returns every entry under an entry group, id-ordered.
func (m *Mock) ListEntries(_ context.Context, project, location, entryGroup string) ([]dcdriver.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := egName(project, location, entryGroup) + "/" + entriesColl + "/"
	all := m.entries.SortedValues()
	out := make([]dcdriver.Entry, 0, len(all))

	for i := range all {
		key := entryName(all[i].Project, all[i].Location, all[i].EntryGroup, all[i].ID)
		if strings.HasPrefix(key, prefix) {
			out = append(out, cloneEntry(&all[i]))
		}
	}

	return out, nil
}

// PatchEntry applies a masked update over an entry's mutable fields.
func (m *Mock) PatchEntry(_ context.Context, cfg *dcdriver.EntryConfig, mask []string) (*dcdriver.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := entryName(cfg.Project, cfg.Location, cfg.EntryGroup, cfg.ID)

	entry, ok := m.entries.Get(key)
	if !ok {
		return nil, notFound("entry", key)
	}

	applyEntryMask(&entry, cfg, mask)
	m.entries.Set(key, entry)

	out := cloneEntry(&entry)

	return &out, nil
}

// DeleteEntry removes an entry and cascades to its tags.
func (m *Mock) DeleteEntry(_ context.Context, project, location, entryGroup, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := entryName(project, location, entryGroup, id)
	if !m.entries.Has(key) {
		return notFound("entry", key)
	}

	m.entries.Delete(key)
	deletePrefixed(m.tags, key+"/"+tagsColl+"/")

	return nil
}

// entryFromConfig builds a stored entry from a create config.
func entryFromConfig(cfg *dcdriver.EntryConfig) dcdriver.Entry {
	return dcdriver.Entry{
		Project: cfg.Project, Location: cfg.Location, EntryGroup: cfg.EntryGroup, ID: cfg.ID,
		DisplayName:         cfg.DisplayName,
		Description:         cfg.Description,
		Type:                cfg.Type,
		UserSpecifiedType:   cfg.UserSpecifiedType,
		UserSpecifiedSystem: cfg.UserSpecifiedSystem,
		LinkedResource:      cfg.LinkedResource,
		Schema:              cloneRaw(cfg.Schema),
		GcsFilesetSpec:      cloneRaw(cfg.GcsFilesetSpec),
	}
}

// applyEntryMask folds the masked mutable fields from cfg into entry. An empty
// mask replaces every mutable field (lenient full update). Type,
// user_specified_type, user_specified_system, linked_resource, and
// gcs_fileset_spec are immutable after creation in real Data Catalog and are not
// remapped here.
func applyEntryMask(entry *dcdriver.Entry, cfg *dcdriver.EntryConfig, mask []string) {
	if masked(mask, maskDisplayName) {
		entry.DisplayName = cfg.DisplayName
	}

	if masked(mask, maskDescription) {
		entry.Description = cfg.Description
	}

	if masked(mask, "schema") {
		entry.Schema = cloneRaw(cfg.Schema)
	}
}

// masked reports whether field is targeted by the updateMask: an empty mask is a
// lenient full update (every field), otherwise the field must be listed (its
// leading path segment matching).
func masked(mask []string, field string) bool {
	if len(mask) == 0 {
		return true
	}

	for _, p := range mask {
		seg := p
		if i := strings.IndexByte(p, '.'); i >= 0 {
			seg = p[:i]
		}

		if seg == field {
			return true
		}
	}

	return false
}

// deletePrefixed removes every entry in s whose key starts with prefix.
func deletePrefixed[V any](s *memstore.Store[V], prefix string) {
	for _, k := range s.Keys() {
		if strings.HasPrefix(k, prefix) {
			s.Delete(k)
		}
	}
}

// newTagID mints a server-assigned tag id.
func newTagID() string { return idgen.UUID() }
