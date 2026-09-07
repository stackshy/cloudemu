// Package driver defines the portable interface for the Google Cloud Data
// Catalog control plane (datacatalog.googleapis.com/v1). It is control-plane
// only — the nested, mostly client-named resource collections a Terraform
// google provider or a real google.golang.org/api/datacatalog/v1 client CRUDs
// are modeled:
//
//	projects/{p}/locations/{loc}/entryGroups/{eg}
//	projects/{p}/locations/{loc}/entryGroups/{eg}/entries/{entry}
//	projects/{p}/locations/{loc}/entryGroups/{eg}/entries/{entry}/tags/{tag}
//	projects/{p}/locations/{loc}/tagTemplates/{tt}
//	projects/{p}/locations/{loc}/tagTemplates/{tt}/fields/{field}
//
// Catalog search, lookupEntry, IAM policy, and the policyTags/taxonomies surface
// are out of scope: CloudEmu emulates the metadata registration control plane,
// not the discovery/classification data plane.
//
// Every Data Catalog mutation is synchronous REST: Create/Get/List/Patch/Delete
// return the resource (or empty) directly, with no google.longrunning.Operation
// wrapper — unlike most GCP control planes. No operation registry is involved.
//
// The only computed, output-only fields are resource names (the full
// deterministic path, minted once and stable across reads), each tag template
// field's own name, and a tag's templateDisplayName / per-field displayName +
// order, all derived stably from the referencing template. Every caller-supplied
// field (display names, descriptions, schema, gcsFilesetSpec, template field
// types, tag values) round-trips verbatim.
package driver

import (
	"context"
	"encoding/json"
)

// EntryGroup is one Data Catalog entry group. Name components are stored
// separately so the full resource name and parent scoping rebuild without
// re-parsing.
type EntryGroup struct {
	Project     string
	Location    string
	ID          string
	DisplayName string
	Description string
}

// Entry is one Data Catalog entry under an entry group. Schema and
// GcsFilesetSpec are opaque caller-supplied JSON blocks that round-trip
// verbatim. Type/UserSpecifiedType/UserSpecifiedSystem/LinkedResource are the
// caller-supplied classification fields; integratedSystem (output-only) is never
// set for user-specified entries, so it is not modeled.
type Entry struct {
	Project             string
	Location            string
	EntryGroup          string
	ID                  string
	DisplayName         string
	Description         string
	Type                string
	UserSpecifiedType   string
	UserSpecifiedSystem string
	LinkedResource      string
	Schema              json.RawMessage
	GcsFilesetSpec      json.RawMessage
}

// TagTemplateField is one typed field of a tag template. Exactly one of
// PrimitiveType or EnumValues is set: PrimitiveType is one of DOUBLE, STRING,
// BOOL, TIMESTAMP, RICHTEXT; a non-empty EnumValues marks an enum field whose
// allowed values are the listed display names (order preserved).
type TagTemplateField struct {
	DisplayName   string
	Description   string
	IsRequired    bool
	Order         int64
	PrimitiveType string
	EnumValues    []string
}

// TagTemplate is one Data Catalog tag template. Fields is the exhaustive map of
// field id → typed field definition; it is caller-supplied and round-trips
// verbatim (each field's own resource name is derived at read time).
type TagTemplate struct {
	Project     string
	Location    string
	ID          string
	DisplayName string
	Fields      map[string]TagTemplateField
}

// TagFieldValue is the value of one tag field. Exactly one pointer is non-nil,
// selecting the value's type; the chosen type must match the referencing
// template field's declared type. DisplayName and Order are output-only,
// populated at read time from the template field definition.
type TagFieldValue struct {
	StringValue    *string
	BoolValue      *bool
	DoubleValue    *float64
	TimestampValue *string
	RichtextValue  *string
	EnumValue      *string

	DisplayName string // output-only, derived from the template field
	Order       int64  // output-only, derived from the template field
}

// Tag is one Data Catalog tag attached to an entry (or entry group). ID is
// server-generated. Template is the full tag template resource name; Column is
// the optional schema column the tag scopes to. TemplateDisplayName is
// output-only, derived from the template at read time.
type Tag struct {
	Project             string
	Location            string
	EntryGroup          string
	Entry               string // empty when the tag is attached to the entry group itself
	ID                  string
	Template            string
	Column              string
	Fields              map[string]TagFieldValue
	TemplateDisplayName string // output-only, derived from the template
}

// EntryGroupConfig is the input to an entry group create or patch.
type EntryGroupConfig struct {
	Project     string
	Location    string
	ID          string
	DisplayName string
	Description string
}

// EntryConfig is the input to an entry create or patch.
type EntryConfig struct {
	Project             string
	Location            string
	EntryGroup          string
	ID                  string
	DisplayName         string
	Description         string
	Type                string
	UserSpecifiedType   string
	UserSpecifiedSystem string
	LinkedResource      string
	Schema              json.RawMessage
	GcsFilesetSpec      json.RawMessage
}

// TagTemplateConfig is the input to a tag template create or patch.
type TagTemplateConfig struct {
	Project     string
	Location    string
	ID          string
	DisplayName string
	Fields      map[string]TagTemplateField
}

// TagTemplateFieldConfig is the input to a tag-template-field create or patch on
// the fields sub-collection.
type TagTemplateFieldConfig struct {
	Project  string
	Location string
	Template string
	FieldID  string
	Field    TagTemplateField
}

// TagConfig is the input to a tag create or patch. Entry is empty when the tag
// is attached to the entry group itself.
type TagConfig struct {
	Project    string
	Location   string
	EntryGroup string
	Entry      string
	ID         string
	Template   string
	Column     string
	Fields     map[string]TagFieldValue
}

// DataCatalog is the control-plane interface a provider implements. Every method
// is synchronous; deleting a parent cascades to its descendants.
type DataCatalog interface {
	CreateEntryGroup(ctx context.Context, cfg *EntryGroupConfig) (*EntryGroup, error)
	GetEntryGroup(ctx context.Context, project, location, id string) (*EntryGroup, error)
	ListEntryGroups(ctx context.Context, project, location string) ([]EntryGroup, error)
	PatchEntryGroup(ctx context.Context, cfg *EntryGroupConfig, mask []string) (*EntryGroup, error)
	DeleteEntryGroup(ctx context.Context, project, location, id string) error

	CreateEntry(ctx context.Context, cfg *EntryConfig) (*Entry, error)
	GetEntry(ctx context.Context, project, location, entryGroup, id string) (*Entry, error)
	ListEntries(ctx context.Context, project, location, entryGroup string) ([]Entry, error)
	PatchEntry(ctx context.Context, cfg *EntryConfig, mask []string) (*Entry, error)
	DeleteEntry(ctx context.Context, project, location, entryGroup, id string) error

	CreateTagTemplate(ctx context.Context, cfg *TagTemplateConfig) (*TagTemplate, error)
	GetTagTemplate(ctx context.Context, project, location, id string) (*TagTemplate, error)
	PatchTagTemplate(ctx context.Context, cfg *TagTemplateConfig, mask []string) (*TagTemplate, error)
	DeleteTagTemplate(ctx context.Context, project, location, id string, force bool) error

	CreateTagTemplateField(ctx context.Context, cfg *TagTemplateFieldConfig) (*TagTemplate, error)
	PatchTagTemplateField(ctx context.Context, cfg *TagTemplateFieldConfig, mask []string) (*TagTemplate, error)
	DeleteTagTemplateField(ctx context.Context, project, location, template, fieldID string, force bool) error

	CreateTag(ctx context.Context, cfg *TagConfig) (*Tag, error)
	ListTags(ctx context.Context, parent *TagParent) ([]Tag, error)
	PatchTag(ctx context.Context, cfg *TagConfig, mask []string) (*Tag, error)
	DeleteTag(ctx context.Context, parent *TagParent, id string) error
}

// TagParent identifies the entry (or entry group) a tag collection hangs under.
// Entry is empty when the tags attach to the entry group itself.
type TagParent struct {
	Project    string
	Location   string
	EntryGroup string
	Entry      string
}
