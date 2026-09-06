package transfer

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// maxSSHKeysPerUser is Transfer's cap on SSH public keys per user.
const maxSSHKeysPerUser = 5

// ImportSSHPublicKey appends a key to a user and returns its generated
// SSHPublicKeyID, incrementing the user's key count. The cap check and append
// are serialized on m.mu so a user never exceeds maxSSHKeysPerUser under
// concurrent imports.
func (m *Mock) ImportSSHPublicKey(_ context.Context, serverID, userName, body string) (string, error) {
	if body == "" {
		return "", invalidRequest("SSHPublicKeyBody is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := userKey(serverID, userName)

	u, ok := m.users.Get(key)
	if !ok {
		return "", notFound("user %s does not exist on server %s", userName, serverID)
	}

	if len(u.SSHPublicKeys) >= maxSSHKeysPerUser {
		return "", invalidRequest("user %s already has the maximum of %d ssh public keys", userName, maxSSHKeysPerUser)
	}

	keyID := newSSHKeyID()

	u = copyUser(u)
	u.SSHPublicKeys = append(u.SSHPublicKeys, driver.SSHPublicKey{
		DateImported:     m.opts.Clock.Now().UTC(),
		SSHPublicKeyBody: body,
		SSHPublicKeyID:   keyID,
	})
	m.users.Set(key, u)

	return keyID, nil
}

// DeleteSSHPublicKey removes a key from a user by its SSHPublicKeyID.
func (m *Mock) DeleteSSHPublicKey(_ context.Context, serverID, userName, keyID string) error {
	key := userKey(serverID, userName)

	u, ok := m.users.Get(key)
	if !ok {
		return notFound("user %s does not exist on server %s", userName, serverID)
	}

	u = copyUser(u)

	idx := -1

	for i, k := range u.SSHPublicKeys {
		if k.SSHPublicKeyID == keyID {
			idx = i

			break
		}
	}

	if idx < 0 {
		return notFound("ssh public key %s does not exist for user %s", keyID, userName)
	}

	u.SSHPublicKeys = append(u.SSHPublicKeys[:idx], u.SSHPublicKeys[idx+1:]...)
	m.users.Set(key, u)

	return nil
}
