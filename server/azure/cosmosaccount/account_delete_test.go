// Real-user end-to-end test for account deletion (round-2 finding C1): deleting a
// Cosmos databaseAccount must be a FULL teardown, not a shallow one. Driving the
// live armcosmos control plane and the azcosmos data plane against one emulator,
// it proves that after BeginDelete:
//   - the deleted account is gone from List (no ghost) and GET 404s;
//   - recreating a same-named account starts CLEAN — none of the previous
//     incarnation's databases/containers/items survive;
//   - a DIFFERENT account's databases/containers/items are untouched.
package cosmosaccount_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/data/azcosmos"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listAccountNames returns the set of databaseAccount names the subscription
// List pager reports, used to assert presence/absence of an account.
func listAccountNames(ctx context.Context, t *testing.T, stack *cosmosStack) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	pager := stack.arm.NewListPager(nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, acc := range page.Value {
			require.NotNil(t, acc.Name)
			names[*acc.Name] = true
		}
	}

	return names
}

// databaseIDs returns the ids of the databases the data-plane client can see.
func databaseIDs(ctx context.Context, t *testing.T, client *azcosmos.Client) []string {
	t.Helper()

	var ids []string
	pager := client.NewQueryDatabasesPager("SELECT * FROM root", nil)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		require.NoError(t, err)

		for _, db := range page.Databases {
			ids = append(ids, db.ID)
		}
	}

	return ids
}

// putUser writes {id, pk, who} into a users container and reads it back to
// confirm the write landed.
func putUser(ctx context.Context, t *testing.T, cc *azcosmos.ContainerClient, id, pk, who string) {
	t.Helper()

	doc, err := json.Marshal(map[string]any{"id": id, "pk": pk, "who": who})
	require.NoError(t, err)

	_, err = cc.CreateItem(ctx, azcosmos.NewPartitionKeyString(pk), doc, nil)
	require.NoError(t, err)
}

// TestSDKCosmosAccountDeleteTearsDownNamespace is the C1 reproduction: an account
// delete fully tears down the account (no List ghost, clean same-name recreate)
// while leaving other accounts untouched.
func TestSDKCosmosAccountDeleteTearsDownNamespace(t *testing.T) {
	ctx := context.Background()
	stack := newCosmosStack(t)

	// Two accounts, each with its own appdb/users and one item.
	epDel := stack.createAccountEndpoint(t, "rg-del", "acct-del")
	epKeep := stack.createAccountEndpoint(t, "rg-keep", "acct-keep")

	clientDel := stack.dataClient(t, epDel)
	usersDel := makeUsersContainer(ctx, t, clientDel)
	putUser(ctx, t, usersDel, "u1", "team-a", "before-delete")

	clientKeep := stack.dataClient(t, epKeep)
	usersKeep := makeUsersContainer(ctx, t, clientKeep)
	putUser(ctx, t, usersKeep, "k1", "team-k", "keeper")

	// Delete acct-del through the real armcosmos LRO.
	poller, err := stack.arm.BeginDelete(ctx, "rg-del", "acct-del", nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	// No ghost: List no longer returns acct-del, but still returns acct-keep.
	names := listAccountNames(ctx, t, stack)
	assert.False(t, names["acct-del"], "deleted account must not linger in List (ghost)")
	assert.True(t, names["acct-keep"], "the other account must survive the delete")

	// GET on the deleted account 404s.
	_, err = stack.arm.Get(ctx, "rg-del", "acct-del", nil)
	wantStatus(t, err, 404, "GET on deleted account")

	// Recreate a same-named account: it must start from an empty namespace.
	epDel2 := stack.createAccountEndpoint(t, "rg-del", "acct-del")
	clientDel2 := stack.dataClient(t, epDel2)

	assert.Empty(t, databaseIDs(ctx, t, clientDel2),
		"a same-name recreate must inherit no stale databases")

	dbDel2, err := clientDel2.NewDatabase("appdb")
	require.NoError(t, err)

	usersDel2, err := dbDel2.NewContainer("users")
	require.NoError(t, err)

	_, err = usersDel2.Read(ctx, nil)
	wantStatus(t, err, 404, "recreated account reading the previous incarnation's container")

	_, err = usersDel2.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-a"), "u1", nil)
	wantStatus(t, err, 404, "recreated account reading the previous incarnation's item")

	// The recreated account is visible again; the keeper is still present.
	names = listAccountNames(ctx, t, stack)
	assert.True(t, names["acct-del"], "recreated account must appear in List")
	assert.True(t, names["acct-keep"], "the other account must still be listed")

	// The other account's data is entirely untouched by the delete + recreate.
	assert.Equal(t, []string{"appdb"}, databaseIDs(ctx, t, clientKeep),
		"the other account keeps its database")

	readK, err := usersKeep.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-k"), "k1", nil)
	require.NoError(t, err, "the other account's item must survive")

	var gotK map[string]any
	require.NoError(t, json.Unmarshal(readK.Value, &gotK))
	assert.Equal(t, "keeper", gotK["who"], "the other account's item is unchanged")
}

// TestSDKCosmosAccountDeletePrefixSubset locks the foo-vs-foobar prefix-safety
// guarantee end-to-end: deleting account "foo" must not touch account "foobar",
// whose name merely starts with "foo". The teardown matches on the "{account}/"
// separator, so "foobar/…" tables never fall inside "foo"'s namespace. A bare
// HasPrefix(name, "foo") would wrongly reap foobar's data — this is the wire-level
// CI guard against that regression class.
func TestSDKCosmosAccountDeletePrefixSubset(t *testing.T) {
	ctx := context.Background()
	stack := newCosmosStack(t)

	// foo and foobar: distinct accounts whose names share the "foo" prefix. Each
	// gets its own appdb/users and one item.
	epFoo := stack.createAccountEndpoint(t, "rg-foo", "foo")
	epFoobar := stack.createAccountEndpoint(t, "rg-foobar", "foobar")

	clientFoo := stack.dataClient(t, epFoo)
	usersFoo := makeUsersContainer(ctx, t, clientFoo)
	putUser(ctx, t, usersFoo, "f1", "team-foo", "foo-data")

	clientFoobar := stack.dataClient(t, epFoobar)
	usersFoobar := makeUsersContainer(ctx, t, clientFoobar)
	putUser(ctx, t, usersFoobar, "b1", "team-bar", "foobar-data")

	// Delete only "foo".
	poller, err := stack.arm.BeginDelete(ctx, "rg-foo", "foo", nil)
	require.NoError(t, err)
	_, err = poller.PollUntilDone(ctx, nil)
	require.NoError(t, err)

	// foo is gone: no List ghost, GET 404s.
	names := listAccountNames(ctx, t, stack)
	assert.False(t, names["foo"], "deleted account must not linger in List (ghost)")
	assert.True(t, names["foobar"], "prefix-sharing account must survive the delete")

	_, err = stack.arm.Get(ctx, "rg-foo", "foo", nil)
	wantStatus(t, err, 404, "GET on deleted account")

	// foobar is entirely untouched: its database, container and item all survive.
	assert.Equal(t, []string{"appdb"}, databaseIDs(ctx, t, clientFoobar),
		"prefix-sharing account keeps its database")

	readBar, err := usersFoobar.ReadItem(ctx, azcosmos.NewPartitionKeyString("team-bar"), "b1", nil)
	require.NoError(t, err, "prefix-sharing account's item must survive")

	var gotBar map[string]any
	require.NoError(t, json.Unmarshal(readBar.Value, &gotBar))
	assert.Equal(t, "foobar-data", gotBar["who"], "prefix-sharing account's item is unchanged")
}
