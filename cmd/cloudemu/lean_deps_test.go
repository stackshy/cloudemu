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

// servicePBModules are the gapic service modules whose descriptors feed the
// generated GCP enum tables. Only internal/gcpenumgen and tests may import
// them. Twin of the list in server/wire/gcpenum/deps_test.go; keep the two in
// step.
//
//nolint:gochecknoglobals // test fixture: the forbidden service pb modules
var servicePBModules = []string{
	"cloud.google.com/go/functions",
	"cloud.google.com/go/orchestration",
	"cloud.google.com/go/redis",
	"cloud.google.com/go/dataplex",
	"cloud.google.com/go/artifactregistry",
	"cloud.google.com/go/backupdr",
}

// linkedGapicPB are the gapic pb packages the binary links on purpose:
// server/grpc/bigtableadmin serves the Bigtable admin API over gRPC.
//
//nolint:gochecknoglobals // test fixture: the deliberately linked pb packages
var linkedGapicPB = map[string]bool{
	"cloud.google.com/go/bigtable/admin/apiv2/adminpb": true,
	"cloud.google.com/go/iam/apiv1/iampb":              true,
}

// TestLeanBinaryHasNoServicePB fails if the lean binary links a service pb
// package beyond the deliberate gRPC ones, so the generated enum tables never
// pull a proto descriptor into the binary.
func TestLeanBinaryHasNoServicePB(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, out)
	}

	for _, dep := range strings.Fields(string(out)) {
		if linkedGapicPB[dep] {
			continue
		}

		forbidden := strings.HasPrefix(dep, "cloud.google.com/go/") && strings.Contains(dep, "/apiv")

		for _, mod := range servicePBModules {
			if dep == mod || strings.HasPrefix(dep, mod+"/") {
				forbidden = true
			}
		}

		if forbidden {
			t.Errorf("lean binary links service package %q; only internal/gcpenumgen and tests may", dep)
		}
	}
}
