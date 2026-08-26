package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// TestSDKWorkspacePatchPreservesOmittedTags proves that a WorkspaceUpdate with
// no Tags leaves the existing tags untouched (real ARM PATCH semantics), while a
// WorkspaceUpdate carrying explicit tags replaces them.
func TestSDKWorkspacePatchPreservesOmittedTags(t *testing.T) {
	client := newWorkspacesClient(t)
	ctx := context.Background()

	// createWorkspace seeds the workspace with tag env=test.
	createWorkspace(t, client)

	// PATCH with omitted tags must not clobber the existing tags.
	patchPoller, err := client.BeginUpdate(ctx, testRG, testWS, armdatabricks.WorkspaceUpdate{}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate (no tags): %v", err)
	}

	if _, err = patchPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("update PollUntilDone (no tags): %v", err)
	}

	got, err := client.Get(ctx, testRG, testWS, nil)
	if err != nil {
		t.Fatalf("Get after tagless patch: %v", err)
	}

	if got.Tags["env"] == nil || *got.Tags["env"] != "test" {
		t.Fatalf("tagless PATCH clobbered tags: got %v, want env=test preserved", got.Tags)
	}

	// PATCH with explicit tags replaces the tag set (AC PATCH semantics).
	replacePoller, err := client.BeginUpdate(ctx, testRG, testWS, armdatabricks.WorkspaceUpdate{
		Tags: map[string]*string{"env": to.Ptr("prod"), "team": to.Ptr("data")},
	}, nil)
	if err != nil {
		t.Fatalf("BeginUpdate (explicit tags): %v", err)
	}

	updated, err := replacePoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("update PollUntilDone (explicit tags): %v", err)
	}

	if updated.Tags["env"] == nil || *updated.Tags["env"] != "prod" {
		t.Fatalf("explicit PATCH did not apply env=prod: got %v", updated.Tags)
	}

	if updated.Tags["team"] == nil || *updated.Tags["team"] != "data" {
		t.Fatalf("explicit PATCH did not apply team=data: got %v", updated.Tags)
	}
}
