package cognito

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// Password handling. Only a salted PBKDF2-SHA256 hash is kept; the plaintext
// is never stored or logged.
const (
	saltLen              = 16
	generatedPasswordLen = 16
	maxPasswordLen       = 256
)

// Stored hashes look like "pbkdf2-sha256$<iterations>$<hex key>". The count is
// stored with each hash, so changing pbkdf2Iterations keeps old hashes valid.
const (
	pbkdf2Prefix = "pbkdf2-sha256$"
	// pbkdf2Iterations is deliberately low for an emulator: fast tests, no real secrets.
	pbkdf2Iterations = 10000
	pbkdf2KeyLen     = 32
	// maxPBKDF2Iterations caps the iteration count a stored hash may claim, so
	// a corrupt snapshot cannot make one sign-in burn CPU.
	maxPBKDF2Iterations = 1_000_000
)

// policySymbols is the set of special characters Cognito counts toward the
// "require symbols" rule.
const policySymbols = "^$*.[]{}()?\"!@#%&/\\,><':;|_~`=+- "

// Character classes used to build a generated temporary password.
const (
	upperChars  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lowerChars  = "abcdefghijklmnopqrstuvwxyz"
	digitChars  = "0123456789"
	symbolChars = "!@#%&*?"
)

// checkPassword validates a password against a pool's policy and returns the
// InvalidPasswordException real Cognito sends for the first rule it breaks.
func checkPassword(pw string, pp driver.PasswordPolicy) error {
	if len(pw) < int(pp.MinimumLength) {
		return invalidPassword("Password not long enough")
	}

	if len(pw) > maxPasswordLen {
		return invalidPassword("Password must have length less than or equal to 256")
	}

	rules := []struct {
		required bool
		chars    string
		reason   string
	}{
		{pp.RequireUppercase, upperChars, "Password must have uppercase characters"},
		{pp.RequireLowercase, lowerChars, "Password must have lowercase characters"},
		{pp.RequireNumbers, digitChars, "Password must have numeric characters"},
		{pp.RequireSymbols, policySymbols, "Password must have symbol characters"},
	}

	for _, r := range rules {
		if r.required && !strings.ContainsAny(pw, r.chars) {
			return invalidPassword(r.reason)
		}
	}

	return nil
}

// generatePassword returns a temporary password that satisfies any policy with
// a minimum length up to its length: it always carries every character class.
func generatePassword(pp driver.PasswordPolicy) string {
	n := max(generatedPasswordLen, int(pp.MinimumLength))

	var b strings.Builder

	b.WriteString(randString(1, upperChars))
	b.WriteString(randString(1, lowerChars))
	b.WriteString(randString(1, digitChars))
	b.WriteString(randString(1, symbolChars))
	b.WriteString(randString(n-b.Len(), alnumMixed))

	return b.String()
}

// hashPassword returns a fresh random salt and the PBKDF2-SHA256 hash of the
// password under it, both encoded for storage.
//
//nolint:gocritic // unnamedResult: (salt, hash) reads clearly at the call sites
func hashPassword(pw string) (string, string) {
	raw := make([]byte, saltLen)
	_, _ = rand.Read(raw)

	salt := hex.EncodeToString(raw)

	return salt, pbkdf2Hash(salt, pw, pbkdf2Iterations)
}

func pbkdf2Hash(salt, pw string, iter int) string {
	key, err := pbkdf2.Key(sha256.New, pw, []byte(salt), iter, pbkdf2KeyLen)
	if err != nil {
		return ""
	}

	return pbkdf2Prefix + strconv.Itoa(iter) + "$" + hex.EncodeToString(key)
}

// verifyPassword reports whether pw matches a stored salt and PBKDF2 hash. A
// hash in any other format never matches.
func verifyPassword(salt, stored, pw string) bool {
	rest, ok := strings.CutPrefix(stored, pbkdf2Prefix)
	if !ok {
		return false
	}

	iterText, _, ok := strings.Cut(rest, "$")
	if !ok {
		return false
	}

	iter, err := strconv.Atoi(iterText)
	if err != nil || iter <= 0 || iter > maxPBKDF2Iterations {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(pbkdf2Hash(salt, pw, iter)), []byte(stored)) == 1
}
