package sts_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/server/aws/sts"
)

// TestMintYieldsDistinctHighEntropyCredentials proves Mint returns unique,
// well-formed temporary credentials on the crypto/rand success path — the guard
// against the removed predictable fallback, which would have produced identical,
// forgeable credentials on every call.
func TestMintYieldsDistinctHighEntropyCredentials(t *testing.T) {
	store := sts.NewSessionStore(config.NewFakeClock(time.Unix(0, 0)))

	a, err := store.Mint(time.Hour)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	b, err := store.Mint(time.Hour)
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
