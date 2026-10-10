package cognito

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func TestVerifyPassword(t *testing.T) {
	const pw = "Corr3ct!horse"

	salt, hash := hashPassword(pw)

	legacySalt := "00112233445566778899aabbccddeeff"
	legacySum := sha256.Sum256([]byte(legacySalt + pw))
	legacyHash := hex.EncodeToString(legacySum[:])

	tests := []struct {
		name   string
		salt   string
		stored string
		pw     string
		want   bool
	}{
		{"pbkdf2 match", salt, hash, pw, true},
		{"pbkdf2 wrong password", salt, hash, "Wr0ng!horse", false},
		{"pbkdf2 wrong salt", legacySalt, hash, pw, false},
		{"unprefixed sha256 hash rejected", legacySalt, legacyHash, pw, false},
		{"empty stored hash", salt, "", pw, false},
		{"malformed pbkdf2 hash", salt, pbkdf2Prefix + "abc", pw, false},
		{"non-numeric iterations", salt, pbkdf2Prefix + "x$00", pw, false},
		{"iterations above cap", salt, pbkdf2Prefix + "2000000$00", pw, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := verifyPassword(tc.salt, tc.stored, tc.pw); got != tc.want {
				t.Fatalf("verifyPassword = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHashPasswordFormat(t *testing.T) {
	salt, hash := hashPassword("Corr3ct!horse")

	if len(salt) != 2*saltLen {
		t.Fatalf("salt length = %d, want %d", len(salt), 2*saltLen)
	}

	want := pbkdf2Prefix + "10000$"
	if !strings.HasPrefix(hash, want) || len(hash) != len(want)+2*pbkdf2KeyLen {
		t.Fatalf("hash %q does not have the %q prefix and a %d-byte key", hash, want, pbkdf2KeyLen)
	}

	_, again := hashPassword("Corr3ct!horse")
	if again == hash {
		t.Fatal("two hashes of the same password share a salt")
	}
}

// TestSignInRejectsUnprefixedHash checks that a user record holding a bare
// SHA-256 hash (written only by unreleased development builds) cannot sign in,
// and that AdminSetUserPassword recovers the account.
func TestSignInRejectsUnprefixedHash(t *testing.T) {
	m, _ := newClockMock(t)
	ctx := context.Background()
	pool := mustCreateEmailPool(t, m)
	client := mustCreateClient(t, m, pool.ID, false, passwordFlows...)
	confirmedUser(t, m, pool.ID, client.ClientID, "alice")

	key := userKey(pool.ID, "alice")
	rec, _ := m.users.Get(key)
	sum := sha256.Sum256([]byte(rec.PasswordSalt + testPassword))
	rec.PasswordHash = hex.EncodeToString(sum[:])
	m.users.Set(key, rec)

	_, err := m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	assertException(t, err, driver.ExNotAuthorized, "Incorrect username or password.")

	requireNoError(t, m.AdminSetUserPassword(ctx, pool.ID, "alice", testPassword, true), "AdminSetUserPassword")

	_, err = m.InitiateAuth(ctx, passwordAuth(client.ClientID, "alice", testPassword))
	requireNoError(t, err, "InitiateAuth after reset")
}
