package azurearm

import (
	"net/url"
	"strings"
)

// Storage service labels of an {account}.{service}.{suffix} host.
const (
	StorageServiceQueue = "queue"
	StorageServiceTable = "table"
)

// restype values of the account-level storage service operations.
const (
	restypeService = "service"
	restypeAccount = "account"
)

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

// PeelStorageAccount reads a path-style storage request
// (https://host:port/{account}/...), as the SDKs build it for an IP or
// emulator endpoint. The leading segment is taken as the account, and the
// rest of the path returned, when isAccount accepts it and either more path
// follows or the request is an account-level operation (rootOp), such as
// "GET /{account}?comp=list". Otherwise the request belongs to the default
// account ("") and path is returned unchanged.
func PeelStorageAccount(path string, rootOp bool, isAccount func(name string) bool) (account, rest string) {
	name, after, more := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if name == "" || (!more && !rootOp) || !isAccount(name) {
		return "", path
	}

	return name, "/" + after
}

// IsStorageServiceOp reports whether q selects an account-level storage
// service operation: Get/Set Service Properties, Get Service Stats
// (restype=service) or Get Account Information (restype=account).
func IsStorageServiceOp(q url.Values) bool {
	switch q.Get("restype") {
	case restypeService, restypeAccount:
		return true
	}

	return false
}
