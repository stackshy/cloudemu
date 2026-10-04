package queue

import (
	"net/http"
	"slices"
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

// queueSDKProducts are the User-Agent product names of the Azure SDK Queue
// clients, lowercased.
//
//nolint:gochecknoglobals // read-only lookup table, not mutable state
var queueSDKProducts = []string{
	"azsdk-go-azqueue",
	"azsdk-python-storage-queue",
	"azsdk-java-azure-storage-queue",
	"azsdk-js-storage-queue",
	"azsdk-net-storage.queues",
}

// isQueueClient reports whether a request on a host that does not name its
// service comes from an Azure SDK Queue client. List Queues and the
// account-level service calls have the same shape as their Blob counterparts,
// so on a bare host only a product token of the User-Agent (such as
// "azsdk-go-azqueue/v1.0.0") picks Queue. Free text such as an application id
// is ignored, and anything else goes to Blob. Other Queue clients should use
// the {account}.queue host or the path-style /{account}/ form.
func isQueueClient(r *http.Request) bool {
	for _, token := range strings.Fields(strings.ToLower(r.UserAgent())) {
		product, _, _ := strings.Cut(token, "/")

		if slices.Contains(queueSDKProducts, product) {
			return true
		}
	}

	return false
}
