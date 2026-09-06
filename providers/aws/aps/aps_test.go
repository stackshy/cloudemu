package aps_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/aps"
	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

func newMock() *aps.Mock {
	return aps.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createWorkspace(t *testing.T, m *aps.Mock, alias string) *driver.Workspace {
	t.Helper()

	ws, err := m.CreateWorkspace(context.Background(), &driver.CreateWorkspaceInput{
		Alias:     alias,
		KmsKeyArn: "arn:aws:kms:us-east-1:123456789012:key/abcd",
		Tags:      map[string]string{"team": "obs"},
	})
	requireNoError(t, err)

	return ws
}

func TestCreateWorkspaceComputedFields(t *testing.T) {
	m := newMock()
	ws := createWorkspace(t, m, "prod")

	if !strings.HasPrefix(ws.WorkspaceID, "ws-") {
		t.Fatalf("workspaceId = %q, want ws- prefix", ws.WorkspaceID)
	}

	if ws.Status != driver.StatusActive {
		t.Fatalf("status = %q, want ACTIVE", ws.Status)
	}

	if !strings.Contains(ws.Arn, ":aps:") || !strings.HasSuffix(ws.Arn, "workspace/"+ws.WorkspaceID) {
		t.Fatalf("arn = %q", ws.Arn)
	}

	wantEndpoint := "/workspaces/" + ws.WorkspaceID + "/"
	if !strings.HasPrefix(ws.PrometheusEndpoint, "https://aps-workspaces.") ||
		!strings.HasSuffix(ws.PrometheusEndpoint, wantEndpoint) {
		t.Fatalf("prometheusEndpoint = %q", ws.PrometheusEndpoint)
	}

	if ws.CreatedAt.IsZero() {
		t.Fatal("createdAt is zero")
	}
}

func TestDescribeWorkspaceStableAcrossReadsAndAliasUpdate(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	created := createWorkspace(t, m, "prod")

	d1, err := m.DescribeWorkspace(ctx, created.WorkspaceID)
	requireNoError(t, err)

	requireNoError(t, m.UpdateWorkspaceAlias(ctx, created.WorkspaceID, "prod-renamed"))

	d2, err := m.DescribeWorkspace(ctx, created.WorkspaceID)
	requireNoError(t, err)

	if d2.Alias != "prod-renamed" {
		t.Fatalf("alias after update = %q, want prod-renamed", d2.Alias)
	}

	// Every computed field is stable across the alias update.
	if d1.Arn != d2.Arn || d1.WorkspaceID != d2.WorkspaceID ||
		d1.PrometheusEndpoint != d2.PrometheusEndpoint || !d1.CreatedAt.Equal(d2.CreatedAt) {
		t.Fatalf("computed fields drifted after alias update: %+v vs %+v", d1, d2)
	}
}

func TestDescribeWorkspaceNotFound(t *testing.T) {
	m := newMock()

	_, err := m.DescribeWorkspace(context.Background(), "ws-missing")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestDeleteWorkspace(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	requireNoError(t, m.DeleteWorkspace(ctx, ws.WorkspaceID))

	_, err := m.DescribeWorkspace(ctx, ws.WorkspaceID)
	assertException(t, err, driver.ExResourceNotFound)

	assertException(t, m.DeleteWorkspace(ctx, ws.WorkspaceID), driver.ExResourceNotFound)
}

func TestListWorkspacesAliasFilter(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	createWorkspace(t, m, "prod-1")
	createWorkspace(t, m, "prod-2")
	createWorkspace(t, m, "staging")

	all, _, err := m.ListWorkspaces(ctx, "", driver.Page{})
	requireNoError(t, err)

	if len(all) != 3 {
		t.Fatalf("list all = %d, want 3", len(all))
	}

	prod, _, err := m.ListWorkspaces(ctx, "prod", driver.Page{})
	requireNoError(t, err)

	if len(prod) != 2 {
		t.Fatalf("list prod = %d, want 2", len(prod))
	}
}

func TestRuleGroupsNamespaceLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	const data = "Z3JvdXBzOgogIC0gbmFtZTogdGVzdA==" // base64 blob, stored verbatim

	ns, err := m.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: ws.WorkspaceID,
		Name:        "rules",
		Data:        data,
	})
	requireNoError(t, err)

	if ns.Status != driver.StatusActive || ns.Data != data {
		t.Fatalf("ns = %+v", ns)
	}

	// Duplicate create conflicts.
	_, err = m.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: ws.WorkspaceID, Name: "rules", Data: data,
	})
	assertException(t, err, driver.ExConflict)

	// Put replaces the data but preserves arn + createdAt.
	const data2 = "Z3JvdXBzOgogIC0gbmFtZTogdXBkYXRlZA=="

	put, err := m.PutRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: ws.WorkspaceID, Name: "rules", Data: data2,
	})
	requireNoError(t, err)

	if put.Data != data2 || put.Arn != ns.Arn || !put.CreatedAt.Equal(ns.CreatedAt) {
		t.Fatalf("put drifted arn/createdAt: %+v vs %+v", put, ns)
	}

	list, _, err := m.ListRuleGroupsNamespaces(ctx, ws.WorkspaceID, "", driver.Page{})
	requireNoError(t, err)

	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}

	requireNoError(t, m.DeleteRuleGroupsNamespace(ctx, ws.WorkspaceID, "rules"))
	_, err = m.DescribeRuleGroupsNamespace(ctx, ws.WorkspaceID, "rules")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestAlertManagerDefinitionLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	const data = "YWxlcnRtYW5hZ2VyX2NvbmZpZzoK"

	def, err := m.CreateAlertManagerDefinition(ctx, ws.WorkspaceID, data)
	requireNoError(t, err)

	if def.Status != driver.StatusActive {
		t.Fatalf("status = %q", def.Status)
	}

	// Duplicate create conflicts.
	_, err = m.CreateAlertManagerDefinition(ctx, ws.WorkspaceID, data)
	assertException(t, err, driver.ExConflict)

	const data2 = "YWxlcnRtYW5hZ2VyX2NvbmZpZzogdXBkYXRlZAo="

	put, err := m.PutAlertManagerDefinition(ctx, ws.WorkspaceID, data2)
	requireNoError(t, err)

	if put.Data != data2 || !put.CreatedAt.Equal(def.CreatedAt) {
		t.Fatalf("put drifted: %+v vs %+v", put, def)
	}

	got, err := m.DescribeAlertManagerDefinition(ctx, ws.WorkspaceID)
	requireNoError(t, err)

	if got.Data != data2 {
		t.Fatalf("describe data = %q, want %q", got.Data, data2)
	}

	requireNoError(t, m.DeleteAlertManagerDefinition(ctx, ws.WorkspaceID))
	_, err = m.DescribeAlertManagerDefinition(ctx, ws.WorkspaceID)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestLoggingConfigurationLifecycle(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	const lg = "arn:aws:logs:us-east-1:123456789012:log-group:aps:*"

	// Not configured yet -> not found (Terraform relies on this to treat logging
	// as absent).
	_, err := m.DescribeLoggingConfiguration(ctx, ws.WorkspaceID)
	assertException(t, err, driver.ExResourceNotFound)

	cfg, err := m.CreateLoggingConfiguration(ctx, ws.WorkspaceID, lg)
	requireNoError(t, err)

	if cfg.Status != driver.StatusActive || cfg.LogGroupArn != lg {
		t.Fatalf("cfg = %+v", cfg)
	}

	const lg2 = "arn:aws:logs:us-east-1:123456789012:log-group:aps2:*"

	upd, err := m.UpdateLoggingConfiguration(ctx, ws.WorkspaceID, lg2)
	requireNoError(t, err)

	if upd.LogGroupArn != lg2 || !upd.CreatedAt.Equal(cfg.CreatedAt) {
		t.Fatalf("update drifted: %+v vs %+v", upd, cfg)
	}

	requireNoError(t, m.DeleteLoggingConfiguration(ctx, ws.WorkspaceID))
	_, err = m.DescribeLoggingConfiguration(ctx, ws.WorkspaceID)
	assertException(t, err, driver.ExResourceNotFound)
}

func TestWorkspaceTags(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	requireNoError(t, m.TagResource(ctx, ws.Arn, map[string]string{"env": "test"}))

	tags, err := m.ListTagsForResource(ctx, ws.Arn)
	requireNoError(t, err)

	if tags["env"] != "test" || tags["team"] != "obs" {
		t.Fatalf("tags = %v", tags)
	}

	requireNoError(t, m.UntagResource(ctx, ws.Arn, []string{"team"}))

	tags, err = m.ListTagsForResource(ctx, ws.Arn)
	requireNoError(t, err)

	if _, ok := tags["team"]; ok {
		t.Fatalf("team tag not removed: %v", tags)
	}
}

func TestRuleGroupsNamespaceTagsByARN(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	ws := createWorkspace(t, m, "prod")

	ns, err := m.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: ws.WorkspaceID, Name: "rules", Data: "Zm9vCg==",
		Tags: map[string]string{"k": "v"},
	})
	requireNoError(t, err)

	requireNoError(t, m.TagResource(ctx, ns.Arn, map[string]string{"k2": "v2"}))

	tags, err := m.ListTagsForResource(ctx, ns.Arn)
	requireNoError(t, err)

	if tags["k"] != "v" || tags["k2"] != "v2" {
		t.Fatalf("ns tags = %v", tags)
	}
}

func TestChildOpsOnMissingWorkspace(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	_, err := m.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: "ws-missing", Name: "rules", Data: "Zm9vCg==",
	})
	assertException(t, err, driver.ExResourceNotFound)

	_, err = m.CreateAlertManagerDefinition(ctx, "ws-missing", "Zm9vCg==")
	assertException(t, err, driver.ExResourceNotFound)

	_, err = m.CreateLoggingConfiguration(ctx, "ws-missing", "arn:aws:logs:::")
	assertException(t, err, driver.ExResourceNotFound)
}

func TestInvalidTagARN(t *testing.T) {
	m := newMock()

	_, err := m.ListTagsForResource(context.Background(), "arn:aws:s3:::bucket")
	assertException(t, err, driver.ExValidation)
}

// assertException asserts err carries the given APS exception name.
func assertException(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error tagged %s, got nil", want)
	}

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error %v is not a driver.APIError", err)
	}

	if apiErr.Exception != want {
		t.Fatalf("exception = %q, want %q", apiErr.Exception, want)
	}
}
