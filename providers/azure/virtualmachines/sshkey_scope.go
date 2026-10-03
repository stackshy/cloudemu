package virtualmachines

import (
	"context"
	"maps"
	"slices"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// SSH public key names are unique per resource group, so the key pair store is
// keyed by subscription, resource group and name. The scope comes from the
// cloudemu:azureSub and cloudemu:azureRG tags the ARM wire records; a key pair
// created without them is filed under the provider's own subscription.

// keyPairKey is the store key of a key pair.
func (m *Mock) keyPairKey(subscription, resourceGroup, name string) string {
	if subscription == "" {
		subscription = m.opts.AccountID
	}

	return strings.ToLower(subscription) + "/" + strings.ToLower(resourceGroup) + "/" + name
}

// findKeyPair resolves a key pair by name alone, for the portable calls that
// carry no scope. An unscoped key pair is tried first; otherwise the first
// match in key order wins, so the choice is stable.
func (m *Mock) findKeyPair(name string) (string, *driver.KeyPairInfo, bool) {
	key := m.keyPairKey("", "", name)
	if kp, ok := m.keyPairs.Get(key); ok {
		return key, kp, true
	}

	matches := m.keyPairs.Filter(func(_ string, kp *driver.KeyPairInfo) bool { return kp.Name == name })
	if len(matches) == 0 {
		return "", nil, false
	}

	keys := slices.Sorted(maps.Keys(matches))

	return keys[0], matches[keys[0]], true
}

// GetKeyPairScoped returns the SSH public key of that name in the given
// subscription and resource group, without its private key.
func (m *Mock) GetKeyPairScoped(_ context.Context, subscription, resourceGroup, name string) (*driver.KeyPairInfo, error) {
	kp, ok := m.keyPairs.Get(m.keyPairKey(subscription, resourceGroup, name))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "sshPublicKey %q not found", name)
	}

	cp := *kp
	cp.PrivateKey = ""

	return &cp, nil
}

// UpdateKeyPairScoped is UpdateKeyPair for the key in the given resource group.
func (m *Mock) UpdateKeyPairScoped(
	_ context.Context, subscription, resourceGroup, name string, publicKey *string, tags map[string]string,
) (*driver.KeyPairInfo, error) {
	return m.updateKeyPairAt(m.keyPairKey(subscription, resourceGroup, name), name, publicKey, tags)
}

// GenerateKeyPairScoped is GenerateKeyPair for the key in the given resource
// group.
func (m *Mock) GenerateKeyPairScoped(_ context.Context, subscription, resourceGroup, name string) (*driver.KeyPairInfo, error) {
	return m.generateKeyPairAt(m.keyPairKey(subscription, resourceGroup, name), name)
}

// DeleteKeyPairScoped deletes the key of that name in the given resource group,
// leaving same-named keys in other resource groups alone.
func (m *Mock) DeleteKeyPairScoped(_ context.Context, subscription, resourceGroup, name string) error {
	if !m.keyPairs.Delete(m.keyPairKey(subscription, resourceGroup, name)) {
		return cerrors.Newf(cerrors.NotFound, "sshPublicKey %q not found", name)
	}

	return nil
}

// rekeyKeyPairs moves restored key pairs to their scoped keys, migrating
// snapshots taken when key pairs were keyed by name alone.
func (m *Mock) rekeyKeyPairs() {
	for key, kp := range m.keyPairs.All() {
		want := m.keyPairKey(kp.Tags[subTag], kp.Tags[rgTag], kp.Name)
		if want != key {
			m.keyPairs.Delete(key)
			m.keyPairs.Set(want, kp)
		}
	}
}
