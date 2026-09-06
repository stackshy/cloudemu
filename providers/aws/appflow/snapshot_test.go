package appflow_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/appflow/driver"
)

// TestSnapshotRoundTripAppFlow proves a snapshot/restore round-trip preserves
// flows and connector profiles under their original identities, so the computed
// flowArn and createdAt survive a restart transparently.
func TestSnapshotRoundTripAppFlow(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	created, err := src.CreateFlow(ctx, &driver.CreateFlowInput{
		FlowName:    "snap-flow",
		Description: "d",
		Tags:        map[string]string{"env": "prod"},
		Extra:       sourceExtra(),
	})
	requireNoError(t, err)

	if _, err := src.CreateConnectorProfile(ctx, &driver.CreateConnectorProfileInput{
		ConnectorProfileName: "snap-cp",
		ConnectorType:        "Salesforce",
	}); err != nil {
		t.Fatalf("create connector profile: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, raw))

	restored, err := dst.DescribeFlow(ctx, "snap-flow")
	requireNoError(t, err)

	if restored.FlowArn != created.FlowArn {
		t.Fatalf("flowArn drifted after restore: %q != %q", restored.FlowArn, created.FlowArn)
	}

	if !restored.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("createdAt drifted after restore")
	}

	if restored.Tags["env"] != "prod" || restored.SourceConnectorType != "S3" {
		t.Fatalf("flow state not preserved: %+v", restored)
	}

	profiles, _, err := dst.DescribeConnectorProfiles(ctx, nil, "", driver.Page{})
	if err != nil || len(profiles) != 1 || profiles[0].ConnectorProfileName != "snap-cp" {
		t.Fatalf("restored profiles = %+v, err %v", profiles, err)
	}
}
