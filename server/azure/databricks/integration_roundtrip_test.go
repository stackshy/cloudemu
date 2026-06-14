package databricks_test

import (
	"context"
	"testing"

	"github.com/databricks/databricks-sdk-go/service/catalog"
	"github.com/databricks/databricks-sdk-go/service/compute"
	"github.com/databricks/databricks-sdk-go/service/files"
	"github.com/databricks/databricks-sdk-go/service/iam"
	"github.com/databricks/databricks-sdk-go/service/jobs"
	"github.com/databricks/databricks-sdk-go/service/pipelines"
	"github.com/databricks/databricks-sdk-go/service/settings"
	"github.com/databricks/databricks-sdk-go/service/sql"
	"github.com/databricks/databricks-sdk-go/service/workspace"
)

// TestSDKFullWorkspaceDataPlane stands up a single Azure server with the whole
// Databricks data plane registered and drives the real WorkspaceClient across
// every resource family through that one endpoint — proving the handlers
// coexist (disjoint routing) and behave like a real workspace.
func TestSDKFullWorkspaceDataPlane(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	t.Run("secrets", func(t *testing.T) {
		if err := w.Secrets.CreateScope(ctx, workspace.CreateScope{Scope: "s1"}); err != nil {
			t.Fatalf("CreateScope: %v", err)
		}

		if err := w.Secrets.PutSecret(ctx, workspace.PutSecret{Scope: "s1", Key: "k", StringValue: "v"}); err != nil {
			t.Fatalf("PutSecret: %v", err)
		}

		got, err := w.Secrets.GetSecret(ctx, workspace.GetSecretRequest{Scope: "s1", Key: "k"})
		if err != nil || got.Value == "" {
			t.Fatalf("GetSecret: %v (value=%q)", err, got.Value)
		}
	})

	t.Run("tokens", func(t *testing.T) {
		tok, err := w.Tokens.Create(ctx, settings.CreateTokenRequest{Comment: "c", LifetimeSeconds: 3600})
		if err != nil || tok.TokenValue == "" {
			t.Fatalf("Tokens.Create: %v", err)
		}
	})

	clusterID := t.Run("clusters", func(t *testing.T) {
		wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
			ClusterName: "c1", SparkVersion: "13.3.x-scala2.12", NodeTypeId: "Standard_DS3_v2", NumWorkers: 1,
		})
		if err != nil || wait.ClusterId == "" {
			t.Fatalf("Clusters.Create: %v", err)
		}

		if _, err = w.Clusters.GetByClusterId(ctx, wait.ClusterId); err != nil {
			t.Fatalf("Clusters.Get: %v", err)
		}
	})
	_ = clusterID

	t.Run("instance_pools", func(t *testing.T) {
		if _, err := w.InstancePools.Create(ctx, compute.CreateInstancePool{
			InstancePoolName: "p1", NodeTypeId: "Standard_DS3_v2",
		}); err != nil {
			t.Fatalf("InstancePools.Create: %v", err)
		}
	})

	t.Run("jobs_and_runs", func(t *testing.T) {
		job, err := w.Jobs.Create(ctx, jobs.CreateJob{Name: "j1"})
		if err != nil {
			t.Fatalf("Jobs.Create: %v", err)
		}

		run, err := w.Jobs.RunNow(ctx, jobs.RunNow{JobId: job.JobId})
		if err != nil || run.Response.RunId == 0 {
			t.Fatalf("Jobs.RunNow: %v", err)
		}

		if _, err = w.Jobs.GetRun(ctx, jobs.GetRunRequest{RunId: run.Response.RunId}); err != nil {
			t.Fatalf("Jobs.GetRun: %v", err)
		}
	})

	t.Run("cluster_policies", func(t *testing.T) {
		if _, err := w.ClusterPolicies.Create(ctx, compute.CreatePolicy{Name: "pol", Definition: "{}"}); err != nil {
			t.Fatalf("ClusterPolicies.Create: %v", err)
		}
	})

	t.Run("sql_warehouses", func(t *testing.T) {
		wait, err := w.Warehouses.Create(ctx, sql.CreateWarehouseRequest{Name: "wh1", ClusterSize: "2X-Small"})
		if err != nil || wait.Id == "" {
			t.Fatalf("Warehouses.Create: %v", err)
		}
	})

	t.Run("repos", func(t *testing.T) {
		if _, err := w.Repos.Create(ctx, workspace.CreateRepoRequest{
			Url: "https://github.com/x/y", Provider: "gitHub",
		}); err != nil {
			t.Fatalf("Repos.Create: %v", err)
		}
	})

	t.Run("git_credentials", func(t *testing.T) {
		if _, err := w.GitCredentials.Create(ctx, workspace.CreateCredentialsRequest{
			GitProvider: "gitHub", GitUsername: "u",
		}); err != nil {
			t.Fatalf("GitCredentials.Create: %v", err)
		}
	})

	t.Run("dbfs", func(t *testing.T) {
		if err := w.Dbfs.Mkdirs(ctx, files.MkDirs{Path: "/tmp/cloudemu"}); err != nil {
			t.Fatalf("Dbfs.Mkdirs: %v", err)
		}

		st, err := w.Dbfs.GetStatus(ctx, files.GetStatusRequest{Path: "/tmp/cloudemu"})
		if err != nil || !st.IsDir {
			t.Fatalf("Dbfs.GetStatus: %v (isDir=%v)", err, st.IsDir)
		}
	})

	t.Run("workspace", func(t *testing.T) {
		if err := w.Workspace.Mkdirs(ctx, workspace.Mkdirs{Path: "/Shared/cloudemu"}); err != nil {
			t.Fatalf("Workspace.Mkdirs: %v", err)
		}

		if _, err := w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: "/Shared/cloudemu"}); err != nil {
			t.Fatalf("Workspace.GetStatus: %v", err)
		}
	})

	t.Run("unity_catalog", func(t *testing.T) {
		if _, err := w.Catalogs.Create(ctx, catalog.CreateCatalog{Name: "cat"}); err != nil {
			t.Fatalf("Catalogs.Create: %v", err)
		}

		if _, err := w.Schemas.Create(ctx, catalog.CreateSchema{Name: "sch", CatalogName: "cat"}); err != nil {
			t.Fatalf("Schemas.Create: %v", err)
		}
	})

	t.Run("uc_storage", func(t *testing.T) {
		if _, err := w.Metastores.Create(ctx, catalog.CreateMetastore{Name: "ms"}); err != nil {
			t.Fatalf("Metastores.Create: %v", err)
		}
	})

	t.Run("scim", func(t *testing.T) {
		if _, err := w.Users.Create(ctx, iam.User{UserName: "alice@example.com"}); err != nil {
			t.Fatalf("Users.Create: %v", err)
		}
	})

	t.Run("pipelines", func(t *testing.T) {
		if _, err := w.Pipelines.ListPipelinesAll(ctx, pipelines.ListPipelinesRequest{}); err != nil {
			t.Fatalf("Pipelines.List: %v", err)
		}
	})

	t.Run("serving", func(t *testing.T) {
		if _, err := w.ServingEndpoints.ListAll(ctx); err != nil {
			t.Fatalf("ServingEndpoints.List: %v", err)
		}
	})
}
