package datacatalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	datacatalog "google.golang.org/api/datacatalog/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	project  = "mock-project"
	location = "us-central1"
)

func newSDKClient(t *testing.T) *datacatalog.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.NewFromProvider(cloud)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := datacatalog.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("datacatalog.NewService: %v", err)
	}

	return svc
}

func locParent() string { return "projects/" + project + "/locations/" + location }

// TestSDKLifecycle drives the real google.golang.org/api Data Catalog client
// through the full hierarchy: entry group → entry (with schema + fileset spec) →
// tag template (typed fields) → tag (referencing the template), verifies
// computed fields (name/templateDisplayName) are stable across a re-read,
// patches display names, then deletes the entry group and confirms the cascade
// removed the entry and its tag.
func TestSDKLifecycle(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	egName := createEntryGroup(t, ctx, svc)
	entryName := createEntry(t, ctx, svc, egName)
	ttName := createTagTemplate(t, ctx, svc)
	tagName := createTag(t, ctx, svc, entryName, ttName)

	assertTagStable(t, ctx, svc, entryName, tagName)
	assertPatch(t, ctx, svc, egName, ttName)
	assertCascadeDelete(t, ctx, svc, egName, entryName)
}

func createEntryGroup(t *testing.T, ctx context.Context, svc *datacatalog.Service) string {
	t.Helper()

	eg, err := svc.Projects.Locations.EntryGroups.Create(locParent(),
		&datacatalog.GoogleCloudDatacatalogV1EntryGroup{DisplayName: "Analytics", Description: "jan"}).
		EntryGroupId("eg1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("EntryGroups.Create: %v", err)
	}

	want := locParent() + "/entryGroups/eg1"
	if eg.Name != want {
		t.Fatalf("entry group name = %q, want %q", eg.Name, want)
	}

	return want
}

func createEntry(t *testing.T, ctx context.Context, svc *datacatalog.Service, egName string) string {
	t.Helper()

	entry := &datacatalog.GoogleCloudDatacatalogV1Entry{
		DisplayName:    "myentry",
		Type:           "FILESET",
		GcsFilesetSpec: &datacatalog.GoogleCloudDatacatalogV1GcsFilesetSpec{FilePatterns: []string{"gs://b/dir/*"}},
		Schema: &datacatalog.GoogleCloudDatacatalogV1Schema{
			Columns: []*datacatalog.GoogleCloudDatacatalogV1ColumnSchema{
				{Column: "first_name", Type: "STRING", Mode: "REQUIRED"},
				{Column: "age", Type: "DOUBLE"},
			},
		},
	}

	e, err := svc.Projects.Locations.EntryGroups.Entries.Create(egName, entry).EntryId("entry1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Entries.Create: %v", err)
	}

	if e.Name != egName+"/entries/entry1" || e.Type != "FILESET" {
		t.Fatalf("entry unexpected: %+v", e)
	}

	if e.GcsFilesetSpec == nil || len(e.GcsFilesetSpec.FilePatterns) != 1 {
		t.Fatalf("gcsFilesetSpec did not round-trip: %+v", e.GcsFilesetSpec)
	}

	if e.Schema == nil || len(e.Schema.Columns) != 2 || e.Schema.Columns[0].Column != "first_name" {
		t.Fatalf("schema did not round-trip: %+v", e.Schema)
	}

	return e.Name
}

func createTagTemplate(t *testing.T, ctx context.Context, svc *datacatalog.Service) string {
	t.Helper()

	tmpl := &datacatalog.GoogleCloudDatacatalogV1TagTemplate{
		DisplayName: "Demo Template",
		Fields: map[string]datacatalog.GoogleCloudDatacatalogV1TagTemplateField{
			"source": {DisplayName: "Source", Type: &datacatalog.GoogleCloudDatacatalogV1FieldType{PrimitiveType: "STRING"}},
			"num_rows": {
				Type: &datacatalog.GoogleCloudDatacatalogV1FieldType{PrimitiveType: "DOUBLE"},
			},
			"sensitivity": {
				Type: &datacatalog.GoogleCloudDatacatalogV1FieldType{
					EnumType: &datacatalog.GoogleCloudDatacatalogV1FieldTypeEnumType{
						AllowedValues: []*datacatalog.GoogleCloudDatacatalogV1FieldTypeEnumTypeEnumValue{
							{DisplayName: "HIGH"}, {DisplayName: "LOW"},
						},
					},
				},
			},
		},
	}

	tt, err := svc.Projects.Locations.TagTemplates.Create(locParent(), tmpl).
		TagTemplateId("demo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("TagTemplates.Create: %v", err)
	}

	want := locParent() + "/tagTemplates/demo"
	if tt.Name != want || len(tt.Fields) != 3 {
		t.Fatalf("tag template unexpected: name=%q fields=%d", tt.Name, len(tt.Fields))
	}

	if got := tt.Fields["source"].Name; got != want+"/fields/source" {
		t.Fatalf("field computed name = %q", got)
	}

	if got := tt.Fields["sensitivity"].Type.EnumType; got == nil || len(got.AllowedValues) != 2 {
		t.Fatalf("enum field did not round-trip: %+v", got)
	}

	return want
}

func createTag(t *testing.T, ctx context.Context, svc *datacatalog.Service, entryName, ttName string) string {
	t.Helper()

	tag := &datacatalog.GoogleCloudDatacatalogV1Tag{
		Template: ttName,
		Fields: map[string]datacatalog.GoogleCloudDatacatalogV1TagField{
			"source":      {StringValue: "bigquery"},
			"num_rows":    {DoubleValue: 42},
			"sensitivity": {EnumValue: &datacatalog.GoogleCloudDatacatalogV1TagFieldEnumValue{DisplayName: "HIGH"}},
		},
	}

	created, err := svc.Projects.Locations.EntryGroups.Entries.Tags.Create(entryName, tag).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Tags.Create: %v", err)
	}

	if !strings.HasPrefix(created.Name, entryName+"/tags/") {
		t.Fatalf("tag name = %q, want under %q", created.Name, entryName)
	}

	if created.TemplateDisplayName != "Demo Template" {
		t.Fatalf("templateDisplayName = %q, want derived", created.TemplateDisplayName)
	}

	if created.Fields["source"].StringValue != "bigquery" || created.Fields["source"].DisplayName != "Source" {
		t.Fatalf("string field/displayName mismatch: %+v", created.Fields["source"])
	}

	assertTagTypeValidation(t, ctx, svc, entryName, ttName)

	return created.Name
}

// assertTagTypeValidation confirms a value whose type mismatches the template
// field is rejected with 400, matching real Data Catalog.
func assertTagTypeValidation(t *testing.T, ctx context.Context, svc *datacatalog.Service, entryName, ttName string) {
	t.Helper()

	bad := &datacatalog.GoogleCloudDatacatalogV1Tag{
		Template: ttName,
		Fields: map[string]datacatalog.GoogleCloudDatacatalogV1TagField{
			"source": {BoolValue: true}, // STRING field given a bool
		},
	}

	if _, err := svc.Projects.Locations.EntryGroups.Entries.Tags.Create(entryName, bad).Context(ctx).Do(); !isCode(err, http.StatusBadRequest) {
		t.Fatalf("type mismatch must be 400, got %v", err)
	}

	unknown := &datacatalog.GoogleCloudDatacatalogV1Tag{
		Template: ttName,
		Fields:   map[string]datacatalog.GoogleCloudDatacatalogV1TagField{"nope": {StringValue: "x"}},
	}

	if _, err := svc.Projects.Locations.EntryGroups.Entries.Tags.Create(entryName, unknown).Context(ctx).Do(); !isCode(err, http.StatusBadRequest) {
		t.Fatalf("unknown field must be 400, got %v", err)
	}
}

// assertTagStable re-lists the tags and confirms the computed name and derived
// template fields did not drift.
func assertTagStable(t *testing.T, ctx context.Context, svc *datacatalog.Service, entryName, tagName string) {
	t.Helper()

	resp, err := svc.Projects.Locations.EntryGroups.Entries.Tags.List(entryName).Context(ctx).Do()
	if err != nil || len(resp.Tags) != 1 {
		t.Fatalf("Tags.List: err=%v count=%d", err, len(resp.Tags))
	}

	if resp.Tags[0].Name != tagName || resp.Tags[0].TemplateDisplayName != "Demo Template" {
		t.Fatalf("tag drift on re-list: %+v", resp.Tags[0])
	}
}

// assertPatch updates the entry group and tag template display names and
// confirms the masked writes applied.
func assertPatch(t *testing.T, ctx context.Context, svc *datacatalog.Service, egName, ttName string) {
	t.Helper()

	if _, err := svc.Projects.Locations.EntryGroups.Patch(egName,
		&datacatalog.GoogleCloudDatacatalogV1EntryGroup{DisplayName: "Renamed"}).
		UpdateMask("displayName").Context(ctx).Do(); err != nil {
		t.Fatalf("EntryGroups.Patch: %v", err)
	}

	got, err := svc.Projects.Locations.EntryGroups.Get(egName).Context(ctx).Do()
	if err != nil || got.DisplayName != "Renamed" {
		t.Fatalf("entry group patch not applied: %+v (%v)", got, err)
	}

	if _, err := svc.Projects.Locations.TagTemplates.Patch(ttName,
		&datacatalog.GoogleCloudDatacatalogV1TagTemplate{DisplayName: "Renamed Template"}).
		UpdateMask("displayName").Context(ctx).Do(); err != nil {
		t.Fatalf("TagTemplates.Patch: %v", err)
	}

	gt, err := svc.Projects.Locations.TagTemplates.Get(ttName).Context(ctx).Do()
	if err != nil || gt.DisplayName != "Renamed Template" || len(gt.Fields) != 3 {
		t.Fatalf("tag template patch/field survival failed: %+v (%v)", gt, err)
	}
}

// assertCascadeDelete deletes the entry group and confirms its entry (and the
// tag beneath it) were cascade-removed.
func assertCascadeDelete(t *testing.T, ctx context.Context, svc *datacatalog.Service, egName, entryName string) {
	t.Helper()

	if _, err := svc.Projects.Locations.EntryGroups.Delete(egName).Context(ctx).Do(); err != nil {
		t.Fatalf("EntryGroups.Delete: %v", err)
	}

	if _, err := svc.Projects.Locations.EntryGroups.Get(egName).Context(ctx).Do(); !isCode(err, http.StatusNotFound) {
		t.Fatalf("entry group must be gone, got %v", err)
	}

	if _, err := svc.Projects.Locations.EntryGroups.Entries.Get(entryName).Context(ctx).Do(); !isCode(err, http.StatusNotFound) {
		t.Fatalf("entry must be cascade-deleted, got %v", err)
	}
}

// TestPrefixCollisionCascade guards the trailing-slash-bounded prefix scan:
// deleting entry group "eg1" must not touch "eg10"'s entries or tags.
func TestPrefixCollisionCascade(t *testing.T) {
	svc := newSDKClient(t)
	ctx := context.Background()

	for _, id := range []string{"eg1", "eg10"} {
		if _, err := svc.Projects.Locations.EntryGroups.Create(locParent(),
			&datacatalog.GoogleCloudDatacatalogV1EntryGroup{}).EntryGroupId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}

		if _, err := svc.Projects.Locations.EntryGroups.Entries.Create(locParent()+"/entryGroups/"+id,
			&datacatalog.GoogleCloudDatacatalogV1Entry{UserSpecifiedType: "custom"}).
			EntryId("e").Context(ctx).Do(); err != nil {
			t.Fatalf("create entry under %s: %v", id, err)
		}
	}

	if _, err := svc.Projects.Locations.EntryGroups.Delete(locParent() + "/entryGroups/eg1").Context(ctx).Do(); err != nil {
		t.Fatalf("delete eg1: %v", err)
	}

	// eg10's entry must survive.
	if _, err := svc.Projects.Locations.EntryGroups.Entries.Get(
		locParent() + "/entryGroups/eg10/entries/e").Context(ctx).Do(); err != nil {
		t.Fatalf("eg10 entry must survive eg1 deletion, got %v", err)
	}
}

func isCode(err error, code int) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == code
	}

	return false
}
