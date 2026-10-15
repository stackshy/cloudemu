//go:build unix

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/server/serverkit"
)

// startEnforceAuthServer runs an in-process --enforce-auth server wired like
// `cloudemu start` does it: endpoints and admin token land in dir.
func startEnforceAuthServer(t *testing.T, dir string) {
	t.Helper()

	app, err := serverkit.New(&serverkit.Config{
		Providers:      []string{"aws"},
		Host:           "127.0.0.1",
		Ports:          map[string]string{"aws": freePort(t)},
		Admin:          true,
		EnforceAuth:    true,
		AdminTokenFile: adminTokenPath(dir),
		EndpointsFile:  endpointsPath(dir),
		Quiet:          true,
		Out:            io.Discard,
	})
	if err != nil {
		t.Fatalf("serverkit.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	if _, err := waitForEndpoints(endpointsPath(dir), 10*time.Second); err != nil {
		t.Fatalf("server not ready: %v", err)
	}
}

// TestCLIAdminCommandsUnderEnforceAuth drives snapshot save/load and cost
// against an --enforce-auth server: they work with the run-dir token or
// CLOUDEMU_ADMIN_TOKEN, and fail with a clear error without one.
func TestCLIAdminCommandsUnderEnforceAuth(t *testing.T) {
	t.Setenv(adminTokenEnv, "")

	dir := t.TempDir()
	startEnforceAuthServer(t, dir)

	if err := snapshotSave(dir, "s1", false); err != nil {
		t.Fatalf("snapshot save with the run-dir token: %v", err)
	}

	if err := snapshotLoad(dir, "s1"); err != nil {
		t.Fatalf("snapshot load with the run-dir token: %v", err)
	}

	if err := runCost([]string{"--home", dir, "--json"}); err != nil {
		t.Fatalf("cost with the run-dir token: %v", err)
	}

	token := adminToken(dir)
	if token == "" {
		t.Fatal("serve did not write the admin token to the run dir")
	}

	if err := os.Remove(adminTokenPath(dir)); err != nil {
		t.Fatal(err)
	}

	if err := snapshotSave(dir, "s2", false); !errors.Is(err, errAdminUnauthorized) {
		t.Fatalf("snapshot save without a token = %v, want errAdminUnauthorized", err)
	}

	if err := runCost([]string{"--home", dir}); !errors.Is(err, errAdminUnauthorized) {
		t.Fatalf("cost without a token = %v, want errAdminUnauthorized", err)
	}

	t.Setenv(adminTokenEnv, "wrong")

	if err := snapshotLoad(dir, "s1"); !errors.Is(err, errAdminUnauthorized) {
		t.Fatalf("snapshot load with a wrong token = %v, want errAdminUnauthorized", err)
	}

	t.Setenv(adminTokenEnv, token)

	if err := snapshotSave(dir, "s2", false); err != nil {
		t.Fatalf("snapshot save with CLOUDEMU_ADMIN_TOKEN: %v", err)
	}
}
