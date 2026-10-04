package idgen_test

import (
	"regexp"
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

var (
	secretKeyShape    = regexp.MustCompile(`^[A-Za-z0-9+/]{40}$`)
	sessionTokenShape = regexp.MustCompile(`^[A-Za-z0-9+/]{300,}$`)
	ociAuthTokenShape = regexp.MustCompile(`^[A-Za-z0-9+/:;<>()#_.-]{20}$`)
)

// TestCredentialSecretsAreRandomAndShaped guards the generators that mint
// signing secrets. They must match the real cloud's shape and never be
// derivable from anything an attacker can observe, such as a shared counter.
func TestCredentialSecretsAreRandomAndShaped(t *testing.T) {
	cases := []struct {
		name  string
		gen   func() (string, error)
		shape *regexp.Regexp
	}{
		{"SecretAccessKey", idgen.SecretAccessKey, secretKeyShape},
		{"SessionToken", idgen.SessionToken, sessionTokenShape},
		{"OCIAuthToken", idgen.OCIAuthToken, ociAuthTokenShape},
	}

	const draws = 200

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(map[string]bool, draws)

			for range draws {
				got, err := tc.gen()
				if err != nil {
					t.Fatalf("%s: %v", tc.name, err)
				}

				if !tc.shape.MatchString(got) {
					t.Fatalf("%s = %q, want shape %s", tc.name, got, tc.shape)
				}

				if seen[got] {
					t.Fatalf("%s repeated value %q", tc.name, got)
				}

				seen[got] = true
			}
		})
	}
}
