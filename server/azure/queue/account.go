package queue

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	storagedriver "github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// WithAccounts scopes queues to the storage accounts in accounts: a queue
// created through {account}.queue.core.windows.net (or the path-style
// /{account}/ prefix) lives only in that account. Without it every request
// uses the default account.
func (h *Handler) WithAccounts(accounts storagedriver.AzureStorageAccounts) *Handler {
	h.accounts = accounts
	return h
}

// resolve returns the account a request targets and the path below it. The
// {account}.queue host names the account when it is an existing storage
// account; otherwise a path-style /{account}/ prefix does. The default account
// "cloudemu" and the bare host map to the default namespace (""), which also
// holds every queue from before accounts were modeled.
func (h *Handler) resolve(r *http.Request) (account, path string) {
	if acct, svc, ok := azurearm.StorageHost(r.Host); ok && svc == azurearm.StorageServiceQueue && h.accountExists(r, acct) {
		return acct, r.URL.Path
	}

	q := r.URL.Query()
	rootOp := q.Get("comp") == compList || azurearm.IsStorageServiceOp(q)

	return azurearm.PeelStorageAccount(r.URL.Path, rootOp, func(name string) bool {
		// A default-namespace queue of the same name keeps its URL.
		return h.accountExists(r, name) && !h.queueExists(r, name)
	})
}

func (h *Handler) accountExists(r *http.Request, name string) bool {
	if h.accounts == nil || name == storagedriver.AzureDefaultStorageAccount {
		return false
	}

	_, err := h.accounts.GetStorageAccount(r.Context(), name)

	return err == nil
}

func (h *Handler) queueExists(r *http.Request, key string) bool {
	_, err := h.resolveQueueURL(r, key)
	return err == nil
}

// queueKey is the driver name of queue in account: the bare name in the
// default account, "{account}/{queue}" in any other. Queue names cannot hold
// "/", so keys never collide.
func queueKey(account, queue string) string {
	return storagedriver.AzureContainerKey(account, queue)
}

// inAccount reports whether the driver queue key belongs to account and, if
// so, the queue's own name.
func inAccount(key, account string) (string, bool) {
	acct, name := storagedriver.SplitAzureContainerKey(key)
	if acct == storagedriver.AzureDefaultStorageAccount {
		acct = ""
	}

	return name, acct == account
}

// isQueueClient reports whether a request on a host that does not name its
// service comes from a Queue client. List Queues and the account-level
// service calls have the same shape as their Blob counterparts, so on a bare
// host the Azure SDK's user agent (azsdk-go-azqueue, azsdk-python-storage-queue,
// azsdk-net-Storage.Queues and so on) tells them apart; anything else is Blob.
func isQueueClient(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.UserAgent()), "queue")
}
