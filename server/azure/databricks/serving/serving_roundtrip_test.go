package serving_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	dbserving "github.com/databricks/databricks-sdk-go/service/serving"

	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/serving"
)

func newServingClient(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New()
	srv.Register(serving.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:               ts.URL,
		Token:              "x",
		Credentials:        config.PatCredentials{},
		HTTPTimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("new workspace client: %v", err)
	}

	return w
}

func entityConfig(version string) *dbserving.EndpointCoreConfigInput {
	return &dbserving.EndpointCoreConfigInput{
		ServedEntities: []dbserving.ServedEntityInput{
			{
				Name:               "m",
				EntityName:         "main.default.model",
				EntityVersion:      version,
				ScaleToZeroEnabled: true,
			},
		},
		TrafficConfig: &dbserving.TrafficConfig{
			Routes: []dbserving.Route{
				{ServedModelName: "m", TrafficPercentage: 100},
			},
		},
	}
}

func TestSDKServingEndpointLifecycle(t *testing.T) {
	w := newServingClient(t)
	ctx := context.Background()

	wait, err := w.ServingEndpoints.Create(ctx, dbserving.CreateServingEndpoint{
		Name:        "ep-1",
		Description: "test endpoint",
		Config:      entityConfig("1"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created, err := wait.Get()
	if err != nil {
		t.Fatalf("Create wait: %v", err)
	}

	if created.Name != "ep-1" {
		t.Fatalf("got name %q after create, want ep-1", created.Name)
	}

	if created.State == nil || created.State.ConfigUpdate != dbserving.EndpointStateConfigUpdateNotUpdating {
		t.Fatalf("create state not terminal: %+v", created.State)
	}

	if created.State.Ready != dbserving.EndpointStateReadyReady {
		t.Fatalf("got ready %q, want READY", created.State.Ready)
	}

	got, err := w.ServingEndpoints.Get(ctx, dbserving.GetServingEndpointRequest{Name: "ep-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Description != "test endpoint" {
		t.Fatalf("unexpected description: %q", got.Description)
	}

	if got.Config == nil || len(got.Config.ServedEntities) != 1 ||
		got.Config.ServedEntities[0].EntityVersion != "1" {
		t.Fatalf("config not round-tripped: %+v", got.Config)
	}

	all, err := w.ServingEndpoints.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 1 || all[0].Name != "ep-1" {
		t.Fatalf("got %d endpoints, want 1 named ep-1: %+v", len(all), all)
	}

	updWait, err := w.ServingEndpoints.UpdateConfig(ctx, dbserving.EndpointCoreConfigInput{
		Name:           "ep-1",
		ServedEntities: entityConfig("2").ServedEntities,
		TrafficConfig:  entityConfig("2").TrafficConfig,
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	updated, err := updWait.Get()
	if err != nil {
		t.Fatalf("UpdateConfig wait: %v", err)
	}

	if updated.Config == nil || len(updated.Config.ServedEntities) != 1 ||
		updated.Config.ServedEntities[0].EntityVersion != "2" {
		t.Fatalf("update not applied: %+v", updated.Config)
	}

	if err = w.ServingEndpoints.Delete(ctx, dbserving.DeleteServingEndpointRequest{Name: "ep-1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err = w.ServingEndpoints.Get(ctx, dbserving.GetServingEndpointRequest{Name: "ep-1"}); err == nil {
		t.Fatal("expected error getting deleted endpoint")
	}
}

func TestSDKServingEndpointGetMissing(t *testing.T) {
	w := newServingClient(t)

	_, err := w.ServingEndpoints.Get(context.Background(), dbserving.GetServingEndpointRequest{Name: "nope"})
	if err == nil {
		t.Fatal("expected RESOURCE_DOES_NOT_EXIST error")
	}
}

func TestSDKServingEndpointListEmpty(t *testing.T) {
	w := newServingClient(t)

	all, err := w.ServingEndpoints.ListAll(context.Background())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	if len(all) != 0 {
		t.Fatalf("got %d endpoints, want 0", len(all))
	}
}

func TestSDKServingEndpointDuplicate(t *testing.T) {
	w := newServingClient(t)
	ctx := context.Background()

	if _, err := w.ServingEndpoints.Create(ctx, dbserving.CreateServingEndpoint{
		Name:   "dup",
		Config: entityConfig("1"),
	}); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	if _, err := w.ServingEndpoints.Create(ctx, dbserving.CreateServingEndpoint{
		Name:   "dup",
		Config: entityConfig("1"),
	}); err == nil {
		t.Fatal("expected RESOURCE_ALREADY_EXISTS error")
	}
}
