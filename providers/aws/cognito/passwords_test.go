package cognito

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

const maxTestIterations = 1_000_000

// verifyPassword reports whether pw matches a stored salt and hash, in either
// the PBKDF2 format or the legacy bare SHA-256 format older snapshots carry.
func verifyPassword(salt, stored, pw string) bool {
	if stored == "" {
		return false
	}

	rest, ok := strings.CutPrefix(stored, pbkdf2Prefix)
	if !ok {
		sum := sha256.Sum256([]byte(salt + pw))

		return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(stored)) == 1
	}

	iterText, _, ok := strings.Cut(rest, "$")
	if !ok {
		return false
	}

	iter, err := strconv.Atoi(iterText)
	if err != nil || iter <= 0 || iter > maxTestIterations {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(pbkdf2Hash(salt, pw, iter)), []byte(stored)) == 1
}

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
		{"legacy sha256 match", legacySalt, legacyHash, pw, true},
		{"legacy sha256 wrong password", legacySalt, legacyHash, "Wr0ng!horse", false},
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
