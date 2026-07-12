package ucstorage_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/ucstorage"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(ucstorage.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:               ts.URL,
		Token:              "test-token",
		Credentials:        config.PatCredentials{},
		HTTPTimeoutSeconds: 10,
	})
	require.NoError(t, err)

	return w
}

func TestSDKMetastoresLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Metastores.Create(ctx, catalog.CreateMetastore{
		Name:        "main",
		Region:      "westus",
		StorageRoot: "abfss://root@acct.dfs.core.windows.net/",
	})
	require.NoError(t, err)
	assert.Equal(t, "main", created.Name)
	assert.NotEmpty(t, created.MetastoreId)

	got, err := w.Metastores.Get(ctx, catalog.GetMetastoreRequest{Id: created.MetastoreId})
	require.NoError(t, err)
	assert.Equal(t, "westus", got.Region)

	updated, err := w.Metastores.Update(ctx, catalog.UpdateMetastore{
		Id:      created.MetastoreId,
		NewName: "renamed",
	})
	require.NoError(t, err)
	assert.Equal(t, "renamed", updated.Name)

	all, err := w.Metastores.ListAll(ctx, catalog.ListMetastoresRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "renamed", all[0].Name)

	err = w.Metastores.Delete(ctx, catalog.DeleteMetastoreRequest{Id: updated.MetastoreId})
	require.NoError(t, err)

	_, err = w.Metastores.Get(ctx, catalog.GetMetastoreRequest{Id: updated.MetastoreId})
	require.Error(t, err)
}

func TestSDKStorageCredentialsLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.StorageCredentials.Create(ctx, catalog.CreateStorageCredential{
		Name:    "cred1",
		Comment: "demo",
		AzureManagedIdentity: &catalog.AzureManagedIdentityRequest{
			AccessConnectorId: "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Databricks/accessConnectors/ac",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "cred1", created.Name)
	require.NotNil(t, created.AzureManagedIdentity)
	assert.Contains(t, created.AzureManagedIdentity.AccessConnectorId, "accessConnectors/ac")

	got, err := w.StorageCredentials.Get(ctx, catalog.GetStorageCredentialRequest{Name: "cred1"})
	require.NoError(t, err)
	assert.Equal(t, "demo", got.Comment)

	all, err := w.StorageCredentials.ListAll(ctx, catalog.ListStorageCredentialsRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 1)

	err = w.StorageCredentials.Delete(ctx, catalog.DeleteStorageCredentialRequest{Name: "cred1"})
	require.NoError(t, err)

	_, err = w.StorageCredentials.Get(ctx, catalog.GetStorageCredentialRequest{Name: "cred1"})
	require.Error(t, err)
}

func TestSDKExternalLocationsLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	_, err := w.StorageCredentials.Create(ctx, catalog.CreateStorageCredential{
		Name: "loc-cred",
		AzureManagedIdentity: &catalog.AzureManagedIdentityRequest{
			AccessConnectorId: "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Databricks/accessConnectors/ac",
		},
	})
	require.NoError(t, err)

	created, err := w.ExternalLocations.Create(ctx, catalog.CreateExternalLocation{
		Name:           "loc1",
		Url:            "abfss://data@acct.dfs.core.windows.net/dir",
		CredentialName: "loc-cred",
	})
	require.NoError(t, err)
	assert.Equal(t, "loc1", created.Name)
	assert.Equal(t, "loc-cred", created.CredentialName)

	got, err := w.ExternalLocations.Get(ctx, catalog.GetExternalLocationRequest{Name: "loc1"})
	require.NoError(t, err)
	assert.Equal(t, "abfss://data@acct.dfs.core.windows.net/dir", got.Url)

	all, err := w.ExternalLocations.ListAll(ctx, catalog.ListExternalLocationsRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 1)

	err = w.ExternalLocations.Delete(ctx, catalog.DeleteExternalLocationRequest{Name: "loc1"})
	require.NoError(t, err)

	_, err = w.ExternalLocations.Get(ctx, catalog.GetExternalLocationRequest{Name: "loc1"})
	require.Error(t, err)
}

func TestSDKVolumesLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Volumes.Create(ctx, catalog.CreateVolumeRequestContent{
		CatalogName: "cat",
		SchemaName:  "sch",
		Name:        "vol",
		VolumeType:  catalog.VolumeTypeManaged,
		Comment:     "demo volume",
	})
	require.NoError(t, err)
	assert.Equal(t, "cat.sch.vol", created.FullName)
	assert.Equal(t, catalog.VolumeTypeManaged, created.VolumeType)

	got, err := w.Volumes.Read(ctx, catalog.ReadVolumeRequest{Name: "cat.sch.vol"})
	require.NoError(t, err)
	assert.Equal(t, "demo volume", got.Comment)

	all, err := w.Volumes.ListAll(ctx, catalog.ListVolumesRequest{
		CatalogName: "cat",
		SchemaName:  "sch",
	})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "cat.sch.vol", all[0].FullName)

	err = w.Volumes.Delete(ctx, catalog.DeleteVolumeRequest{Name: "cat.sch.vol"})
	require.NoError(t, err)

	_, err = w.Volumes.Read(ctx, catalog.ReadVolumeRequest{Name: "cat.sch.vol"})
	require.Error(t, err)
}
