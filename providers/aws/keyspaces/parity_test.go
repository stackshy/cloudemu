package keyspaces

import (
	"context"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// Keyspace/table names follow the documented KeyspaceName/TableName shape:
// length 1-48, pattern [a-zA-Z0-9][a-zA-Z0-9_]{0,47}.
func TestNameValidation(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	maxName := strings.Repeat("a", maxNameLen)
	tooLong := strings.Repeat("a", maxNameLen+1)

	valid := []string{"a", "A1", "1abc", "9", "my_keyspace", maxName}
	for _, n := range valid {
		if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: n}); err != nil {
			t.Fatalf("CreateKeyspace(%q): unexpected error %v", n, err)
		}
	}

	invalid := []string{"", tooLong, "_lead", "has-dash", "has.dot", "has space", "a/b", "ünicode"}
	for _, n := range invalid {
		if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: n}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("CreateKeyspace(%q): got %v, want InvalidArgument", n, err)
		}

		if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
			KeyspaceName: "a", Name: n, SchemaDefinition: sampleSchema(),
		}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("CreateTable(%q): got %v, want InvalidArgument", n, err)
		}
	}

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "a", Name: "1t", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable leading digit: %v", err)
	}

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "a", Name: maxName, SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable 48-char name: %v", err)
	}

	if _, err := m.RestoreTable(ctx, ksdriver.RestoreTableConfig{
		SourceKeyspace: "a", SourceTable: "1t", TargetKeyspace: "a", TargetTable: tooLong,
		RestoreTimestamp: m.opts.Clock.Now().Add(-1),
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("RestoreTable too-long target: got %v, want InvalidArgument", err)
	}

	fields := []ksdriver.FieldDefinition{{Name: "f", Type: "text"}}
	if _, err := m.CreateType(ctx, "a", tooLong, fields); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("CreateType too long: got %v, want InvalidArgument", err)
	}

	if _, err := m.CreateType(ctx, "a", maxName, fields); err != nil {
		t.Fatalf("CreateType 48-char name: %v", err)
	}
}

// A well-formed request naming a parent keyspace that doesn't exist is
// ResourceNotFound for CreateTable, CreateType, and RestoreTable's target.
func TestMissingParentKeyspaceIsNotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "ghost", Name: "t", SchemaDefinition: sampleSchema(),
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("CreateTable: got %v, want NotFound", err)
	}

	if _, err := m.CreateType(ctx, "ghost", "addr", []ksdriver.FieldDefinition{{Name: "f", Type: "text"}}); !cerrors.IsNotFound(err) {
		t.Fatalf("CreateType: got %v, want NotFound", err)
	}

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "src", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	if _, err := m.RestoreTable(ctx, ksdriver.RestoreTableConfig{
		SourceKeyspace: "app", SourceTable: "src", TargetKeyspace: "ghost", TargetTable: "dst",
		RestoreTimestamp: m.opts.Clock.Now(),
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("RestoreTable: got %v, want NotFound", err)
	}
}

// Tagging operations require an existing keyspace or table and a well-formed
// Keyspaces ARN.
func TestTagsRequireExistingResource(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	ghostKS := m.keyspaceARN("ghost")
	ghostTable := m.tableARN("app", "ghost")

	for _, arn := range []string{ghostKS, ghostTable} {
		if err := m.TagResource(ctx, arn, map[string]string{"k": "v"}); !cerrors.IsNotFound(err) {
			t.Fatalf("TagResource(%s): got %v, want NotFound", arn, err)
		}

		if err := m.UntagResource(ctx, arn, []string{"k"}); !cerrors.IsNotFound(err) {
			t.Fatalf("UntagResource(%s): got %v, want NotFound", arn, err)
		}

		if _, err := m.ListTagsForResource(ctx, arn); !cerrors.IsNotFound(err) {
			t.Fatalf("ListTagsForResource(%s): got %v, want NotFound", arn, err)
		}
	}

	if len(m.tags) != 0 {
		t.Fatalf("tags stored for missing resources: %v", m.tags)
	}

	for _, arn := range []string{"", "not-an-arn", "arn:aws:dynamodb:us-east-1:123456789012:table/x"} {
		if err := m.TagResource(ctx, arn, map[string]string{"k": "v"}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("TagResource(%q): got %v, want InvalidArgument", arn, err)
		}
	}

	// Existing keyspace and table ARNs still work, including after deletion
	// turning them into NotFound.
	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	tableARN := m.tableARN("app", "t")
	for _, arn := range []string{m.keyspaceARN("app"), tableARN} {
		if err := m.TagResource(ctx, arn, map[string]string{"k": "v"}); err != nil {
			t.Fatalf("TagResource(%s): %v", arn, err)
		}

		if tags, err := m.ListTagsForResource(ctx, arn); err != nil || len(tags) != 1 {
			t.Fatalf("ListTagsForResource(%s): %v %v", arn, tags, err)
		}
	}

	if err := m.DeleteTable(ctx, "app", "t"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}

	if _, err := m.ListTagsForResource(ctx, tableARN); !cerrors.IsNotFound(err) {
		t.Fatalf("ListTagsForResource after delete: got %v, want NotFound", err)
	}
}

// Tag keys/values follow the documented Keyspaces tag restrictions on every
// path that accepts tags.
func TestTagRestrictions(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	arn := m.keyspaceARN("app")

	bad := []map[string]string{
		{"aws:owner": "x"},
		{"AWS:owner": "x"},
		{"": "x"},
		{"k": ""},
		{strings.Repeat("k", maxTagKeyLen+1): "v"},
		{"k": strings.Repeat("v", maxTagValueLen+1)},
		{"k*": "v"},
		{"k": "v@x"},
	}

	for _, tags := range bad {
		if err := m.TagResource(ctx, arn, tags); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("TagResource(%v): got %v, want InvalidArgument", tags, err)
		}

		if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "k2", Tags: tags}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("CreateKeyspace tags %v: got %v, want InvalidArgument", tags, err)
		}

		if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
			KeyspaceName: "app", Name: "t2", SchemaDefinition: sampleSchema(), Tags: tags,
		}); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("CreateTable tags %v: got %v, want InvalidArgument", tags, err)
		}
	}

	if err := m.TagResource(ctx, arn, nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("TagResource no tags: got %v, want InvalidArgument", err)
	}

	if err := m.UntagResource(ctx, arn, nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("UntagResource no tags: got %v, want InvalidArgument", err)
	}

	good := map[string]string{
		strings.Repeat("k", maxTagKeyLen): strings.Repeat("v", maxTagValueLen),
		"team name":                       "data + ml = a/b_c:d.e-f",
		"ключ":                            "значение1",
		"awsish":                          "ok",
	}
	if err := m.TagResource(ctx, arn, good); err != nil {
		t.Fatalf("TagResource valid: %v", err)
	}

	if tags, err := m.ListTagsForResource(ctx, arn); err != nil || len(tags) != len(good) {
		t.Fatalf("ListTagsForResource: %v %v", tags, err)
	}
}

// The trailing "/" on a resource ARN is optional; both forms address the same
// resource, and an ARN for another account or region is not found.
func TestTagARNForms(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t", SchemaDefinition: sampleSchema(),
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	noSlash := strings.TrimSuffix(m.tableARN("app", "t"), "/")
	if err := m.TagResource(ctx, noSlash, map[string]string{"k": "v"}); err != nil {
		t.Fatalf("TagResource without trailing slash: %v", err)
	}

	tags, err := m.ListTagsForResource(ctx, m.tableARN("app", "t"))
	if err != nil || len(tags) != 1 {
		t.Fatalf("ListTagsForResource canonical: %v %v", tags, err)
	}

	for _, arn := range []string{
		"arn:aws:cassandra:us-west-2:123456789012:/keyspace/app/",
		"arn:aws:cassandra:us-east-1:999999999999:/keyspace/app/",
		"arn:aws:cassandra:us-east-1:123456789012:/keyspace/app/table/",
		"arn:aws:cassandra:us-east-1:123456789012:/keyspace/app/type/t/",
	} {
		if _, err := m.ListTagsForResource(ctx, arn); !cerrors.IsNotFound(err) {
			t.Fatalf("ListTagsForResource(%s): got %v, want NotFound", arn, err)
		}
	}
}

// All four documented system keyspaces exist and none can be deleted.
func TestSystemKeyspaces(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	for _, name := range []string{"system", "system_schema", "system_schema_mcs", "system_multiregion_info"} {
		if _, err := m.GetKeyspace(ctx, name); err != nil {
			t.Fatalf("GetKeyspace(%s): %v", name, err)
		}

		if err := m.DeleteKeyspace(ctx, name); !cerrors.IsInvalidArgument(err) {
			t.Fatalf("DeleteKeyspace(%s): got %v, want InvalidArgument", name, err)
		}
	}
}
