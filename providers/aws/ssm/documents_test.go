package ssm_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ssm"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

const shellDocJSON = `{
  "schemaVersion": "2.2",
  "description": "Say hello",
  "parameters": {
    "greeting": {"type": "String", "default": "hello", "description": "What to say"},
    "count": {"type": "Integer", "default": 3}
  },
  "mainSteps": [
    {"action": "aws:runShellScript", "name": "say", "inputs": {"runCommand": ["echo {{ greeting }}"]}}
  ]
}`

const shellDocYAML = `schemaVersion: '2.2'
description: Say hello from YAML
parameters:
  greeting:
    type: String
    default: hi
mainSteps:
  - action: aws:runPowerShellScript
    name: say
    inputs:
      runCommand:
        - Write-Output hi
`

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// wantException asserts err carries the named SSM exception.
func wantException(t *testing.T, err error, name string) {
	t.Helper()

	var ex interface{ SSMException() (string, int) }
	if !stderrors.As(err, &ex) {
		t.Fatalf("error %v carries no SSM exception, want %s", err, name)
	}

	if got, _ := ex.SSMException(); got != name {
		t.Fatalf("exception = %s (%v), want %s", got, err, name)
	}
}

func createDoc(t *testing.T, m *ssm.Mock, name, content, format string) *ssmdriver.DocumentDescription {
	t.Helper()

	d, err := m.CreateDocument(context.Background(), &ssmdriver.CreateDocumentInput{
		Name: name, Content: content, DocumentFormat: format,
	})
	if err != nil {
		t.Fatalf("CreateDocument %s: %v", name, err)
	}

	return d
}

func TestCreateDocumentDerivesMetadata(t *testing.T) {
	m := newMock()
	d := createDoc(t, m, "hello-doc", shellDocJSON, "")

	checks := map[string][2]string{
		"version":  {d.DocumentVersion, "1"},
		"latest":   {d.LatestVersion, "1"},
		"default":  {d.DefaultVersion, "1"},
		"type":     {d.DocumentType, "Command"},
		"format":   {d.DocumentFormat, "JSON"},
		"schema":   {d.SchemaVersion, "2.2"},
		"status":   {d.Status, "Active"},
		"owner":    {d.Owner, config.NewOptions().AccountID},
		"hash":     {d.Hash, sha(shellDocJSON)},
		"hashType": {d.HashType, "Sha256"},
		"desc":     {d.Description, "Say hello"},
		"platform": {strings.Join(d.PlatformTypes, ","), "Linux,MacOS"},
	}

	for field, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", field, c[0], c[1])
		}
	}

	if len(d.Parameters) != 2 || d.Parameters[0].Name != "count" || d.Parameters[0].DefaultValue != "3" ||
		d.Parameters[1].Name != "greeting" || d.Parameters[1].DefaultValue != "hello" {
		t.Errorf("parameters = %+v", d.Parameters)
	}
}

func TestCreateDocumentErrors(t *testing.T) {
	m := newMock()
	createDoc(t, m, "taken", shellDocJSON, "")

	cases := []struct {
		name, docName, content, docType, format, exc string
	}{
		{"duplicate", "taken", shellDocJSON, "", "", "DocumentAlreadyExists"},
		{"reserved aws", "AWS-Mine", shellDocJSON, "", "", "ValidationException"},
		{"reserved amzn", "amzn-doc", shellDocJSON, "", "", "ValidationException"},
		{"bad name", "a b", shellDocJSON, "", "", "ValidationException"},
		{"bad json", "badjson", `{"schemaVersion":`, "", "", "InvalidDocumentContent"},
		{"bad yaml", "badyaml", "a: [", "", "YAML", "InvalidDocumentContent"},
		{"no schema", "noschema", `{"mainSteps":[{"action":"aws:runShellScript"}]}`, "", "", "InvalidDocumentContent"},
		{"bad schema", "badschema", `{"schemaVersion":"9.9","mainSteps":[{}]}`, "", "", "InvalidDocumentSchemaVersion"},
		{"no steps", "nosteps", `{"schemaVersion":"2.2"}`, "", "", "InvalidDocumentContent"},
		{"too big", "big", `{"schemaVersion":"2.2","x":"` + strings.Repeat("a", 70000) + `"}`, "", "", "MaxDocumentSizeExceeded"},
		{"automation schema", "auto", `{"schemaVersion":"2.2","mainSteps":[{}]}`, "Automation", "", "InvalidDocumentSchemaVersion"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.CreateDocument(context.Background(), &ssmdriver.CreateDocumentInput{
				Name: tc.docName, Content: tc.content, DocumentType: tc.docType, DocumentFormat: tc.format,
			})
			wantException(t, err, tc.exc)
		})
	}
}

func TestDocumentLimitExceeded(t *testing.T) {
	m := newMock()
	body := `{"schemaVersion":"2.2","mainSteps":[{"action":"aws:runShellScript"}]}`

	for i := 0; i < 500; i++ {
		createDoc(t, m, fmt.Sprintf("doc-%03d", i), body, "")
	}

	_, err := m.CreateDocument(context.Background(), &ssmdriver.CreateDocumentInput{Name: "one-more", Content: body})
	wantException(t, err, "DocumentLimitExceeded")
}

func TestYAMLDocumentConvertsBothWays(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	d := createDoc(t, m, "yaml-doc", shellDocYAML, "YAML")

	if d.DocumentFormat != "YAML" || d.SchemaVersion != "2.2" || strings.Join(d.PlatformTypes, ",") != "Windows,Linux,MacOS" {
		t.Fatalf("yaml doc = %+v", d)
	}

	stored, err := m.GetDocument(ctx, ssmdriver.DocumentRef{Name: "yaml-doc"}, "")
	if err != nil || stored.Content != shellDocYAML || stored.DocumentFormat != "YAML" {
		t.Fatalf("stored content = %+v, %v", stored, err)
	}

	asJSON, err := m.GetDocument(ctx, ssmdriver.DocumentRef{Name: "yaml-doc"}, "JSON")
	if err != nil {
		t.Fatalf("GetDocument JSON: %v", err)
	}

	if asJSON.DocumentFormat != "JSON" || !strings.Contains(asJSON.Content, `"schemaVersion": "2.2"`) {
		t.Fatalf("converted JSON = %s", asJSON.Content)
	}

	createDoc(t, m, "json-doc", shellDocJSON, "JSON")

	asYAML, err := m.GetDocument(ctx, ssmdriver.DocumentRef{Name: "json-doc"}, "YAML")
	if err != nil {
		t.Fatalf("GetDocument YAML: %v", err)
	}

	if !strings.Contains(asYAML.Content, `schemaVersion: "2.2"`) || !strings.Contains(asYAML.Content, "- action: aws:runShellScript") {
		t.Fatalf("converted YAML = %s", asYAML.Content)
	}
}

func updateDoc(m *ssm.Mock, name, content, version, versionName string) (*ssmdriver.DocumentDescription, error) {
	return m.UpdateDocument(context.Background(), &ssmdriver.UpdateDocumentInput{
		Name: name, Content: content, Version: version, VersionName: versionName,
	})
}

func TestUpdateDocumentVersioning(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createDoc(t, m, "versioned", shellDocJSON, "")

	v2content := strings.Replace(shellDocJSON, "Say hello", "Say hello twice", 1)

	d, err := updateDoc(m, "versioned", v2content, "$LATEST", "release-2")
	if err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}

	if d.DocumentVersion != "2" || d.LatestVersion != "2" || d.DefaultVersion != "1" || d.VersionName != "release-2" {
		t.Fatalf("after update: %+v", d)
	}

	_, err = updateDoc(m, "versioned", v2content, "2", "")
	wantException(t, err, "DuplicateDocumentContent")

	_, err = updateDoc(m, "versioned", shellDocJSON, "1", "")
	wantException(t, err, "InvalidDocumentVersion")

	_, err = updateDoc(m, "versioned", shellDocJSON, "", "release-2")
	wantException(t, err, "DuplicateDocumentVersionName")

	_, err = updateDoc(m, "missing-doc", shellDocJSON, "", "")
	wantException(t, err, "InvalidDocument")

	desc, err := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "versioned"})
	if err != nil || desc.DocumentVersion != "1" || desc.Description != "Say hello" {
		t.Fatalf("default describe = %+v, %v", desc, err)
	}

	byName, err := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "versioned", VersionName: "release-2"})
	if err != nil || byName.DocumentVersion != "2" {
		t.Fatalf("describe by version name = %+v, %v", byName, err)
	}

	_, err = m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "versioned", Version: "7"})
	wantException(t, err, "InvalidDocumentVersion")

	res, err := m.UpdateDocumentDefaultVersion(ctx, "versioned", "2")
	if err != nil || res.DefaultVersion != "2" || res.DefaultVersionName != "release-2" {
		t.Fatalf("UpdateDocumentDefaultVersion = %+v, %v", res, err)
	}

	_, err = m.UpdateDocumentDefaultVersion(ctx, "versioned", "9")
	wantException(t, err, "InvalidDocumentVersion")

	versions, err := m.ListDocumentVersions(ctx, "versioned")
	if err != nil || len(versions) != 2 || versions[0].IsDefaultVersion || !versions[1].IsDefaultVersion {
		t.Fatalf("ListDocumentVersions = %+v, %v", versions, err)
	}
}

func TestDeleteDocumentVersionRules(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createDoc(t, m, "deletable", shellDocJSON, "")

	if _, err := updateDoc(m, "deletable", strings.Replace(shellDocJSON, "hello", "bye", 1), "", ""); err != nil {
		t.Fatalf("update: %v", err)
	}

	wantException(t, m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "deletable", Version: "1"}), "InvalidDocumentOperation")

	if err := m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "deletable", Version: "2"}); err != nil {
		t.Fatalf("delete version 2: %v", err)
	}

	d, err := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "deletable"})
	if err != nil || d.LatestVersion != "1" {
		t.Fatalf("after version delete = %+v, %v", d, err)
	}

	if err := m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "deletable"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	_, err = m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "deletable"})
	wantException(t, err, "InvalidDocument")
	wantException(t, m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "deletable"}), "InvalidDocument")
}

func TestAWSOwnedCatalog(t *testing.T) {
	ctx := context.Background()
	m := newMock()

	d, err := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "AWS-RunShellScript"})
	if err != nil {
		t.Fatalf("describe AWS-RunShellScript: %v", err)
	}

	if d.Owner != "Amazon" || d.DocumentType != "Command" || d.SchemaVersion != "1.2" ||
		strings.Join(d.PlatformTypes, ",") != "Linux,MacOS" {
		t.Fatalf("AWS-RunShellScript = %+v", d)
	}

	arn := "arn:aws:ssm:us-east-1::document/AWS-RunShellScript"
	if _, err := m.GetDocument(ctx, ssmdriver.DocumentRef{Name: arn}, ""); err != nil {
		t.Fatalf("GetDocument by ARN: %v", err)
	}

	wantException(t, m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "AWS-RunShellScript"}), "InvalidDocumentOperation")

	_, err = updateDoc(m, "AWS-RunShellScript", shellDocJSON, "", "")
	wantException(t, err, "InvalidDocumentOperation")

	amazon, err := m.ListDocuments(ctx, []ssmdriver.DocumentFilter{{Key: "Owner", Values: []string{"Amazon"}}})
	if err != nil || len(amazon) < 10 {
		t.Fatalf("Owner=Amazon listed %d, %v", len(amazon), err)
	}
}

func TestDocumentPermissions(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createDoc(t, m, "shared-doc", shellDocJSON, "")

	share := func(add, remove []string, typ string) error {
		return m.ModifyDocumentPermission(ctx, &ssmdriver.ModifyPermissionInput{
			Name: "shared-doc", PermissionType: typ, AccountIDsToAdd: add, AccountIDsToRemove: remove,
		})
	}

	if err := share([]string{"111122223333", "444455556666"}, nil, "Share"); err != nil {
		t.Fatalf("share: %v", err)
	}

	got, err := m.DescribeDocumentPermission(ctx, "shared-doc", "Share")
	if err != nil || len(got) != 2 || got[0].AccountID != "111122223333" || got[0].SharedDocumentVersion != "$DEFAULT" {
		t.Fatalf("DescribeDocumentPermission = %+v, %v", got, err)
	}

	wantException(t, m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "shared-doc"}), "InvalidDocumentOperation")
	wantException(t, share([]string{"111122223333"}, nil, "Public"), "InvalidPermissionType")
	wantException(t, share([]string{"12"}, nil, "Share"), "ValidationException")

	many := make([]string, 21)
	for i := range many {
		many[i] = fmt.Sprintf("%012d", i)
	}

	wantException(t, share(many, nil, "Share"), "DocumentPermissionLimit")

	if err := share(nil, []string{"111122223333", "444455556666"}, "Share"); err != nil {
		t.Fatalf("unshare: %v", err)
	}

	if err := m.DeleteDocument(ctx, ssmdriver.DocumentRef{Name: "shared-doc"}); err != nil {
		t.Fatalf("delete after unshare: %v", err)
	}
}

func TestListDocumentsFilters(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createDoc(t, m, "team-alpha", shellDocJSON, "")
	createDoc(t, m, "team-beta", shellDocYAML, "YAML")

	if err := m.TagDocument(ctx, "team-beta", map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("TagDocument: %v", err)
	}

	names := func(filters ...ssmdriver.DocumentFilter) string {
		t.Helper()

		docs, err := m.ListDocuments(ctx, filters)
		if err != nil {
			t.Fatalf("ListDocuments: %v", err)
		}

		out := make([]string, 0, len(docs))
		for i := range docs {
			out = append(out, docs[i].Name)
		}

		return strings.Join(out, ",")
	}

	self := ssmdriver.DocumentFilter{Key: "Owner", Values: []string{"Self"}}

	cases := map[string][2]string{
		"self":     {names(self), "team-alpha,team-beta"},
		"prefix":   {names(ssmdriver.DocumentFilter{Key: "Name", Values: []string{"team-a"}}), "team-alpha"},
		"tag":      {names(ssmdriver.DocumentFilter{Key: "tag:env", Values: []string{"prod", "dev"}}), "team-beta"},
		"platform": {names(self, ssmdriver.DocumentFilter{Key: "PlatformTypes", Values: []string{"Windows"}}), "team-beta"},
		"keyword":  {names(ssmdriver.DocumentFilter{Key: "SearchKeyword", Values: []string{"ALPHA"}}), "team-alpha"},
		"type":     {names(self, ssmdriver.DocumentFilter{Key: "DocumentType", Values: []string{"Automation"}}), ""},
	}

	for name, c := range cases {
		if c[0] != c[1] {
			t.Errorf("%s: got %q, want %q", name, c[0], c[1])
		}
	}

	_, err := m.ListDocuments(ctx, []ssmdriver.DocumentFilter{{Key: "Colour", Values: []string{"red"}}})
	wantException(t, err, "InvalidFilterKey")
}

func TestDocumentTags(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	createDoc(t, m, "tagged", shellDocJSON, "")

	if err := m.TagDocument(ctx, "tagged", map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("TagDocument: %v", err)
	}

	if err := m.UntagDocument(ctx, "tagged", []string{"a"}); err != nil {
		t.Fatalf("UntagDocument: %v", err)
	}

	tags, err := m.ListDocumentTags(ctx, "tagged")
	if err != nil || len(tags) != 1 || tags["b"] != "2" {
		t.Fatalf("tags = %v, %v", tags, err)
	}

	d, _ := m.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "tagged"})
	if d.Tags["b"] != "2" {
		t.Fatalf("describe tags = %v", d.Tags)
	}

	if err := m.TagDocument(ctx, "AWS-RunShellScript", map[string]string{"a": "1"}); err == nil {
		t.Fatal("tagging an AWS-owned document succeeded")
	}
}

func TestSendCommandResolvesDocument(t *testing.T) {
	ctx := context.Background()
	m := newMock()
	send := func(doc string) error {
		_, err := m.SendCommand(ctx, ssmdriver.CommandConfig{InstanceIDs: []string{"i-0123"}, DocumentName: doc})
		return err
	}

	wantException(t, send("No-Such-Doc"), "InvalidDocument")
	wantException(t, send("AWS-StopEC2Instance"), "InvalidDocument")

	if err := send("AWS-RunShellScript"); err != nil {
		t.Fatalf("send AWS-RunShellScript: %v", err)
	}

	createDoc(t, m, "my-command", shellDocJSON, "")

	if err := send("my-command"); err != nil {
		t.Fatalf("send customer document: %v", err)
	}
}

func TestDocumentSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock()
	createDoc(t, src, "persisted", shellDocYAML, "YAML")

	if _, err := updateDoc(src, "persisted", shellDocJSON, "", "v2"); err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := src.TagDocument(ctx, "persisted", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("tag: %v", err)
	}

	if err := src.ModifyDocumentPermission(ctx, &ssmdriver.ModifyPermissionInput{
		Name: "persisted", PermissionType: "Share", AccountIDsToAdd: []string{"111122223333"},
	}); err != nil {
		t.Fatalf("share: %v", err)
	}

	raw, err := src.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock()
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	d, err := dst.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "persisted", Version: "$LATEST"})
	if err != nil || d.DocumentVersion != "2" || d.DefaultVersion != "1" || d.VersionName != "v2" ||
		d.Hash != sha(shellDocJSON) || d.Tags["k"] != "v" {
		t.Fatalf("restored = %+v, %v", d, err)
	}

	shares, err := dst.DescribeDocumentPermission(ctx, "persisted", "Share")
	if err != nil || len(shares) != 1 {
		t.Fatalf("restored shares = %+v, %v", shares, err)
	}

	if _, err := updateDoc(dst, "persisted", strings.Replace(shellDocJSON, "Say hello", "v3", 1), "", ""); err != nil {
		t.Fatalf("update after restore: %v", err)
	}

	d, _ = dst.DescribeDocument(ctx, ssmdriver.DocumentRef{Name: "persisted", Version: "$LATEST"})
	if d.DocumentVersion != "3" {
		t.Fatalf("version after restore = %s, want 3", d.DocumentVersion)
	}
}
