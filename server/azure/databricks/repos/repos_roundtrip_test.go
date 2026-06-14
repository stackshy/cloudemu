package repos_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/server"
	"github.com/stackshy/cloudemu/server/azure/databricks/repos"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(repos.New())

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	w, err := databricks.NewWorkspaceClient(&databricks.Config{
		Host:        ts.URL,
		Token:       "test-token",
		Credentials: config.PatCredentials{},
	})
	require.NoError(t, err)

	return w
}

func TestSDKReposLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Repos.Create(ctx, workspace.CreateRepoRequest{
		Url:      "https://github.com/example/demo.git",
		Provider: "gitHub",
		Path:     "/Repos/team/demo",
	})
	require.NoError(t, err)
	assert.NotZero(t, created.Id)
	assert.Equal(t, "/Repos/team/demo", created.Path)
	assert.Equal(t, "gitHub", created.Provider)
	assert.Equal(t, "main", created.Branch)
	assert.NotEmpty(t, created.HeadCommitId)

	got, err := w.Repos.GetByRepoId(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, created.Id, got.Id)
	assert.Equal(t, "https://github.com/example/demo.git", got.Url)

	err = w.Repos.Update(ctx, workspace.UpdateRepoRequest{
		RepoId: created.Id,
		Branch: "develop",
	})
	require.NoError(t, err)

	afterUpdate, err := w.Repos.GetByRepoId(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, "develop", afterUpdate.Branch)

	all, err := w.Repos.ListAll(ctx, workspace.ListReposRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 1)
	assert.Equal(t, created.Id, all[0].Id)
	assert.Equal(t, "develop", all[0].Branch)

	err = w.Repos.DeleteByRepoId(ctx, created.Id)
	require.NoError(t, err)

	_, err = w.Repos.GetByRepoId(ctx, created.Id)
	require.Error(t, err)
}

func TestSDKReposDefaultPath(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Repos.Create(ctx, workspace.CreateRepoRequest{
		Url:      "https://github.com/example/no-path.git",
		Provider: "gitHub",
	})
	require.NoError(t, err)
	assert.Equal(t, "/Repos/cloudemu/no-path", created.Path)
}

func TestSDKReposGetMissing(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	_, err := w.Repos.GetByRepoId(ctx, 999)
	require.Error(t, err)
}
