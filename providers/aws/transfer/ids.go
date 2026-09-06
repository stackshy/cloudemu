package transfer

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// hexLen is the number of hex characters in a Transfer id suffix: a ServerID is
// "s-<17hex>" (length 19) and an SSHPublicKeyID is "key-<17hex>" (length 21).
const hexLen = 17

// hexAlphabet is the lowercase-hex alphabet used for id suffixes.
const hexAlphabet = "0123456789abcdef"

// randHex returns a random string of n lowercase-hex characters using
// crypto/rand. A random-source failure falls back to '0' so it never panics.
func randHex(n int) string {
	out := make([]byte, n)

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		for i := range out {
			out[i] = hexAlphabet[0]
		}

		return string(out)
	}

	for i, v := range b {
		out[i] = hexAlphabet[int(v)%len(hexAlphabet)]
	}

	return string(out)
}

// newServerID returns a fresh server id of the form "s-<17hex>".
func newServerID() string { return "s-" + randHex(hexLen) }

// newSSHKeyID returns a fresh SSH-public-key id of the form "key-<17hex>".
func newSSHKeyID() string { return "key-" + randHex(hexLen) }

// hostKeyFingerprint returns a deterministic SHA256:<base64> fingerprint for a
// server, matching the shape Transfer reports (and the Terraform
// aws_transfer_server host_key_fingerprint attribute reads back). It is derived
// from the server id so a given server always reports a stable value.
func hostKeyFingerprint(serverID string) string {
	sum := sha256.Sum256([]byte(serverID))

	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}
