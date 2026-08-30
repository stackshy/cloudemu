package main

import (
	"os/exec"
	"strings"
	"testing"
)

// engineModulePaths are the heavy real-engine dependencies that must never reach
// the lean cloudemu binary. They live only in the :engines image (contrib/server);
// the shared server/serveflags package the lean binary now builds from is
// deliberately dep-light so pulling it in cannot drag these along. This is the
// structural guard against the "compile engines into the lean binary" trap.
//
//nolint:gochecknoglobals // test fixture: the forbidden engine module paths
var engineModulePaths = []string{
	"github.com/fergusstrange/embedded-postgres",
	"github.com/alicebob/miniredis",
	"github.com/redis/go-redis",
	"github.com/docker/docker",
}

// TestLeanBinaryHasNoEngineDeps shells `go list -deps .` for the lean binary and
// fails if any forbidden engine module appears in its transitive dependency
// graph. It proves both the lean binary and server/serveflags stay engine-free.
func TestLeanBinaryHasNoEngineDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, out)
	}

	deps := string(out)

	for _, mod := range engineModulePaths {
		if strings.Contains(deps, mod) {
			t.Errorf("lean binary transitively depends on engine module %q — it must stay engine-free", mod)
		}
	}
}
