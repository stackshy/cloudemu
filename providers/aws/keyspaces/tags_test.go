package keyspaces

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

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

// The documented TagList bound (at most 60 items per request) applies to the
// library path too, not only to the wire handler, on every operation that
// takes tags. Exactly 60 is accepted.
func TestTagListBound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustKeyspace(t, m, "app")

	arn := m.keyspaceARN("app")

	tooMany := make(map[string]string, maxTagListLen+1)
	keys := make([]string, 0, maxTagListLen+1)

	for i := 0; i <= maxTagListLen; i++ {
		k := "k" + strconv.Itoa(i)
		tooMany[k] = "v"
		keys = append(keys, k)
	}

	if err := m.TagResource(ctx, arn, tooMany); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("TagResource 61 tags: got %v, want InvalidArgument", err)
	}

	if err := m.UntagResource(ctx, arn, keys); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("UntagResource 61 keys: got %v, want InvalidArgument", err)
	}

	if _, err := m.CreateKeyspace(ctx, ksdriver.CreateKeyspaceConfig{Name: "k2", Tags: tooMany}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("CreateKeyspace 61 tags: got %v, want InvalidArgument", err)
	}

	if _, err := m.CreateTable(ctx, ksdriver.CreateTableConfig{
		KeyspaceName: "app", Name: "t2", SchemaDefinition: sampleSchema(), Tags: tooMany,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("CreateTable 61 tags: got %v, want InvalidArgument", err)
	}

	if _, err := m.RestoreTable(ctx, ksdriver.RestoreTableConfig{
		SourceKeyspace: "app", SourceTable: "t1", TargetKeyspace: "app", TargetTable: "t3",
		RestoreTimestamp: time.Now(), Tags: tooMany,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("RestoreTable 61 tags: got %v, want InvalidArgument", err)
	}

	delete(tooMany, "k0")

	if err := m.TagResource(ctx, arn, tooMany); err != nil {
		t.Fatalf("TagResource 60 tags: %v", err)
	}

	if err := m.UntagResource(ctx, arn, keys[1:]); err != nil {
		t.Fatalf("UntagResource 60 keys: %v", err)
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
