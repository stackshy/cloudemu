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

// TestSDKFullWorkspaceDataPlane stands up a SINGLE Azure server with the whole
// Databricks data plane registered and drives the real WorkspaceClient through
// a full lifecycle of every resource family against that one endpoint — the
// way a real user would, proving the handlers coexist and behave correctly
// together (not just in isolation).
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

		secs, err := w.Secrets.ListSecretsAll(ctx, workspace.ListSecretsRequest{Scope: "s1"})
		if err != nil || len(secs) != 1 {
			t.Fatalf("ListSecrets: %v (n=%d)", err, len(secs))
		}

		if err := w.Secrets.DeleteSecret(ctx, workspace.DeleteSecret{Scope: "s1", Key: "k"}); err != nil {
			t.Fatalf("DeleteSecret: %v", err)
		}

		if err := w.Secrets.DeleteScope(ctx, workspace.DeleteScope{Scope: "s1"}); err != nil {
			t.Fatalf("DeleteScope: %v", err)
		}
	})

	t.Run("tokens", func(t *testing.T) {
		tok, err := w.Tokens.Create(ctx, settings.CreateTokenRequest{Comment: "c", LifetimeSeconds: 3600})
		if err != nil || tok.TokenValue == "" {
			t.Fatalf("Tokens.Create: %v", err)
		}

		list, err := w.Tokens.ListAll(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("Tokens.List: %v (n=%d)", err, len(list))
		}

		if err := w.Tokens.Delete(ctx, settings.RevokeTokenRequest{TokenId: tok.TokenInfo.TokenId}); err != nil {
			t.Fatalf("Tokens.Delete: %v", err)
		}
	})

	var clusterID string

	t.Run("clusters", func(t *testing.T) {
		wait, err := w.Clusters.Create(ctx, compute.CreateCluster{
			ClusterName: "c1", SparkVersion: "13.3.x-scala2.12", NodeTypeId: "Standard_DS3_v2", NumWorkers: 1,
		})
		if err != nil || wait.ClusterId == "" {
			t.Fatalf("Clusters.Create: %v", err)
		}

		clusterID = wait.ClusterId

		got, err := w.Clusters.GetByClusterId(ctx, clusterID)
		if err != nil || got.State != compute.StateRunning {
			t.Fatalf("Clusters.Get: %v (state=%v)", err, got.State)
		}

		if _, err := w.Clusters.Resize(ctx, compute.ResizeCluster{ClusterId: clusterID, NumWorkers: 2}); err != nil {
			t.Fatalf("Clusters.Resize: %v", err)
		}

		if err := w.Clusters.Pin(ctx, compute.PinCluster{ClusterId: clusterID}); err != nil {
			t.Fatalf("Clusters.Pin: %v", err)
		}

		if _, err := w.Clusters.ListAll(ctx, compute.ListClustersRequest{}); err != nil {
			t.Fatalf("Clusters.List: %v", err)
		}
	})

	t.Run("instance_pools", func(t *testing.T) {
		pool, err := w.InstancePools.Create(ctx, compute.CreateInstancePool{
			InstancePoolName: "p1", NodeTypeId: "Standard_DS3_v2",
		})
		if err != nil {
			t.Fatalf("InstancePools.Create: %v", err)
		}

		if _, err := w.InstancePools.GetByInstancePoolId(ctx, pool.InstancePoolId); err != nil {
			t.Fatalf("InstancePools.Get: %v", err)
		}

		if err := w.InstancePools.DeleteByInstancePoolId(ctx, pool.InstancePoolId); err != nil {
			t.Fatalf("InstancePools.Delete: %v", err)
		}
	})

	t.Run("cluster_policies", func(t *testing.T) {
		pol, err := w.ClusterPolicies.Create(ctx, compute.CreatePolicy{Name: "pol", Definition: "{}"})
		if err != nil {
			t.Fatalf("ClusterPolicies.Create: %v", err)
		}

		if _, err := w.ClusterPolicies.Get(ctx, compute.GetClusterPolicyRequest{PolicyId: pol.PolicyId}); err != nil {
			t.Fatalf("ClusterPolicies.Get: %v", err)
		}

		if err := w.ClusterPolicies.Delete(ctx, compute.DeletePolicy{PolicyId: pol.PolicyId}); err != nil {
			t.Fatalf("ClusterPolicies.Delete: %v", err)
		}
	})

	t.Run("libraries", func(t *testing.T) {
		lib := compute.Library{Pypi: &compute.PythonPyPiLibrary{Package: "requests"}}
		if err := w.Libraries.Install(ctx, compute.InstallLibraries{
			ClusterId: clusterID, Libraries: []compute.Library{lib},
		}); err != nil {
			t.Fatalf("Libraries.Install: %v", err)
		}

		st, err := w.Libraries.ClusterStatusByClusterId(ctx, clusterID)
		if err != nil || len(st.LibraryStatuses) != 1 {
			t.Fatalf("Libraries.ClusterStatus: %v", err)
		}

		if err := w.Libraries.Uninstall(ctx, compute.UninstallLibraries{
			ClusterId: clusterID, Libraries: []compute.Library{lib},
		}); err != nil {
			t.Fatalf("Libraries.Uninstall: %v", err)
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

		if _, err := w.Jobs.GetRun(ctx, jobs.GetRunRequest{RunId: run.Response.RunId}); err != nil {
			t.Fatalf("Jobs.GetRun: %v", err)
		}

		if _, err := w.Jobs.ListRunsAll(ctx, jobs.ListRunsRequest{JobId: job.JobId}); err != nil {
			t.Fatalf("Jobs.ListRuns: %v", err)
		}

		if err := w.Jobs.DeleteByJobId(ctx, job.JobId); err != nil {
			t.Fatalf("Jobs.Delete: %v", err)
		}
	})

	// terminate the cluster created earlier (lifecycle close-out)
	t.Run("cluster_terminate", func(t *testing.T) {
		if _, err := w.Clusters.Delete(ctx, compute.DeleteCluster{ClusterId: clusterID}); err != nil {
			t.Fatalf("Clusters.Delete: %v", err)
		}
	})

	t.Run("sql_warehouses", func(t *testing.T) {
		wait, err := w.Warehouses.Create(ctx, sql.CreateWarehouseRequest{Name: "wh1", ClusterSize: "2X-Small"})
		if err != nil || wait.Id == "" {
			t.Fatalf("Warehouses.Create: %v", err)
		}

		if _, err := w.Warehouses.GetById(ctx, wait.Id); err != nil {
			t.Fatalf("Warehouses.Get: %v", err)
		}

		if _, err := w.Warehouses.Stop(ctx, sql.StopRequest{Id: wait.Id}); err != nil {
			t.Fatalf("Warehouses.Stop: %v", err)
		}

		if err := w.Warehouses.DeleteById(ctx, wait.Id); err != nil {
			t.Fatalf("Warehouses.Delete: %v", err)
		}
	})

	t.Run("repos", func(t *testing.T) {
		repo, err := w.Repos.Create(ctx, workspace.CreateRepoRequest{Url: "https://github.com/x/y", Provider: "gitHub"})
		if err != nil {
			t.Fatalf("Repos.Create: %v", err)
		}

		if _, err := w.Repos.GetByRepoId(ctx, repo.Id); err != nil {
			t.Fatalf("Repos.Get: %v", err)
		}

		if err := w.Repos.DeleteByRepoId(ctx, repo.Id); err != nil {
			t.Fatalf("Repos.Delete: %v", err)
		}
	})

	t.Run("git_credentials", func(t *testing.T) {
		cred, err := w.GitCredentials.Create(ctx, workspace.CreateCredentialsRequest{GitProvider: "gitHub", GitUsername: "u"})
		if err != nil {
			t.Fatalf("GitCredentials.Create: %v", err)
		}

		if _, err := w.GitCredentials.GetByCredentialId(ctx, cred.CredentialId); err != nil {
			t.Fatalf("GitCredentials.Get: %v", err)
		}

		if err := w.GitCredentials.DeleteByCredentialId(ctx, cred.CredentialId); err != nil {
			t.Fatalf("GitCredentials.Delete: %v", err)
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

		if err := w.Dbfs.Delete(ctx, files.Delete{Path: "/tmp/cloudemu", Recursive: true}); err != nil {
			t.Fatalf("Dbfs.Delete: %v", err)
		}
	})

	t.Run("workspace", func(t *testing.T) {
		if err := w.Workspace.Mkdirs(ctx, workspace.Mkdirs{Path: "/Shared/cloudemu"}); err != nil {
			t.Fatalf("Workspace.Mkdirs: %v", err)
		}

		if _, err := w.Workspace.GetStatus(ctx, workspace.GetStatusRequest{Path: "/Shared/cloudemu"}); err != nil {
			t.Fatalf("Workspace.GetStatus: %v", err)
		}

		if err := w.Workspace.Delete(ctx, workspace.Delete{Path: "/Shared/cloudemu", Recursive: true}); err != nil {
			t.Fatalf("Workspace.Delete: %v", err)
		}
	})

	t.Run("unity_catalog", func(t *testing.T) {
		if _, err := w.Catalogs.Create(ctx, catalog.CreateCatalog{Name: "cat"}); err != nil {
			t.Fatalf("Catalogs.Create: %v", err)
		}

		if _, err := w.Schemas.Create(ctx, catalog.CreateSchema{Name: "sch", CatalogName: "cat"}); err != nil {
			t.Fatalf("Schemas.Create: %v", err)
		}

		if _, err := w.Schemas.GetByFullName(ctx, "cat.sch"); err != nil {
			t.Fatalf("Schemas.Get: %v", err)
		}

		if err := w.Catalogs.DeleteByName(ctx, "cat"); err != nil {
			t.Fatalf("Catalogs.Delete: %v", err)
		}
	})

	t.Run("uc_storage", func(t *testing.T) {
		ms, err := w.Metastores.Create(ctx, catalog.CreateMetastore{Name: "ms"})
		if err != nil {
			t.Fatalf("Metastores.Create: %v", err)
		}

		if _, err := w.Metastores.GetById(ctx, ms.MetastoreId); err != nil {
			t.Fatalf("Metastores.Get: %v", err)
		}

		if err := w.Metastores.DeleteById(ctx, ms.MetastoreId); err != nil {
			t.Fatalf("Metastores.Delete: %v", err)
		}
	})

	t.Run("scim", func(t *testing.T) {
		user, err := w.Users.Create(ctx, iam.User{UserName: "alice@example.com"})
		if err != nil {
			t.Fatalf("Users.Create: %v", err)
		}

		if _, err := w.Users.GetById(ctx, user.Id); err != nil {
			t.Fatalf("Users.Get: %v", err)
		}

		if err := w.Users.DeleteById(ctx, user.Id); err != nil {
			t.Fatalf("Users.Delete: %v", err)
		}
	})

	t.Run("pipelines", func(t *testing.T) {
		p, err := w.Pipelines.Create(ctx, pipelines.CreatePipeline{Name: "pl1"})
		if err != nil {
			t.Fatalf("Pipelines.Create: %v", err)
		}

		if _, err := w.Pipelines.GetByPipelineId(ctx, p.PipelineId); err != nil {
			t.Fatalf("Pipelines.Get: %v", err)
		}

		if _, err := w.Pipelines.ListPipelinesAll(ctx, pipelines.ListPipelinesRequest{}); err != nil {
			t.Fatalf("Pipelines.List: %v", err)
		}

		if err := w.Pipelines.DeleteByPipelineId(ctx, p.PipelineId); err != nil {
			t.Fatalf("Pipelines.Delete: %v", err)
		}
	})

	t.Run("serving", func(t *testing.T) {
		if _, err := w.ServingEndpoints.ListAll(ctx); err != nil {
			t.Fatalf("ServingEndpoints.List: %v", err)
		}
	})
}
