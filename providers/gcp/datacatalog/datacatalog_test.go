package datacatalog_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/gcp/datacatalog"
	dcdriver "github.com/stackshy/cloudemu/v2/services/datacatalog/driver"
)

const (
	project  = "proj"
	location = "us-central1"
)

func newMock() *datacatalog.Mock { return datacatalog.New(config.NewOptions()) }

func strptr(s string) *string { return &s }

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func requireCode(t *testing.T, err error, code cerrors.Code, msg string) {
	t.Helper()

	if cerrors.GetCode(err) != code {
		t.Fatalf("%s: want code %v, got %v", msg, code, err)
	}
}

func seedGroup(t *testing.T, m *datacatalog.Mock, id string) {
	t.Helper()

	_, err := m.CreateEntryGroup(context.Background(), &dcdriver.EntryGroupConfig{
		Project: project, Location: location, ID: id, DisplayName: "d",
	})
	requireNoError(t, err, "CreateEntryGroup")
}

func TestEntryGroupCRUD(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	seedGroup(t, m, "eg1")

	_, err := m.CreateEntryGroup(ctx, &dcdriver.EntryGroupConfig{Project: project, Location: location, ID: "eg1"})
	requireCode(t, err, cerrors.AlreadyExists, "duplicate entry group")

	got, err := m.GetEntryGroup(ctx, project, location, "eg1")
	requireNoError(t, err, "GetEntryGroup")

	if got.DisplayName != "d" {
		t.Fatalf("displayName = %q", got.DisplayName)
	}

	_, err = m.PatchEntryGroup(ctx, &dcdriver.EntryGroupConfig{
		Project: project, Location: location, ID: "eg1", DisplayName: "d2",
	}, []string{"displayName"})
	requireNoError(t, err, "PatchEntryGroup")

	got, _ = m.GetEntryGroup(ctx, project, location, "eg1")
	if got.DisplayName != "d2" {
		t.Fatalf("patched displayName = %q", got.DisplayName)
	}

	_, err = m.GetEntryGroup(ctx, project, location, "missing")
	requireCode(t, err, cerrors.NotFound, "missing entry group")
}

func TestEntrySchemaRoundTrip(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	seedGroup(t, m, "eg1")

	schema := json.RawMessage(`{"columns":[{"column":"c1","type":"STRING"}]}`)
	e, err := m.CreateEntry(ctx, &dcdriver.EntryConfig{
		Project: project, Location: location, EntryGroup: "eg1", ID: "e1",
		Type: "FILESET", Schema: schema,
	})
	requireNoError(t, err, "CreateEntry")

	if string(e.Schema) != string(schema) {
		t.Fatalf("schema drift: %s", e.Schema)
	}

	// Missing parent entry group → NotFound.
	_, err = m.CreateEntry(ctx, &dcdriver.EntryConfig{
		Project: project, Location: location, EntryGroup: "ghost", ID: "e1",
	})
	requireCode(t, err, cerrors.NotFound, "entry under missing group")
}

func TestEntryGroupCascadeAndPrefix(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for _, id := range []string{"eg1", "eg10"} {
		seedGroup(t, m, id)

		_, err := m.CreateEntry(ctx, &dcdriver.EntryConfig{
			Project: project, Location: location, EntryGroup: id, ID: "e",
		})
		requireNoError(t, err, "CreateEntry")
	}

	requireNoError(t, m.DeleteEntryGroup(ctx, project, location, "eg1"), "DeleteEntryGroup eg1")

	// eg1's entry gone.
	_, err := m.GetEntry(ctx, project, location, "eg1", "e")
	requireCode(t, err, cerrors.NotFound, "eg1 entry cascaded")

	// eg10's entry survives the prefix-bounded scan.
	_, err = m.GetEntry(ctx, project, location, "eg10", "e")
	requireNoError(t, err, "eg10 entry survives")
}

func seedTemplate(t *testing.T, m *datacatalog.Mock) string {
	t.Helper()

	tt, err := m.CreateTagTemplate(context.Background(), &dcdriver.TagTemplateConfig{
		Project: project, Location: location, ID: "tmpl", DisplayName: "T",
		Fields: map[string]dcdriver.TagTemplateField{
			"s": {PrimitiveType: "STRING"},
			"e": {EnumValues: []string{"A", "B"}},
		},
	})
	requireNoError(t, err, "CreateTagTemplate")

	return "projects/" + project + "/locations/" + location + "/tagTemplates/" + tt.ID
}

func TestTagTemplateValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	// A field with neither primitive nor enum type is rejected.
	_, err := m.CreateTagTemplate(ctx, &dcdriver.TagTemplateConfig{
		Project: project, Location: location, ID: "bad",
		Fields: map[string]dcdriver.TagTemplateField{"x": {}},
	})
	requireCode(t, err, cerrors.InvalidArgument, "typeless field")

	// A template with no fields is rejected.
	_, err = m.CreateTagTemplate(ctx, &dcdriver.TagTemplateConfig{Project: project, Location: location, ID: "empty"})
	requireCode(t, err, cerrors.InvalidArgument, "empty template")
}

func TestTagTypeMatching(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	seedGroup(t, m, "eg1")
	_, err := m.CreateEntry(ctx, &dcdriver.EntryConfig{Project: project, Location: location, EntryGroup: "eg1", ID: "e1"})
	requireNoError(t, err, "CreateEntry")

	tmpl := seedTemplate(t, m)

	// Correct types succeed and derive templateDisplayName.
	tag, err := m.CreateTag(ctx, &dcdriver.TagConfig{
		Project: project, Location: location, EntryGroup: "eg1", Entry: "e1", Template: tmpl,
		Fields: map[string]dcdriver.TagFieldValue{
			"s": {StringValue: strptr("hi")},
			"e": {EnumValue: strptr("A")},
		},
	})
	requireNoError(t, err, "CreateTag")

	if tag.TemplateDisplayName != "T" {
		t.Fatalf("templateDisplayName = %q", tag.TemplateDisplayName)
	}

	// Wrong primitive type → InvalidArgument.
	b := true
	_, err = m.CreateTag(ctx, &dcdriver.TagConfig{
		Project: project, Location: location, EntryGroup: "eg1", Entry: "e1", Template: tmpl,
		Fields: map[string]dcdriver.TagFieldValue{"s": {BoolValue: &b}},
	})
	requireCode(t, err, cerrors.InvalidArgument, "bool for string field")

	// Enum value outside the allowed set → InvalidArgument.
	_, err = m.CreateTag(ctx, &dcdriver.TagConfig{
		Project: project, Location: location, EntryGroup: "eg1", Entry: "e1", Template: tmpl,
		Fields: map[string]dcdriver.TagFieldValue{"e": {EnumValue: strptr("Z")}},
	})
	requireCode(t, err, cerrors.InvalidArgument, "disallowed enum value")
}

func TestTagTemplateForceDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	seedGroup(t, m, "eg1")
	_, err := m.CreateEntry(ctx, &dcdriver.EntryConfig{Project: project, Location: location, EntryGroup: "eg1", ID: "e1"})
	requireNoError(t, err, "CreateEntry")

	tmpl := seedTemplate(t, m)
	_, err = m.CreateTag(ctx, &dcdriver.TagConfig{
		Project: project, Location: location, EntryGroup: "eg1", Entry: "e1", Template: tmpl,
		Fields: map[string]dcdriver.TagFieldValue{"s": {StringValue: strptr("hi")}},
	})
	requireNoError(t, err, "CreateTag")

	// A dependent tag blocks a non-forced delete.
	err = m.DeleteTagTemplate(ctx, project, location, "tmpl", false)
	requireCode(t, err, cerrors.FailedPrecondition, "non-force delete with dependents")

	// Force removes the template and its dependent tags.
	requireNoError(t, m.DeleteTagTemplate(ctx, project, location, "tmpl", true), "force delete")

	tags, err := m.ListTags(ctx, &dcdriver.TagParent{Project: project, Location: location, EntryGroup: "eg1", Entry: "e1"})
	requireNoError(t, err, "ListTags")

	if len(tags) != 0 {
		t.Fatalf("dependent tags survived force delete: %d", len(tags))
	}
}
