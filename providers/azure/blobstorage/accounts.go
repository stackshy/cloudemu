package blobstorage

import (
	"context"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
	"github.com/stackshy/cloudemu/v2/services/storage/storageengine"
)

// Compile-time check that Mock satisfies the Azure storage-account namespace
// capability the ARM storage-account handler reaches by type assertion.
var _ driver.AzureStorageAccounts = (*Mock)(nil)

// CreateStorageAccount registers a storage account. An account that already
// exists in the same resource group is an update (created=false). A record with
// no subscription (migrated from an older snapshot) matches any caller and is
// stamped with the caller's subscription. Any other owner is reported as
// *driver.AccountExistsError so the caller can choose the real error code.
func (m *Mock) CreateStorageAccount(_ context.Context, a driver.StorageAccountRef) (bool, error) {
	if a.Name == "" {
		return false, cerrors.New(cerrors.InvalidArgument, "storage account name cannot be empty")
	}

	m.accountMu.Lock()
	defer m.accountMu.Unlock()

	cur, ok := m.accounts.Get(a.Name)
	if !ok {
		if a.CreatedAt == "" {
			a.CreatedAt = m.opts.Clock.Now().UTC().Format(blobTimeFormat)
		}

		m.accounts.Set(a.Name, a)

		return true, nil
	}

	if !subscriptionsCompatible(cur.Subscription, a.Subscription) || !strings.EqualFold(cur.ResourceGroup, a.ResourceGroup) {
		return false, &driver.AccountExistsError{
			Name: cur.Name, Subscription: cur.Subscription, ResourceGroup: cur.ResourceGroup,
		}
	}

	if cur.Subscription == "" && a.Subscription != "" {
		cur.Subscription = a.Subscription
		m.accounts.Set(cur.Name, cur)
	}

	return false, nil
}

// subscriptionsCompatible reports whether two subscriptions may refer to the
// same owner: an empty one (a migrated record, or a caller that sent none)
// matches anything.
func subscriptionsCompatible(a, b string) bool {
	return a == "" || b == "" || strings.EqualFold(a, b)
}

// GetStorageAccount returns the named account.
func (m *Mock) GetStorageAccount(_ context.Context, name string) (driver.StorageAccountRef, error) {
	a, ok := m.accounts.Get(name)
	if !ok {
		return driver.StorageAccountRef{}, cerrors.Newf(cerrors.NotFound, "storage account %q not found", name)
	}

	return a, nil
}

// ListStorageAccounts returns every account, sorted by name.
func (m *Mock) ListStorageAccounts(_ context.Context) ([]driver.StorageAccountRef, error) {
	all := m.accounts.All()
	out := make([]driver.StorageAccountRef, 0, len(all))

	for _, a := range all {
		out = append(out, a)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// DeleteStorageAccount removes an account and everything it holds: its
// containers (force-deleted with their blobs), its access keys, blob service
// properties, encryption and attributes. The match on container keys is
// bounded by the trailing slash, so deleting "pg" never touches "pg2/...".
// A default-namespace container that merely shares the account's name is not
// the account's data and survives. The one exception is the default account
// itself, whose containers are the bare keys.
func (m *Mock) DeleteStorageAccount(ctx context.Context, name string) error {
	m.accountMu.Lock()
	defer m.accountMu.Unlock()

	if !m.accounts.Has(name) {
		return cerrors.Newf(cerrors.NotFound, "storage account %q not found", name)
	}

	for _, key := range m.accountContainerKeys(name) {
		m.dropContainer(ctx, key)
	}

	m.accountKeys.Delete(name)
	m.blobServiceProps.Delete(name)
	m.accountEncryption.Delete(name)
	m.bucketAttrs.Delete(name)
	m.accounts.Delete(name)

	return nil
}

// PurgeResourceGroup deletes every account recorded under the resource group,
// backing the resource-group cascade delete. The group matches
// case-insensitively; an account with no recorded subscription (migrated)
// matches any subscription.
func (m *Mock) PurgeResourceGroup(ctx context.Context, sub, rg string) error {
	for _, a := range m.accounts.All() {
		if !strings.EqualFold(a.ResourceGroup, rg) || !subscriptionsCompatible(a.Subscription, sub) {
			continue
		}

		if err := m.DeleteStorageAccount(ctx, a.Name); err != nil && !cerrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// ListAccountContainers lists one account's containers under their bare names.
// The default account lists the default namespace; any other account must
// exist.
func (m *Mock) ListAccountContainers(_ context.Context, account string) ([]driver.BucketInfo, error) {
	if !isDefaultAccount(account) && !m.accounts.Has(account) {
		return nil, cerrors.Newf(cerrors.NotFound, "storage account %q not found", account)
	}

	keys := m.accountContainerKeys(account)
	sort.Strings(keys)

	out := make([]driver.BucketInfo, 0, len(keys))

	for _, k := range keys {
		ctr, ok := m.containers.Get(k)
		if !ok {
			continue
		}

		_, bare := driver.SplitAzureContainerKey(k)
		out = append(out, driver.BucketInfo{Name: bare, Region: ctr.Region, CreatedAt: ctr.CreatedAt})
	}

	return out, nil
}

// ForceDeleteContainer deletes a container together with its blobs, versions,
// snapshots and soft-deleted entries, the way the ARM blob container DELETE
// does (unlike DeleteBucket, which refuses a non-empty container).
func (m *Mock) ForceDeleteContainer(ctx context.Context, container string) error {
	if !m.containers.Has(container) {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	m.dropContainer(ctx, container)

	return nil
}

// SetContainerEncryptionScope stores the container's ARM encryption scope
// settings.
func (m *Mock) SetContainerEncryptionScope(
	_ context.Context, container string, scope driver.ContainerEncryptionScope,
) error {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ctr.mu.Lock()
	ctr.encryptionScope = scope
	ctr.mu.Unlock()

	return nil
}

// ContainerEncryptionScope returns the container's ARM encryption scope
// settings.
func (m *Mock) ContainerEncryptionScope(_ context.Context, container string) (driver.ContainerEncryptionScope, error) {
	ctr, ok := m.containers.Get(container)
	if !ok {
		return driver.ContainerEncryptionScope{}, cerrors.Newf(cerrors.NotFound, "container %q not found", container)
	}

	ctr.mu.Lock()
	defer ctr.mu.Unlock()

	return ctr.encryptionScope, nil
}

// accountContainerKeys returns the store keys of every container owned by the
// account: the bare keys for the default account, otherwise the keys under the
// "account/" prefix.
func (m *Mock) accountContainerKeys(account string) []string {
	var out []string

	if isDefaultAccount(account) {
		for _, k := range m.containers.Keys() {
			if !strings.Contains(k, "/") {
				out = append(out, k)
			}
		}

		return out
	}

	prefix := account + "/"

	for _, k := range m.containers.Keys() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}

	return out
}

// dropContainer removes a container and purges the bytes of its live blobs
// from a wired storage engine (best effort, like DeleteObject).
func (m *Mock) dropContainer(ctx context.Context, key string) {
	ctr, ok := m.containers.Get(key)
	if !ok {
		return
	}

	m.containers.Delete(key)

	if m.opts.StorageEngine == nil {
		return
	}

	for blobKey := range ctr.objects.All() {
		_ = storageengine.Delete(ctx, m.opts.StorageEngine, config.StorageRef{Bucket: key, Key: blobKey})
	}
}

// checkQualifiedContainer validates a qualified "account/name" container key
// passed to CreateBucket: the account must exist and the name must be a valid
// container name, so a portable caller cannot fabricate a key.
func (m *Mock) checkQualifiedContainer(key string) error {
	account, name := driver.SplitAzureContainerKey(key)

	if !m.accounts.Has(account) {
		return cerrors.Newf(cerrors.NotFound, "storage account %q not found", account)
	}

	if !driver.ValidAzureContainerName(name) {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid container name %q", name)
	}

	return nil
}

func isDefaultAccount(account string) bool {
	return account == "" || account == AccountName
}
