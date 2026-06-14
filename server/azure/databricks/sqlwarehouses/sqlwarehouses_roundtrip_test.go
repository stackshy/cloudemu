package sqlwarehouses_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/sql"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/sqlwarehouses"
)

func newWarehouseClient(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(sqlwarehouses.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "test-token",
		Credentials: config.PatCredentials{},
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

func TestSDKWarehouseLifecycle(t *testing.T) {
	w := newWarehouseClient(t)
	ctx := context.Background()

	wait, err := w.Warehouses.Create(ctx, sql.CreateWarehouseRequest{
		Name:           "wh-1",
		ClusterSize:    "Small",
		AutoStopMins:   30,
		MaxNumClusters: 2,
		MinNumClusters: 1,
		EnablePhoton:   true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	id := wait.Id
	if id == "" {
		t.Fatal("expected warehouse id from waiter")
	}

	created, err := wait.Get()
	if err != nil {
		t.Fatalf("Create wait: %v", err)
	}

	if created.State != sql.StateRunning {
		t.Fatalf("got state %q after create, want RUNNING", created.State)
	}

	got, err := w.Warehouses.GetById(ctx, id)
	if err != nil {
		t.Fatalf("GetById: %v", err)
	}

	if got.Name != "wh-1" || got.ClusterSize != "Small" {
		t.Fatalf("unexpected warehouse: %+v", got)
	}

	if got.State != sql.StateRunning {
		t.Fatalf("got state %q, want RUNNING", got.State)
	}

	all, err := w.Warehouses.ListAll(ctx, sql.ListWarehousesRequest{})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 1 {
		t.Fatalf("got %d warehouses, want 1", len(all))
	}

	if all[0].Id != id {
		t.Fatalf("listed id %q, want %q", all[0].Id, id)
	}

	editWait, err := w.Warehouses.Edit(ctx, sql.EditWarehouseRequest{
		Id:          id,
		Name:        "wh-1-edited",
		ClusterSize: "Medium",
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if _, err = editWait.Get(); err != nil {
		t.Fatalf("Edit wait: %v", err)
	}

	edited, err := w.Warehouses.GetById(ctx, id)
	if err != nil {
		t.Fatalf("GetById after edit: %v", err)
	}

	if edited.Name != "wh-1-edited" || edited.ClusterSize != "Medium" {
		t.Fatalf("edit not applied: %+v", edited)
	}

	stopWait, err := w.Warehouses.Stop(ctx, sql.StopRequest{Id: id})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	stopped, err := stopWait.Get()
	if err != nil {
		t.Fatalf("Stop wait: %v", err)
	}

	if stopped.State != sql.StateStopped {
		t.Fatalf("got state %q after stop, want STOPPED", stopped.State)
	}

	startWait, err := w.Warehouses.Start(ctx, sql.StartRequest{Id: id})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	running, err := startWait.Get()
	if err != nil {
		t.Fatalf("Start wait: %v", err)
	}

	if running.State != sql.StateRunning {
		t.Fatalf("got state %q after start, want RUNNING", running.State)
	}

	if err = w.Warehouses.DeleteById(ctx, id); err != nil {
		t.Fatalf("DeleteById: %v", err)
	}

	if _, err = w.Warehouses.GetById(ctx, id); err == nil {
		t.Fatal("expected error getting deleted warehouse")
	}
}

func TestSDKWarehouseGetMissing(t *testing.T) {
	w := newWarehouseClient(t)

	if _, err := w.Warehouses.GetById(context.Background(), "does-not-exist"); err == nil {
		t.Fatal("expected RESOURCE_DOES_NOT_EXIST error")
	}
}

func TestSDKWarehouseListEmpty(t *testing.T) {
	w := newWarehouseClient(t)

	all, err := w.Warehouses.ListAll(context.Background(), sql.ListWarehousesRequest{})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 0 {
		t.Fatalf("got %d warehouses, want 0", len(all))
	}
}
