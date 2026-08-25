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
