package azurearm

import "strings"

// storageServices are the Azure Storage data-plane service labels that sit
// between the account name and the storage DNS suffix, as in
// {account}.blob.core.windows.net.
//
//nolint:gochecknoglobals // read-only lookup set, not mutable state
var storageServices = map[string]bool{"blob": true, "queue": true, "table": true, "file": true, "dfs": true}

// storageSuffixes are the Azure Storage DNS suffixes across the public, China
// and US Gov clouds.
//
//nolint:gochecknoglobals // read-only lookup table, not mutable state
var storageSuffixes = []string{".core.windows.net", ".core.chinacloudapi.cn", ".core.usgovcloudapi.net"}

// IsStorageHost reports whether host is an {account}.{service}.{suffix}
// storage account endpoint, matched case-insensitively. A trailing :port is
// ignored. Data-plane handlers whose path roots collide with blob container
// names (Key Vault "keys", Cosmos "dbs", Search "indexes") use it to leave
// storage account requests to the storage handlers.
func IsStorageHost(host string) bool {
	_, _, ok := StorageHost(host)

	return ok
}

// StorageHost splits an {account}.{service}.{suffix} storage endpoint into its
// lowercased account name and service label (blob, queue, table, file, dfs).
// A trailing :port is ignored. ok is false for any other host, including a
// bare host such as localhost:4568.
func StorageHost(host string) (account, service string, ok bool) {
	host, _, _ = strings.Cut(strings.ToLower(host), ":")

	for _, suffix := range storageSuffixes {
		prefix, found := strings.CutSuffix(host, suffix)
		if !found {
			continue
		}

		dot := strings.LastIndexByte(prefix, '.')
		if dot <= 0 || !storageServices[prefix[dot+1:]] {
			return "", "", false
		}

		return prefix[:dot], prefix[dot+1:], true
	}

	return "", "", false
}
