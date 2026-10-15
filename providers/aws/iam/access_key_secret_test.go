package iam

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

var (
	accessKeyIDShape     = regexp.MustCompile(`^AKIA[A-Z2-7]{16}$`)
	secretAccessKeyShape = regexp.MustCompile(`^[A-Za-z0-9+/]{40}$`)
)

// counterGuesses returns the secrets an attacker would try if secrets were
// built from the shared id counter: every "secret-%08x" up to the counter's
// current value, going back window steps.
func counterGuesses(window uint64) map[string]bool {
	probe, err := strconv.ParseUint(idgen.GenerateID(""), 16, 64)
	if err != nil {
		panic(err)
	}

	out := make(map[string]bool, window)
	for n := probe - min(window, probe); n <= probe; n++ {
		out[fmt.Sprintf("secret-%08x", n)] = true
	}

	return out
}

func TestCreateAccessKeySecretIsRandom(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateUser(ctx, driver.UserConfig{Name: "erin"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	first, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	second, err := m.CreateAccessKey(ctx, driver.AccessKeyConfig{UserName: "erin"})
	if err != nil {
		t.Fatalf("CreateAccessKey: %v", err)
	}

	guesses := counterGuesses(64)

	for _, ak := range []*driver.AccessKeyInfo{first, second} {
		if !accessKeyIDShape.MatchString(ak.AccessKeyID) {
			t.Fatalf("access key id %q, want AKIA plus 16 base32 chars", ak.AccessKeyID)
		}

		if !secretAccessKeyShape.MatchString(ak.SecretAccessKey) {
			t.Fatalf("secret %q, want 40 base64-alphabet chars", ak.SecretAccessKey)
		}

		if guesses[ak.SecretAccessKey] {
			t.Fatalf("secret %q is derivable from the id counter", ak.SecretAccessKey)
		}
	}

	if first.SecretAccessKey == second.SecretAccessKey {
		t.Fatal("two access keys share a secret")
	}

	if first.AccessKeyID == second.AccessKeyID {
		t.Fatal("two access keys share an id")
	}
}
