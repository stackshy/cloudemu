package blobstorage

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// storageKeyBytes is the length of a raw Azure storage account key. Real keys
// are 64-byte secrets rendered as base64.
const storageKeyBytes = 64

// Compile-time check that Mock satisfies the optional StorageAccountKeys
// capability the ARM storage-account handler reaches by type assertion.
var _ driver.StorageAccountKeys = (*Mock)(nil)

// ListStorageAccountKeys returns the account's access keys, generating a stable
// key1/key2 pair on first access so a data-plane SharedKeyCredential can be
// built and later matched against a regenerate.
func (m *Mock) ListStorageAccountKeys(_ context.Context, account string) ([]driver.AccountKey, error) {
	if keys, ok := m.accountKeys.Get(account); ok {
		return cloneKeys(keys), nil
	}

	keys := []driver.AccountKey{
		m.newAccountKey("key1"),
		m.newAccountKey("key2"),
	}
	m.accountKeys.Set(account, keys)

	return cloneKeys(keys), nil
}

// RegenerateStorageAccountKey rotates the value of the named key and returns the
// full, updated key list.
func (m *Mock) RegenerateStorageAccountKey(ctx context.Context, account, keyName string) ([]driver.AccountKey, error) {
	if keyName != "key1" && keyName != "key2" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "invalid key name %q, want key1 or key2", keyName)
	}

	// Ensure a base pair exists before rotating one of them.
	if _, err := m.ListStorageAccountKeys(ctx, account); err != nil {
		return nil, err
	}

	keys, _ := m.accountKeys.Get(account)
	rotated := cloneKeys(keys)

	for i := range rotated {
		if rotated[i].KeyName == keyName {
			rotated[i] = m.newAccountKey(keyName)
		}
	}

	m.accountKeys.Set(account, rotated)

	return cloneKeys(rotated), nil
}

// newAccountKey mints a fresh access key with a random base64 value.
func (m *Mock) newAccountKey(name string) driver.AccountKey {
	raw := make([]byte, storageKeyBytes)
	_, _ = rand.Read(raw)

	return driver.AccountKey{
		KeyName:      name,
		Value:        base64.StdEncoding.EncodeToString(raw),
		Permissions:  "Full",
		CreationTime: m.opts.Clock.Now().UTC().Format(blobTimeFormat),
	}
}

func cloneKeys(keys []driver.AccountKey) []driver.AccountKey {
	out := make([]driver.AccountKey, len(keys))
	copy(out, keys)

	return out
}
