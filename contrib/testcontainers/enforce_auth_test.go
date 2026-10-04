package cloudemu

import (
	"regexp"
	"slices"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

var hexToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestWithEnforceAuthGeneratesToken checks that an empty token is replaced by
// one generated on the client, which both the container env and Reset/Seed see.
func TestWithEnforceAuthGeneratesToken(t *testing.T) {
	opt := WithEnforceAuth("")

	var req testcontainers.GenericContainerRequest
	if err := opt.Customize(&req); err != nil {
		t.Fatalf("Customize: %v", err)
	}

	tok := req.Env[adminTokenEnv]
	if !hexToken.MatchString(tok) {
		t.Fatalf("container env token = %q, want 64 hex characters", tok)
	}

	// Run applies the options once more to learn the token; it must match.
	if got := adminTokenFrom([]testcontainers.ContainerCustomizer{opt}); got != tok {
		t.Fatalf("client token %q != container token %q", got, tok)
	}

	if other := adminTokenFrom([]testcontainers.ContainerCustomizer{WithEnforceAuth("")}); other == tok {
		t.Fatal("two WithEnforceAuth(\"\") calls generated the same token")
	}

	if got := adminTokenFrom([]testcontainers.ContainerCustomizer{WithEnforceAuth("fixed")}); got != "fixed" {
		t.Fatalf("explicit token = %q, want fixed", got)
	}
}

// TestWithEnforceAuthAppendsCmd checks the flag is added to an existing command
// rather than replacing it, and that the image default is used when none is set.
func TestWithEnforceAuthAppendsCmd(t *testing.T) {
	req := testcontainers.GenericContainerRequest{}
	req.Cmd = []string{"serve", "--host", "0.0.0.0", "--log-requests"}

	opt := WithEnforceAuth("t")
	if err := opt.Customize(&req); err != nil {
		t.Fatal(err)
	}
	if err := opt.Customize(&req); err != nil {
		t.Fatal(err)
	}

	want := []string{"serve", "--host", "0.0.0.0", "--log-requests", "--enforce-auth"}
	if !slices.Equal(req.Cmd, want) {
		t.Fatalf("Cmd = %v, want %v", req.Cmd, want)
	}

	var empty testcontainers.GenericContainerRequest
	if err := WithEnforceAuth("t").Customize(&empty); err != nil {
		t.Fatal(err)
	}

	if want := append(defaultCmd(), "--enforce-auth"); !slices.Equal(empty.Cmd, want) {
		t.Fatalf("Cmd with no prior command = %v, want %v", empty.Cmd, want)
	}
}
