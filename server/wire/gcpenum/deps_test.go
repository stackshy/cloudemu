package gcpenum_test

import (
	"os/exec"
	"strings"
	"testing"
)

// servicePBModules are the gapic service modules whose descriptors feed the
// generated enum tables. Only internal/gcpenumgen and tests may import them.
// Twin of the list in cmd/cloudemu/lean_deps_test.go; keep the two in step.
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

// enumPackages are gcpenum and the handlers that use its generated tables,
// plus gcs, which must stay untouched by them.
//
//nolint:gochecknoglobals // test fixture: the guarded packages
var enumPackages = []string{
	"github.com/stackshy/cloudemu/v2/server/wire/gcpenum",
	"github.com/stackshy/cloudemu/v2/server/gcp/cloudfunctions",
	"github.com/stackshy/cloudemu/v2/server/gcp/composer",
	"github.com/stackshy/cloudemu/v2/server/gcp/artifactregistry",
	"github.com/stackshy/cloudemu/v2/server/gcp/memorystore",
	"github.com/stackshy/cloudemu/v2/server/gcp/backupdr",
	"github.com/stackshy/cloudemu/v2/server/gcp/dataplex",
	"github.com/stackshy/cloudemu/v2/server/gcp/gcs",
}

func goListDeps(t *testing.T, pkgs ...string) []string {
	t.Helper()

	out, err := exec.Command("go", append([]string{"list", "-deps"}, pkgs...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}

	return strings.Fields(string(out))
}

func isServicePB(dep string) bool {
	for _, mod := range servicePBModules {
		if dep == mod || strings.HasPrefix(dep, mod+"/") {
			return true
		}
	}

	return strings.HasPrefix(dep, "cloud.google.com/go/") && strings.Contains(dep, "/apiv")
}

// TestEnumHandlersHaveNoServicePB proves the generated tables are plain Go:
// no handler using them links a gapic service or pb package.
func TestEnumHandlersHaveNoServicePB(t *testing.T) {
	for _, dep := range goListDeps(t, enumPackages...) {
		if isServicePB(dep) {
			t.Errorf("enum handlers depend on service package %q; only internal/gcpenumgen and tests may", dep)
		}
	}
}

// TestGCPEnumHasNoProtobuf keeps gcpenum itself free of the protobuf runtime.
func TestGCPEnumHasNoProtobuf(t *testing.T) {
	for _, dep := range goListDeps(t, enumPackages[0]) {
		if strings.HasPrefix(dep, "google.golang.org/protobuf") {
			t.Errorf("gcpenum depends on %q; it must stay protobuf-free", dep)
		}
	}
}
