package dockerengine_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine"
	azureserver "github.com/stackshy/cloudemu/v2/server/azure"
)

// TestACIContainerGroupE2E runs the exact flow a real user runs against Azure
// Container Instances: create a container group with the real ACI SDK (one
// alpine container, restartPolicy Never → run-to-completion), poll the group's
// container instanceView until it reaches Terminated, assert exitCode 0, then
// read the container's logs and assert they carry the marker — all against
// CloudEmu backed by a real Docker container (no cloud account). It proves the
// real container ran to completion (its exit code reaches the wire) and its
// real stdout was captured and surfaced through Containers.ListLogs.
func TestACIContainerGroupE2E(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := dockerengine.NewContainers()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewAzure(config.WithContainerEngine(eng))
	ts := httptest.NewTLSServer(azureserver.New(azureserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	const (
		subID     = "00000000-0000-0000-0000-000000000000"
		rg        = "rg-e2e"
		group     = "cg-e2e"
		container = "app"
		marker    = "cloudemu-aci-marker-7"
	)

	groupsClient, err := armcontainerinstance.NewContainerGroupsClient(subID, azureFakeCred{}, armOpts(ts))
	if err != nil {
		t.Fatalf("groups client: %v", err)
	}

	containersClient, err := armcontainerinstance.NewContainersClient(subID, azureFakeCred{}, armOpts(ts))
	if err != nil {
		t.Fatalf("containers client: %v", err)
	}

	ctx := context.Background()

	// 1. Create the container group — like `az container create`. restartPolicy
	//    Never makes it a run-to-completion workload.
	createPoller, err := groupsClient.BeginCreateOrUpdate(ctx, rg, group, armcontainerinstance.ContainerGroup{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerinstance.ContainerGroupProperties{
			OSType:        to.Ptr(armcontainerinstance.OperatingSystemTypesLinux),
			RestartPolicy: to.Ptr(armcontainerinstance.ContainerGroupRestartPolicyNever),
			Containers: []*armcontainerinstance.Container{{
				Name: to.Ptr(container),
				Properties: &armcontainerinstance.ContainerProperties{
					Image:   to.Ptr("alpine:3.20"),
					Command: []*string{to.Ptr("/bin/sh"), to.Ptr("-c"), to.Ptr("echo " + marker)},
					Resources: &armcontainerinstance.ResourceRequirements{
						Requests: &armcontainerinstance.ResourceRequests{
							CPU:        to.Ptr(1.0),
							MemoryInGB: to.Ptr(1.0),
						},
					},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	// 2. Poll GET until the container's instanceView reports Terminated, then
	//    assert it exited 0 — proving the real container ran to completion and its
	//    real exit code reached the wire.
	var exitCode int32 = -1

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		got, gerr := groupsClient.Get(ctx, rg, group, nil)
		if gerr != nil {
			t.Fatalf("Get: %v", gerr)
		}

		state := containerState(t, got.ContainerGroup)
		if state != nil && state.State != nil && *state.State == "Terminated" {
			if state.ExitCode == nil {
				t.Fatalf("terminated container has no exit code: %+v", state)
			}

			exitCode = *state.ExitCode

			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	if exitCode != 0 {
		t.Fatalf("container did not exit 0 within the deadline (got %d)", exitCode)
	}

	// 3. Read the container's real stdout via Containers.ListLogs and assert it
	//    carries the marker.
	logs, err := containersClient.ListLogs(ctx, rg, group, container, &armcontainerinstance.ContainersClientListLogsOptions{
		Tail: to.Ptr[int32](100),
	})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}

	if logs.Content == nil || !strings.Contains(*logs.Content, marker) {
		t.Fatalf("marker %q not found in container logs: %v", marker, logs.Content)
	}

	// 4. Delete the group — the real container is torn down and no leak remains.
	delPoller, err := groupsClient.BeginDelete(ctx, rg, group, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll: %v", err)
	}
}

// containerState returns the current instanceView state of the group's single
// container, or nil if the response has not populated it yet.
func containerState(t *testing.T, g armcontainerinstance.ContainerGroup) *armcontainerinstance.ContainerState {
	t.Helper()

	if g.Properties == nil || len(g.Properties.Containers) != 1 {
		t.Fatalf("expected exactly one container: %+v", g.Properties)
	}

	iv := g.Properties.Containers[0].Properties.InstanceView
	if iv == nil {
		return nil
	}

	return iv.CurrentState
}
