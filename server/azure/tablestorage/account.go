package tablestorage

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// WithAccounts scopes tables to the storage accounts in accounts: a table
// created through {account}.table.core.windows.net (or the path-style
// /{account}/ prefix) lives only in that account. Without it every request
// uses the default account.
func (h *Handler) WithAccounts(accounts storagedriver.AzureStorageAccounts) *Handler {
	h.accounts = accounts
	return h
}

// resolve returns the account a request targets and the path below it. The
// {account}.table host names the account when it is an existing storage
// account; otherwise a path-style /{account}/ prefix does. The default account
// "cloudemu" and the bare host map to the default namespace (""), which also
// holds every table from before accounts were modeled.
func (h *Handler) resolve(r *http.Request) (account, path string) {
	if acct, svc, ok := azurearm.StorageHost(r.Host); ok && svc == azurearm.StorageServiceTable && h.accountExists(r, acct) {
		return acct, r.URL.Path
	}

	rootOp := azurearm.IsStorageServiceOp(r.URL.Query())

	return azurearm.PeelStorageAccount(r.URL.Path, rootOp, func(name string) bool {
		return h.accountExists(r, name)
	})
}

func (h *Handler) accountExists(r *http.Request, name string) bool {
	if h.accounts == nil || name == storagedriver.AzureDefaultStorageAccount {
		return false
	}

	_, err := h.accounts.GetStorageAccount(r.Context(), name)

	return err == nil
}

// tableKey is the driver name of table in account: the bare name in the
// default account, "{account}/{table}" in any other. Table names are
// alphanumeric, so keys never collide.
func tableKey(account, table string) string {
	return storagedriver.AzureContainerKey(account, table)
}

// tableName is the table's own name within its account.
func tableName(key string) string {
	_, name := storagedriver.SplitAzureContainerKey(key)
	return name
}

// isTableClient reports whether a root request on a host that does not name
// its service comes from a Table client. The account-level service calls have
// the same shape for Blob, Queue and Table, so on a bare host the Azure SDK's
// user agent (azsdk-go-aztables, azsdk-python-data-tables and so on) tells
// them apart.
func isTableClient(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.UserAgent()), "table")
}
