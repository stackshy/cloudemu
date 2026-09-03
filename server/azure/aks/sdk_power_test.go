package aks_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

// TestSDKAKSClusterStartStop drives the real armcontainerservice SDK through
// BeginStop/BeginStart and asserts powerState.code flips on both the cluster
// and its agent pools, matching real AKS's "stop the whole node fleet"
// semantics.
func TestSDKAKSClusterStartStop(t *testing.T) {
	clusters, pools, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:   to.Ptr("system"),
					Count:  to.Ptr[int32](2),
					VMSize: to.Ptr("Standard_DS2_v2"),
					Mode:   to.Ptr(armcontainerservice.AgentPoolModeSystem),
				},
			},
		},
	})

	stopPoller, err := clusters.BeginStop(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("BeginStop: %v", err)
	}

	if _, err := stopPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Stop PollUntilDone: %v", err)
	}

	props := getClusterProps(t, clusters)
	if props.PowerState == nil || props.PowerState.Code == nil ||
		*props.PowerState.Code != armcontainerservice.CodeStopped {
		t.Fatalf("got powerState %+v, want Stopped", props.PowerState)
	}

	poolGot, err := pools.Get(ctx, "rg-1", "k8s-1", "system", nil)
	if err != nil {
		t.Fatalf("Pool Get: %v", err)
	}

	if poolGot.Properties.PowerState == nil || poolGot.Properties.PowerState.Code == nil ||
		*poolGot.Properties.PowerState.Code != armcontainerservice.CodeStopped {
		t.Fatalf("got pool powerState %+v, want Stopped", poolGot.Properties.PowerState)
	}

	// Stopping an already-stopped cluster is idempotent (no error).
	stopAgainPoller, err := clusters.BeginStop(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("second BeginStop: %v", err)
	}

	if _, err := stopAgainPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("second Stop PollUntilDone: %v", err)
	}

	startPoller, err := clusters.BeginStart(ctx, "rg-1", "k8s-1", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}

	if _, err := startPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Start PollUntilDone: %v", err)
	}

	props = getClusterProps(t, clusters)
	if props.PowerState == nil || props.PowerState.Code == nil ||
		*props.PowerState.Code != armcontainerservice.CodeRunning {
		t.Fatalf("got powerState %+v, want Running", props.PowerState)
	}

	poolGot, err = pools.Get(ctx, "rg-1", "k8s-1", "system", nil)
	if err != nil {
		t.Fatalf("Pool Get after start: %v", err)
	}

	if poolGot.Properties.PowerState == nil || poolGot.Properties.PowerState.Code == nil ||
		*poolGot.Properties.PowerState.Code != armcontainerservice.CodeRunning {
		t.Fatalf("got pool powerState %+v, want Running", poolGot.Properties.PowerState)
	}
}

// TestSDKAKSClusterStartStopMissingCluster asserts start/stop on a managed
// cluster that was never created returns NotFound.
func TestSDKAKSClusterStartStopMissingCluster(t *testing.T) {
	clusters, _, _ := newSDKClients(t)
	ctx := context.Background()

	if _, err := clusters.BeginStop(ctx, "rg-1", "ghost", nil); err == nil {
		t.Fatal("expected error stopping a missing cluster")
	} else {
		assertResponseCode(t, err, http.StatusNotFound)
	}

	if _, err := clusters.BeginStart(ctx, "rg-1", "ghost", nil); err == nil {
		t.Fatal("expected error starting a missing cluster")
	} else {
		assertResponseCode(t, err, http.StatusNotFound)
	}
}

// TestSDKAKSDeleteLastSystemPoolRejected asserts deleting the sole System-mode
// agent pool on a cluster fails — AKS requires at least one System pool at
// all times — while a second System pool makes either deletable.
func TestSDKAKSDeleteLastSystemPoolRejected(t *testing.T) {
	clusters, pools, _ := newSDKClients(t)
	ctx := context.Background()

	createDriftCluster(t, clusters, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			AgentPoolProfiles: []*armcontainerservice.ManagedClusterAgentPoolProfile{
				{
					Name:   to.Ptr("system"),
					Count:  to.Ptr[int32](2),
					VMSize: to.Ptr("Standard_DS2_v2"),
					Mode:   to.Ptr(armcontainerservice.AgentPoolModeSystem),
				},
			},
		},
	})

	if _, err := pools.BeginDelete(ctx, "rg-1", "k8s-1", "system", nil); err == nil {
		t.Fatal("expected error deleting the last System-mode pool")
	} else {
		assertResponseCode(t, err, http.StatusConflict)
	}

	if _, err := pools.Get(ctx, "rg-1", "k8s-1", "system", nil); err != nil {
		t.Fatalf("system pool should still exist after rejected delete: %v", err)
	}

	// Add a second System pool; now the first is deletable.
	poolPoller, err := pools.BeginCreateOrUpdate(ctx, "rg-1", "k8s-1", "system2", armcontainerservice.AgentPool{
		Properties: &armcontainerservice.ManagedClusterAgentPoolProfileProperties{
			Count:  to.Ptr[int32](1),
			VMSize: to.Ptr("Standard_DS2_v2"),
			Mode:   to.Ptr(armcontainerservice.AgentPoolModeSystem),
		},
	}, nil)
	if err != nil {
		t.Fatalf("second system pool BeginCreateOrUpdate: %v", err)
	}

	if _, err := poolPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("second system pool PollUntilDone: %v", err)
	}

	delPoller, err := pools.BeginDelete(ctx, "rg-1", "k8s-1", "system", nil)
	if err != nil {
		t.Fatalf("BeginDelete after adding a second System pool: %v", err)
	}

	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete poll after adding a second System pool: %v", err)
	}
}

func assertResponseCode(t *testing.T, err error, want int) {
	t.Helper()

	var respErr *azcore.ResponseError

	if !errors.As(err, &respErr) {
		t.Fatalf("want *azcore.ResponseError with code %d, got %v", want, err)
	}

	if respErr.StatusCode != want {
		t.Fatalf("got HTTP %d, want %d (%v)", respErr.StatusCode, want, err)
	}
}
