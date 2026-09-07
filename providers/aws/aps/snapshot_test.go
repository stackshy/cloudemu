package aps_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/aps/driver"
)

// TestSnapshotRoundTrip proves a snapshot/restore round-trip preserves
// workspaces and their child resources under their original identities, so the
// computed arn, prometheusEndpoint, createdAt and the verbatim definition blobs
// survive a restart transparently.
func TestSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	ws := createWorkspace(t, src, "snap")

	const data = "Z3JvdXBzOgo="

	_, err := src.CreateRuleGroupsNamespace(ctx, &driver.RuleGroupsNamespaceInput{
		WorkspaceID: ws.WorkspaceID, Name: "rules", Data: data,
	})
	requireNoError(t, err)

	_, err = src.CreateAlertManagerDefinition(ctx, ws.WorkspaceID, "YWxlcnQK")
	requireNoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, raw))

	restored, err := dst.DescribeWorkspace(ctx, ws.WorkspaceID)
	requireNoError(t, err)

	if restored.Arn != ws.Arn || restored.PrometheusEndpoint != ws.PrometheusEndpoint ||
		!restored.CreatedAt.Equal(ws.CreatedAt) {
		t.Fatalf("workspace identity drifted after restore: %+v", restored)
	}

	ns, err := dst.DescribeRuleGroupsNamespace(ctx, ws.WorkspaceID, "rules")
	requireNoError(t, err)

	if ns.Data != data {
		t.Fatalf("rule groups data not preserved: %q", ns.Data)
	}

	def, err := dst.DescribeAlertManagerDefinition(ctx, ws.WorkspaceID)
	requireNoError(t, err)

	if def.Data != "YWxlcnQK" {
		t.Fatalf("alert manager data not preserved: %q", def.Data)
	}
}
