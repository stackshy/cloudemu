package unitycatalog_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/unitycatalog"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(unitycatalog.New())

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

func TestSDKCatalogLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Catalogs.Create(ctx, catalog.CreateCatalog{Name: "main", Comment: "primary"})
	require.NoError(t, err)
	assert.Equal(t, "main", created.Name)
	assert.Equal(t, "main", created.FullName)

	got, err := w.Catalogs.GetByName(ctx, "main")
	require.NoError(t, err)
	assert.Equal(t, "primary", got.Comment)

	updated, err := w.Catalogs.Update(ctx, catalog.UpdateCatalog{Name: "main", Comment: "updated"})
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Comment)

	all, err := w.Catalogs.ListAll(ctx, catalog.ListCatalogsRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "main", all[0].Name)

	err = w.Catalogs.DeleteByName(ctx, "main")
	require.NoError(t, err)

	_, err = w.Catalogs.GetByName(ctx, "main")
	require.Error(t, err)
}

func TestSDKSchemaLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	_, err := w.Catalogs.Create(ctx, catalog.CreateCatalog{Name: "main"})
	require.NoError(t, err)

	created, err := w.Schemas.Create(ctx, catalog.CreateSchema{Name: "sales", CatalogName: "main"})
	require.NoError(t, err)
	assert.Equal(t, "main.sales", created.FullName)

	got, err := w.Schemas.GetByFullName(ctx, "main.sales")
	require.NoError(t, err)
	assert.Equal(t, "main", got.CatalogName)

	updated, err := w.Schemas.Update(ctx, catalog.UpdateSchema{FullName: "main.sales", Comment: "c"})
	require.NoError(t, err)
	assert.Equal(t, "c", updated.Comment)

	all, err := w.Schemas.ListAll(ctx, catalog.ListSchemasRequest{CatalogName: "main"})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, "main.sales", all[0].FullName)

	err = w.Schemas.DeleteByFullName(ctx, "main.sales")
	require.NoError(t, err)

	_, err = w.Schemas.GetByFullName(ctx, "main.sales")
	require.Error(t, err)
}

func TestSDKTablesListAndGet(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	all, err := w.Tables.ListAll(ctx, catalog.ListTablesRequest{
		CatalogName: "main",
		SchemaName:  "sales",
	})
	require.NoError(t, err)
	assert.Empty(t, all)

	_, err = w.Tables.GetByFullName(ctx, "main.sales.orders")
	require.Error(t, err)
}
