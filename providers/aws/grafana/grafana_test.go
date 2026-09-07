package grafana_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/grafana"
	"github.com/stackshy/cloudemu/v2/services/grafana/driver"
)

func newMock() *grafana.Mock {
	return grafana.New(config.NewOptions())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func createWS(t *testing.T, m *grafana.Mock) *driver.Workspace {
	t.Helper()

	out, err := m.CreateWorkspace(context.Background(), &driver.CreateWorkspaceInput{
		AccountAccessType:       "CURRENT_ACCOUNT",
		AuthenticationProviders: []string{driver.AuthAWSSSO},
		PermissionType:          "SERVICE_MANAGED",
		Name:                    "ws",
		Description:             "desc",
		Tags:                    map[string]string{"a": "1"},
		VpcConfiguration:        json.RawMessage(`{"subnetIds":["subnet-1"]}`),
	})
	requireNoError(t, err)

	return out
}

func TestCreateComputedFields(t *testing.T) {
	m := newMock()
	w := createWS(t, m)

	if !strings.HasPrefix(w.ID, "g-") || len(w.ID) != len("g-")+10 {
		t.Fatalf("id = %q, want g-<10 hex>", w.ID)
	}

	if w.Status != driver.StatusActive {
		t.Fatalf("status = %q, want ACTIVE", w.Status)
	}

	if w.GrafanaVersion != "9.4" {
		t.Fatalf("grafanaVersion = %q, want default 9.4", w.GrafanaVersion)
	}

	if !strings.Contains(w.Arn, ":grafana:") || !strings.Contains(w.Arn, "/workspaces/"+w.ID) {
		t.Fatalf("arn = %q, want grafana workspace arn", w.Arn)
	}

	if w.Endpoint != w.ID+".grafana-workspace."+config.NewOptions().Region+".amazonaws.com" {
		t.Fatalf("endpoint = %q", w.Endpoint)
	}

	if w.SamlConfigurationStatus != driver.SamlNotConfigured {
		t.Fatalf("samlConfigurationStatus = %q", w.SamlConfigurationStatus)
	}
}

func TestCreateValidation(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	cases := []*driver.CreateWorkspaceInput{
		{AuthenticationProviders: []string{"AWS_SSO"}, PermissionType: "SERVICE_MANAGED"},    // no accountAccessType
		{AccountAccessType: "CURRENT_ACCOUNT", PermissionType: "SERVICE_MANAGED"},            // no providers
		{AccountAccessType: "CURRENT_ACCOUNT", AuthenticationProviders: []string{"AWS_SSO"}}, // no permissionType
	}

	for i, in := range cases {
		if _, err := m.CreateWorkspace(ctx, in); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestDescribeStable(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	a, err := m.DescribeWorkspace(ctx, w.ID)
	requireNoError(t, err)

	b, err := m.DescribeWorkspace(ctx, w.ID)
	requireNoError(t, err)

	if a.ID != b.ID || a.Arn != b.Arn || a.Endpoint != b.Endpoint ||
		!a.Created.Equal(b.Created) || a.GrafanaVersion != b.GrafanaVersion {
		t.Fatal("computed fields drifted across reads")
	}

	if _, err := m.DescribeWorkspace(ctx, "g-missing"); err == nil {
		t.Fatal("expected not found for missing workspace")
	}
}

func TestUpdateMergeSemantics(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	newDesc := "updated"
	updated, err := m.UpdateWorkspace(ctx, &driver.UpdateWorkspaceInput{
		ID:          w.ID,
		Description: &newDesc,
	})
	requireNoError(t, err)

	if updated.Description != "updated" {
		t.Fatalf("description = %q, want updated", updated.Description)
	}

	// Name was omitted and must survive.
	if updated.Name != "ws" {
		t.Fatalf("name = %q, want unchanged ws", updated.Name)
	}

	// Computed identity survives.
	if updated.ID != w.ID || updated.Arn != w.Arn || !updated.Created.Equal(w.Created) {
		t.Fatal("computed fields changed on update")
	}
}

func TestUpdateRemoveVpc(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	updated, err := m.UpdateWorkspace(ctx, &driver.UpdateWorkspaceInput{
		ID:                     w.ID,
		RemoveVpcConfiguration: true,
	})
	requireNoError(t, err)

	if updated.VpcConfiguration != nil {
		t.Fatalf("vpcConfiguration = %s, want nil after removal", updated.VpcConfiguration)
	}
}

func TestConfiguration(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	cfg, ver, err := m.DescribeWorkspaceConfiguration(ctx, w.ID)
	requireNoError(t, err)

	if cfg != "{}" || ver != "9.4" {
		t.Fatalf("default config/version = %q/%q, want {}/9.4", cfg, ver)
	}

	newVer := "10.4"
	requireNoError(t, m.UpdateWorkspaceConfiguration(ctx, &driver.UpdateConfigurationInput{
		ID:             w.ID,
		Configuration:  `{"x":1}`,
		GrafanaVersion: &newVer,
	}))

	cfg, ver, err = m.DescribeWorkspaceConfiguration(ctx, w.ID)
	requireNoError(t, err)

	if cfg != `{"x":1}` || ver != "10.4" {
		t.Fatalf("updated config/version = %q/%q", cfg, ver)
	}

	// The version upgrade is also reflected on the workspace description.
	d, err := m.DescribeWorkspace(ctx, w.ID)
	requireNoError(t, err)

	if d.GrafanaVersion != "10.4" {
		t.Fatalf("workspace grafanaVersion = %q, want 10.4", d.GrafanaVersion)
	}
}

func TestAuthentication(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	updated, err := m.UpdateWorkspaceAuthentication(ctx, &driver.UpdateAuthenticationInput{
		ID:                      w.ID,
		AuthenticationProviders: []string{driver.AuthAWSSSO, driver.AuthSAML},
		SamlConfigured:          true,
	})
	requireNoError(t, err)

	if len(updated.AuthenticationProviders) != 2 {
		t.Fatalf("providers = %v, want 2", updated.AuthenticationProviders)
	}

	if updated.SamlConfigurationStatus != driver.SamlConfigured {
		t.Fatalf("samlConfigurationStatus = %q, want CONFIGURED", updated.SamlConfigurationStatus)
	}

	got, err := m.DescribeWorkspaceAuthentication(ctx, w.ID)
	requireNoError(t, err)

	if got.SamlConfigurationStatus != driver.SamlConfigured {
		t.Fatal("describe authentication did not reflect update")
	}
}

func TestTags(t *testing.T) {
	m := newMock()
	ctx := context.Background()
	w := createWS(t, m)

	requireNoError(t, m.TagResource(ctx, w.Arn, map[string]string{"team": "obs"}))

	tags, err := m.ListTagsForResource(ctx, w.Arn)
	requireNoError(t, err)

	if tags["team"] != "obs" || tags["a"] != "1" {
		t.Fatalf("tags = %v, want team=obs and a=1", tags)
	}

	requireNoError(t, m.UntagResource(ctx, w.Arn, []string{"team"}))

	tags, err = m.ListTagsForResource(ctx, w.Arn)
	requireNoError(t, err)

	if _, ok := tags["team"]; ok {
		t.Fatal("team tag not removed")
	}

	if _, err := m.ListTagsForResource(ctx, "arn:aws:s3:::bucket"); err == nil {
		t.Fatal("expected error for non-grafana ARN")
	}
}

func TestListAndDelete(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	w := createWS(t, m)
	_ = createWS(t, m)

	items, next, err := m.ListWorkspaces(ctx, driver.Page{})
	requireNoError(t, err)

	if len(items) != 2 || next != "" {
		t.Fatalf("list = %d items next=%q, want 2 and empty", len(items), next)
	}

	deleted, err := m.DeleteWorkspace(ctx, w.ID)
	requireNoError(t, err)

	if deleted.Status != driver.StatusDeleting {
		t.Fatalf("deleted status = %q, want DELETING", deleted.Status)
	}

	if _, err := m.DescribeWorkspace(ctx, w.ID); err == nil {
		t.Fatal("expected not found after delete")
	}

	if _, err := m.DeleteWorkspace(ctx, w.ID); err == nil {
		t.Fatal("expected not found deleting twice")
	}
}

func TestListPagination(t *testing.T) {
	m := newMock()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = createWS(t, m)
	}

	page1, next, err := m.ListWorkspaces(ctx, driver.Page{MaxResults: 2})
	requireNoError(t, err)

	if len(page1) != 2 || next == "" {
		t.Fatalf("page1 = %d next=%q, want 2 and a token", len(page1), next)
	}

	page2, next2, err := m.ListWorkspaces(ctx, driver.Page{MaxResults: 2, NextToken: next})
	requireNoError(t, err)

	if len(page2) != 1 || next2 != "" {
		t.Fatalf("page2 = %d next=%q, want 1 and empty", len(page2), next2)
	}
}
