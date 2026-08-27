package cosmosdb

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPurgeAccount verifies the account-delete teardown drops the account's own
// table, its discovery attributes and every data-plane container table in its
// namespace, while leaving another account's namespace and attributes intact.
func TestPurgeAccount(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// acct1: the account table, its attributes and one container table.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct1"}))
	m.SetTableAttributes("acct1", driver.AccountAttributes{Kind: "GlobalDocumentDB"})
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct1/db1/coll1", PartitionKey: "pk"}))

	// acct2: an independent account that must survive acct1's purge.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct2"}))
	m.SetTableAttributes("acct2", driver.AccountAttributes{Kind: "MongoDB"})
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct2/db1/coll1", PartitionKey: "pk"}))

	m.PurgeAccount("acct1")

	// acct1 is gone: not listed as an account, its own table and its container
	// table no longer describe.
	assert.NotContains(t, m.AccountTables(), "acct1", "purged account must not linger in AccountTables")

	_, err := m.DescribeTable(ctx, "acct1")
	require.Error(t, err, "purged account's own table must be gone")

	_, err = m.DescribeTable(ctx, "acct1/db1/coll1")
	require.Error(t, err, "purged account's container table must be gone")

	// acct2 is untouched.
	assert.Contains(t, m.AccountTables(), "acct2", "the other account must survive the purge")

	_, err = m.DescribeTable(ctx, "acct2/db1/coll1")
	require.NoError(t, err, "the other account's container table must survive")
}

// TestPurgeAccountPrefixSubset locks the foo-vs-foobar prefix-safety guarantee:
// purging "foo" must not touch "foobar", whose name merely starts with "foo".
// The purge matches on the "{account}/" separator, so "foobar/…" tables never
// fall inside "foo"'s namespace. A bare HasPrefix(name, "foo") would wrongly
// reap foobar's data — this test is the CI guard against that regression class.
func TestPurgeAccountPrefixSubset(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	// foo: account table + attributes + a container holding one item.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "foo"}))
	m.SetTableAttributes("foo", driver.AccountAttributes{Kind: "GlobalDocumentDB"})
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "foo/db1/coll1", PartitionKey: "pk"}))
	require.NoError(t, m.PutItem(ctx, "foo/db1/coll1", map[string]any{"pk": "a", "v": "foo-item"}))

	// foobar: a distinct account whose name has "foo" as a prefix; it must survive.
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "foobar"}))
	m.SetTableAttributes("foobar", driver.AccountAttributes{Kind: "MongoDB"})
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "foobar/db1/coll1", PartitionKey: "pk"}))
	require.NoError(t, m.PutItem(ctx, "foobar/db1/coll1", map[string]any{"pk": "a", "v": "foobar-item"}))

	m.PurgeAccount("foo")

	// foo and its data are gone.
	assert.NotContains(t, m.AccountTables(), "foo", "purged account must not linger")

	_, err := m.DescribeTable(ctx, "foo")
	require.Error(t, err, "purged account's own table must be gone")

	_, err = m.DescribeTable(ctx, "foo/db1/coll1")
	require.Error(t, err, "purged account's container table must be gone")

	// foobar survives untouched — table, attributes and its item.
	assert.Contains(t, m.AccountTables(), "foobar", "prefix-sharing account must survive")

	_, err = m.DescribeTable(ctx, "foobar/db1/coll1")
	require.NoError(t, err, "prefix-sharing account's container table must survive")

	item, err := m.GetItem(ctx, "foobar/db1/coll1", map[string]any{"pk": "a"})
	require.NoError(t, err, "prefix-sharing account's item must survive")
	assert.Equal(t, "foobar-item", item["v"])
}

// TestPurgeAccountEmpty verifies the empty-account guard: purging "" is a no-op
// and never reaps unrelated tables. nsPrefix("")=="" makes HasPrefix(t,"")
// always true, so without the guard an empty account would match and delete
// every table — a latent match-all data-loss footgun.
func TestPurgeAccountEmpty(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct1"}))
	m.SetTableAttributes("acct1", driver.AccountAttributes{Kind: "GlobalDocumentDB"})
	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct1/db1/coll1", PartitionKey: "pk"}))

	m.PurgeAccount("")

	assert.Contains(t, m.AccountTables(), "acct1", "empty-account purge must not drop real accounts")

	_, err := m.DescribeTable(ctx, "acct1")
	require.NoError(t, err, "empty-account purge must not drop unrelated tables")

	_, err = m.DescribeTable(ctx, "acct1/db1/coll1")
	require.NoError(t, err, "empty-account purge must not drop unrelated container tables")
}

// TestPurgeAccountUnknown verifies purging an account that was never created is a
// no-op that leaves existing state intact.
func TestPurgeAccountUnknown(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	require.NoError(t, m.CreateTable(ctx, driver.TableConfig{Name: "acct1"}))
	m.SetTableAttributes("acct1", driver.AccountAttributes{Kind: "GlobalDocumentDB"})

	m.PurgeAccount("does-not-exist")

	assert.Contains(t, m.AccountTables(), "acct1")

	_, err := m.DescribeTable(ctx, "acct1")
	require.NoError(t, err)
}
