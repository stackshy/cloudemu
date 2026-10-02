package driver

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// AzureDefaultStorageAccount is the account that owns the default blob
// namespace: containers created without an account (the bare data-plane host
// and every portable Bucket call) belong to it.
const AzureDefaultStorageAccount = "cloudemu"

// StorageAccountRef is an Azure storage account as its own resource, separate
// from the blob containers it holds. Subscription is empty for accounts
// migrated from an older snapshot, which recorded none; an empty subscription
// matches any caller.
type StorageAccountRef struct {
	Name          string
	Subscription  string
	ResourceGroup string
	CreatedAt     string
}

// AccountExistsError reports that a storage account name is already owned.
// It carries the owner's subscription and resource group so the ARM layer can
// pick the real error code. It unwraps to a cerrors AlreadyExists error.
type AccountExistsError struct {
	Name          string
	Subscription  string
	ResourceGroup string
}

func (e *AccountExistsError) Error() string { return e.Unwrap().Error() }

// Unwrap returns the canonical AlreadyExists error.
func (e *AccountExistsError) Unwrap() error {
	return cerrors.Newf(cerrors.AlreadyExists, "storage account %q already exists", e.Name)
}

// ContainerEncryptionScope is the per-container encryption scope setting of
// the ARM blob container resource (defaultEncryptionScope and
// denyEncryptionScopeOverride). An empty DefaultEncryptionScope means the
// account default ($account-encryption-key).
type ContainerEncryptionScope struct {
	DefaultEncryptionScope      string
	DenyEncryptionScopeOverride bool
}

// AzureStorageAccounts is an OPTIONAL Azure-only capability, discovered by type
// assertion. It models storage accounts as a namespace of their own: an
// account is not a container, and each account holds its own containers. The
// shared Bucket interface is unchanged; containers of a named account are
// addressed through AzureContainerKey and never appear in ListBuckets.
type AzureStorageAccounts interface {
	// CreateStorageAccount registers the account. It reports created=false and
	// no error when the account already exists in the same resource group and a
	// compatible subscription. Any other owner yields *AccountExistsError.
	CreateStorageAccount(ctx context.Context, a StorageAccountRef) (created bool, err error)
	GetStorageAccount(ctx context.Context, name string) (StorageAccountRef, error)
	ListStorageAccounts(ctx context.Context) ([]StorageAccountRef, error)
	// DeleteStorageAccount removes the account and all of its data: containers
	// (with their blobs), keys and account-level settings.
	DeleteStorageAccount(ctx context.Context, name string) error
	// PurgeResourceGroup deletes every account recorded under sub/rg.
	PurgeResourceGroup(ctx context.Context, sub, rg string) error
	// ListAccountContainers lists the containers of one account by bare name.
	ListAccountContainers(ctx context.Context, account string) ([]BucketInfo, error)
	// ForceDeleteContainer deletes a container together with its blobs, the
	// way the ARM blob container DELETE does.
	ForceDeleteContainer(ctx context.Context, container string) error
	SetContainerEncryptionScope(ctx context.Context, container string, scope ContainerEncryptionScope) error
	ContainerEncryptionScope(ctx context.Context, container string) (ContainerEncryptionScope, error)
}

// AzureContainerKey returns the store key of container name in account. The
// default account's containers keep their bare name, so every existing caller
// is unchanged; other accounts' containers are qualified "account/name".
// Container names cannot contain "/", so the two forms never collide.
func AzureContainerKey(account, name string) string {
	if account == "" || account == AzureDefaultStorageAccount {
		return name
	}

	return account + "/" + name
}

// SplitAzureContainerKey is the inverse of AzureContainerKey. A bare key
// belongs to the default account, reported as "".
func SplitAzureContainerKey(key string) (account, name string) {
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}

	return "", key
}

const (
	minAzureContainerNameLen = 3
	maxAzureContainerNameLen = 63
)

// ValidAzureContainerName reports whether name follows the Azure container
// naming rule: 3-63 characters of lower-case letters, digits and hyphens,
// starting and ending with a letter or digit, with no consecutive hyphens.
// "$root" and "$web" are the reserved system containers and are also valid.
func ValidAzureContainerName(name string) bool {
	if name == "$root" || name == "$web" {
		return true
	}

	if len(name) < minAzureContainerNameLen || len(name) > maxAzureContainerNameLen {
		return false
	}

	if name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return false
	}

	return strings.IndexFunc(name, notContainerNameRune) < 0
}

// notContainerNameRune reports whether c is outside the container name
// alphabet (lower-case letters, digits and hyphen).
func notContainerNameRune(c rune) bool {
	return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-'
}
