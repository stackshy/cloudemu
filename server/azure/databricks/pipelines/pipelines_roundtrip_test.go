package pipelines_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/pipelines"

	"github.com/stackshy/cloudemu/server"
	pipelineshandler "github.com/stackshy/cloudemu/server/azure/databricks/pipelines"
)

func newPipelineClient(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(pipelineshandler.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:               ts.URL,
		Token:              "test-token",
		Credentials:        config.PatCredentials{},
		HTTPTimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

func TestSDKPipelineLifecycle(t *testing.T) {
	w := newPipelineClient(t)
	ctx := context.Background()

	created, err := w.Pipelines.Create(ctx, pipelines.CreatePipeline{
		Name:        "pipe-1",
		Storage:     "/mnt/pipe",
		Target:      "db",
		Continuous:  true,
		Development: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id := created.PipelineId
	if id == "" {
		t.Fatal("expected pipeline id from create")
	}

	got, err := w.Pipelines.GetByPipelineId(ctx, id)
	if err != nil {
		t.Fatalf("GetByPipelineId: %v", err)
	}

	if got.Name != "pipe-1" {
		t.Fatalf("unexpected pipeline: %+v", got)
	}

	if got.State != pipelines.PipelineStateIdle {
		t.Fatalf("got state %q, want IDLE", got.State)
	}

	all, err := w.Pipelines.ListPipelinesAll(ctx, pipelines.ListPipelinesRequest{})
	if err != nil {
		t.Fatalf("ListPipelinesAll: %v", err)
	}

	if len(all) != 1 {
		t.Fatalf("got %d pipelines, want 1", len(all))
	}

	if all[0].PipelineId != id {
		t.Fatalf("listed id %q, want %q", all[0].PipelineId, id)
	}

	if err = w.Pipelines.Update(ctx, pipelines.EditPipeline{
		PipelineId: id,
		Name:       "pipe-1-edited",
		Channel:    "PREVIEW",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	edited, err := w.Pipelines.GetByPipelineId(ctx, id)
	if err != nil {
		t.Fatalf("GetByPipelineId after update: %v", err)
	}

	if edited.Name != "pipe-1-edited" {
		t.Fatalf("update not applied: %+v", edited)
	}

	upd, err := w.Pipelines.StartUpdate(ctx, pipelines.StartUpdate{
		PipelineId:  id,
		FullRefresh: true,
	})
	if err != nil {
		t.Fatalf("StartUpdate: %v", err)
	}

	if upd.UpdateId == "" {
		t.Fatal("expected update id from StartUpdate")
	}

	stopWait, err := w.Pipelines.Stop(ctx, pipelines.StopRequest{PipelineId: id})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	stopped, err := stopWait.Get()
	if err != nil {
		t.Fatalf("Stop wait: %v", err)
	}

	if stopped.State != pipelines.PipelineStateIdle {
		t.Fatalf("got state %q after stop, want IDLE", stopped.State)
	}

	if err = w.Pipelines.DeleteByPipelineId(ctx, id); err != nil {
		t.Fatalf("DeleteByPipelineId: %v", err)
	}

	if _, err = w.Pipelines.GetByPipelineId(ctx, id); err == nil {
		t.Fatal("expected error getting deleted pipeline")
	}
}

func TestSDKPipelineGetMissing(t *testing.T) {
	w := newPipelineClient(t)

	if _, err := w.Pipelines.GetByPipelineId(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected RESOURCE_DOES_NOT_EXIST error")
	}
}

func TestSDKPipelineListEmpty(t *testing.T) {
	w := newPipelineClient(t)

	all, err := w.Pipelines.ListPipelinesAll(context.Background(), pipelines.ListPipelinesRequest{})
	if err != nil {
		t.Fatalf("ListPipelinesAll: %v", err)
	}

	if len(all) != 0 {
		t.Fatalf("got %d pipelines, want 0", len(all))
	}
}
