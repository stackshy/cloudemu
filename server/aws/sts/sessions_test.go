package sts_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/aws/sts"
)

// TestMintYieldsDistinctHighEntropyCredentials proves Mint returns unique,
// well-formed temporary credentials on the crypto/rand success path. This is the guard
// against the removed predictable fallback, which would have produced identical,
// forgeable credentials on every call.
func TestMintYieldsDistinctHighEntropyCredentials(t *testing.T) {
	store := sts.NewSessionStore(config.NewFakeClock(time.Unix(0, 0)))

	a, err := store.Mint(time.Hour, sts.SessionOwner{})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	b, err := store.Mint(time.Hour, sts.SessionOwner{})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if !strings.HasPrefix(a.AccessKeyID, "ASIA") {
		t.Fatalf("access key id %q missing ASIA prefix", a.AccessKeyID)
	}

	if a.AccessKeyID == b.AccessKeyID || a.SecretAccessKey == b.SecretAccessKey || a.SessionToken == b.SessionToken {
		t.Fatal("two Mint calls returned identical credentials; entropy source is broken")
	}

	// A predictable fallback would repeat a single alphabet character; a genuine
	// draw has more than one distinct character.
	if distinctChars(a.SecretAccessKey) < 2 {
		t.Fatalf("secret %q has too little entropy (predictable fallback?)", a.SecretAccessKey)
	}
}

func distinctChars(s string) int {
	seen := map[rune]struct{}{}
	for _, r := range s {
		seen[r] = struct{}{}
	}

	return len(seen)
}

// TestMintMatchesRealSTSShape checks the minted credential has the shape real
// STS returns: ASIA plus 16 base32 characters, a 40-character base64-alphabet
// secret and a long base64 session token.
func TestMintMatchesRealSTSShape(t *testing.T) {
	store := sts.NewSessionStore(config.NewFakeClock(time.Unix(0, 0)))

	sess, err := store.Mint(time.Hour, sts.SessionOwner{})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	checks := []struct {
		name, got string
		re        *regexp.Regexp
	}{
		{"AccessKeyID", sess.AccessKeyID, regexp.MustCompile(`^ASIA[A-Z2-7]{16}$`)},
		{"SecretAccessKey", sess.SecretAccessKey, regexp.MustCompile(`^[A-Za-z0-9+/]{40}$`)},
		{"SessionToken", sess.SessionToken, regexp.MustCompile(`^[A-Za-z0-9+/]{300,}$`)},
	}

	for _, c := range checks {
		if !c.re.MatchString(c.got) {
			t.Fatalf("%s = %q, want shape %s", c.name, c.got, c.re)
		}
	}
}
