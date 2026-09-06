package workflows

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	wdriver "github.com/stackshy/cloudemu/v2/services/workflows/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	return New(config.NewOptions(config.WithProjectID("p")))
}

func fields(kv map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range kv {
		out[k] = json.RawMessage(`"` + v + `"`)
	}

	return out
}

func TestWorkflowCRUD(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	cfg := &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{
			"description":    "hello",
			"sourceContents": "main:\n  steps: []",
			"serviceAccount": "sa@p.iam.gserviceaccount.com",
		}),
	}

	res, op, err := m.CreateWorkflow(ctx, cfg)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	if !op.Done || op.Type != "create" {
		t.Fatalf("op = %+v", op)
	}

	if res.State != "ACTIVE" || res.RevisionID == "" || res.CreateTime.IsZero() {
		t.Fatalf("computed fields missing: %+v", res)
	}

	if res.Revision != 1 {
		t.Fatalf("initial revision = %d, want 1", res.Revision)
	}

	got, err := m.GetWorkflow(ctx, "p", "us-central1", "wf")
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}

	if got.RevisionID != res.RevisionID || !got.CreateTime.Equal(res.CreateTime) {
		t.Fatalf("computed unstable across reads: got %+v want %+v", got, res)
	}

	// Duplicate -> AlreadyExists.
	if _, _, err := m.CreateWorkflow(ctx, cfg); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create err = %v, want AlreadyExists", err)
	}

	// List scoping.
	all, err := m.ListWorkflows(ctx, "p", "us-central1")
	if err != nil || len(all) != 1 {
		t.Fatalf("ListWorkflows = %d,%v, want 1", len(all), err)
	}

	if _, err := m.DeleteWorkflow(ctx, "p", "us-central1", "wf"); err != nil {
		t.Fatalf("DeleteWorkflow: %v", err)
	}

	if _, err := m.GetWorkflow(ctx, "p", "us-central1", "wf"); !cerrors.IsNotFound(err) {
		t.Fatalf("get after delete err = %v, want NotFound", err)
	}
}

func TestMissingIDAndLocation(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateWorkflow(ctx, &wdriver.Config{Project: "p", Location: "us-central1"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing id err = %v, want InvalidArgument", err)
	}

	if _, _, err := m.CreateWorkflow(ctx, &wdriver.Config{Project: "p", ID: "wf"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing location err = %v, want InvalidArgument", err)
	}
}

// TestRevisionBumpOnSourceChange is the drift-critical behavior: a Patch that
// changes sourceContents (or serviceAccount) mints a new revisionId and
// revisionCreateTime, while a Patch that touches only labels/description leaves
// them stable — matching real GCP and keeping a Terraform plan clean.
func TestRevisionBumpOnSourceChange(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	create := &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{
			"description":    "v1",
			"sourceContents": "SOURCE-A",
			"serviceAccount": "sa-a@p.iam.gserviceaccount.com",
		}),
	}

	res, _, err := m.CreateWorkflow(ctx, create)
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	rev1 := res.RevisionID
	createTime := res.CreateTime
	revCreate1 := res.RevisionCreateTime

	// Patch only description -> revision UNCHANGED.
	patchDesc := &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{"description": "v2"}),
	}

	res2, _, err := m.PatchWorkflow(ctx, patchDesc, []string{"description"})
	if err != nil {
		t.Fatalf("PatchWorkflow(description): %v", err)
	}

	if res2.RevisionID != rev1 {
		t.Fatalf("description-only patch bumped revision: %q -> %q", rev1, res2.RevisionID)
	}

	if !res2.RevisionCreateTime.Equal(revCreate1) {
		t.Fatalf("description-only patch changed revisionCreateTime")
	}

	if res2.Revision != 1 {
		t.Fatalf("description-only patch bumped revision counter to %d", res2.Revision)
	}

	// Patch sourceContents -> revision CHANGES, createTime stable.
	patchSrc := &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{"sourceContents": "SOURCE-B"}),
	}

	res3, _, err := m.PatchWorkflow(ctx, patchSrc, []string{"sourceContents"})
	if err != nil {
		t.Fatalf("PatchWorkflow(sourceContents): %v", err)
	}

	if res3.RevisionID == rev1 {
		t.Fatalf("sourceContents patch did not bump revision (still %q)", rev1)
	}

	if res3.Revision != 2 {
		t.Fatalf("revision counter = %d, want 2", res3.Revision)
	}

	if !res3.CreateTime.Equal(createTime) {
		t.Fatalf("createTime changed on source patch: %v -> %v", createTime, res3.CreateTime)
	}

	// Re-patch with the SAME source -> no further bump (idempotent revision).
	res4, _, err := m.PatchWorkflow(ctx, patchSrc, []string{"sourceContents"})
	if err != nil {
		t.Fatalf("PatchWorkflow(same source): %v", err)
	}

	if res4.RevisionID != res3.RevisionID {
		t.Fatalf("same-source patch bumped revision: %q -> %q", res3.RevisionID, res4.RevisionID)
	}

	// Patch serviceAccount -> revision CHANGES.
	patchSA := &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{"serviceAccount": "sa-b@p.iam.gserviceaccount.com"}),
	}

	res5, _, err := m.PatchWorkflow(ctx, patchSA, []string{"serviceAccount"})
	if err != nil {
		t.Fatalf("PatchWorkflow(serviceAccount): %v", err)
	}

	if res5.Revision != 3 {
		t.Fatalf("serviceAccount patch revision = %d, want 3", res5.Revision)
	}
}

func TestPatchNotFound(t *testing.T) {
	m := newMock(t)

	_, _, err := m.PatchWorkflow(context.Background(),
		&wdriver.Config{Project: "p", Location: "us-central1", ID: "nope", Fields: fields(map[string]string{"description": "x"})},
		[]string{"description"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("patch missing err = %v, want NotFound", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	if _, _, err := m.CreateWorkflow(ctx, &wdriver.Config{
		Project: "p", Location: "us-central1", ID: "wf",
		Fields: fields(map[string]string{"sourceContents": "SRC", "description": "d"}),
	}); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	orig, err := m.GetWorkflow(ctx, "p", "us-central1", "wf")
	if err != nil {
		t.Fatalf("GetWorkflow: %v", err)
	}

	blob, err := m.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	restored := newMock(t)
	if err := restored.Restore(ctx, blob); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	got, err := restored.GetWorkflow(ctx, "p", "us-central1", "wf")
	if err != nil {
		t.Fatalf("GetWorkflow after restore: %v", err)
	}

	if got.RevisionID != orig.RevisionID || got.Revision != orig.Revision || !got.CreateTime.Equal(orig.CreateTime) {
		t.Fatalf("restored resource differs: %+v vs %+v", got, orig)
	}
}
