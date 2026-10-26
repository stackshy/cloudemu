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
