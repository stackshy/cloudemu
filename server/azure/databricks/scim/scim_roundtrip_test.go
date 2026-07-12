package scim_test

import (
	"context"
	"net/http/httptest"
	"testing"

	databricks "github.com/databricks/databricks-sdk-go"
	"github.com/databricks/databricks-sdk-go/config"
	"github.com/databricks/databricks-sdk-go/service/iam"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/server"
	"github.com/stackshy/cloudemu/v2/server/azure/databricks/scim"
)

func newWorkspace(t *testing.T) *databricks.WorkspaceClient {
	t.Helper()

	srv := server.New(scim.New())

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

func TestSDKUsersLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Users.Create(ctx, iam.User{
		UserName:    "alice@example.com",
		DisplayName: "Alice",
		Active:      true,
		Emails: []iam.ComplexValue{
			{Value: "alice@example.com", Primary: true, Type: "work"},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.Id)
	assert.Equal(t, "alice@example.com", created.UserName)

	got, err := w.Users.GetById(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, created.Id, got.Id)
	assert.Equal(t, "Alice", got.DisplayName)
	require.Len(t, got.Emails, 1)
	assert.Equal(t, "alice@example.com", got.Emails[0].Value)

	all, err := w.Users.ListAll(ctx, iam.ListUsersRequest{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, created.Id, all[0].Id)

	err = w.Users.Update(ctx, iam.User{
		Id:          created.Id,
		UserName:    "alice@example.com",
		DisplayName: "Alice Updated",
		Active:      true,
	})
	require.NoError(t, err)

	got, err = w.Users.GetById(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, "Alice Updated", got.DisplayName)

	err = w.Users.DeleteById(ctx, created.Id)
	require.NoError(t, err)

	all, err = w.Users.ListAll(ctx, iam.ListUsersRequest{})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestSDKGroupsLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.Groups.Create(ctx, iam.Group{
		DisplayName: "engineers",
		Members: []iam.ComplexValue{
			{Value: "123", Display: "alice"},
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.Id)
	assert.Equal(t, "engineers", created.DisplayName)

	got, err := w.Groups.GetById(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, created.Id, got.Id)
	require.Len(t, got.Members, 1)
	assert.Equal(t, "123", got.Members[0].Value)

	all, err := w.Groups.ListAll(ctx, iam.ListGroupsRequest{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, created.Id, all[0].Id)

	err = w.Groups.DeleteById(ctx, created.Id)
	require.NoError(t, err)

	all, err = w.Groups.ListAll(ctx, iam.ListGroupsRequest{})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestSDKServicePrincipalsLifecycle(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	created, err := w.ServicePrincipals.Create(ctx, iam.ServicePrincipal{
		DisplayName: "ci-runner",
		Active:      true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.Id)
	require.NotEmpty(t, created.ApplicationId)
	assert.Equal(t, "ci-runner", created.DisplayName)

	got, err := w.ServicePrincipals.GetById(ctx, created.Id)
	require.NoError(t, err)
	assert.Equal(t, created.Id, got.Id)
	assert.Equal(t, created.ApplicationId, got.ApplicationId)

	all, err := w.ServicePrincipals.ListAll(ctx, iam.ListServicePrincipalsRequest{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, created.Id, all[0].Id)

	err = w.ServicePrincipals.DeleteById(ctx, created.Id)
	require.NoError(t, err)

	all, err = w.ServicePrincipals.ListAll(ctx, iam.ListServicePrincipalsRequest{})
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestSDKListMultipleReturns(t *testing.T) {
	w := newWorkspace(t)
	ctx := context.Background()

	for _, n := range []string{"u1@example.com", "u2@example.com", "u3@example.com"} {
		_, err := w.Users.Create(ctx, iam.User{UserName: n})
		require.NoError(t, err)
	}

	all, err := w.Users.ListAll(ctx, iam.ListUsersRequest{})
	require.NoError(t, err)
	assert.Len(t, all, 3)
}
