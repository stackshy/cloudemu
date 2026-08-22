package container_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/container"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
)

// runResult carries the outcome of an asynchronous engine Run.
type runResult struct {
	handle string
	err    error
}

// TestRunToCompletionWithSidecarDoesNotHang proves a run-to-completion workload
// whose main container exits while a sidecar keeps running returns as soon as the
// main container exits — instead of blocking on the never-exiting sidecar forever
// — and that both containers stay observable afterwards (main exited, sidecar
// still running).
func TestRunToCompletionWithSidecarDoesNotHang(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := container.New()
	t.Cleanup(func() { _ = eng.Close() })

	spec := config.ContainerRunSpec{
		Name:            "sidecar-test",
		RunToCompletion: true,
		Containers: []config.ContainerSpec{
			{Name: "main", Image: "alpine:3.20", Command: []string{"/bin/sh", "-c", "echo done"}},
			{Name: "sidecar", Image: "alpine:3.20", Command: []string{"/bin/sh", "-c", "sleep 300"}},
		},
	}

	done := make(chan runResult, 1)

	go func() {
		h, err := eng.Run(context.Background(), spec)
		done <- runResult{handle: h, err: err}
	}()

	var res runResult

	select {
	case res = <-done:
	case <-time.After(45 * time.Second):
		t.Fatal("Run did not return — a non-exiting sidecar blocked run-to-completion")
	}

	if res.err != nil {
		t.Fatalf("Run: %v", res.err)
	}

	t.Cleanup(func() { _ = eng.Stop(context.Background(), res.handle) })

	statuses, err := eng.Status(context.Background(), res.handle)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	byName := map[string]config.ContainerStatus{}
	for _, s := range statuses {
		byName[s.Name] = s
	}

	if byName["main"].State != "exited" {
		t.Fatalf("main state = %q, want exited", byName["main"].State)
	}

	if byName["sidecar"].State != "running" {
		t.Fatalf("sidecar state = %q, want running", byName["sidecar"].State)
	}
}
