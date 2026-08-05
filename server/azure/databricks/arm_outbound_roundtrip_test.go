package databricks_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databricks/armdatabricks"
)

// controlPlaneCategory is one of the static categories the emulator synthesizes
// for a workspace's outbound network dependencies (see the provider's
// ListOutboundNetworkDependencies).
const controlPlaneCategory = "control-plane"

func newOutboundClient(t *testing.T) *armdatabricks.OutboundNetworkDependenciesEndpointsClient {
	t.Helper()

	opts, sub := newARMOptions(t)
	seedWorkspace(t, opts, testRG, testWS)

	client, err := armdatabricks.NewOutboundNetworkDependenciesEndpointsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	return client
}

func TestSDKOutboundList(t *testing.T) {
	client := newOutboundClient(t)

	resp, err := client.List(context.Background(), testRG, testWS, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(resp.OutboundEnvironmentEndpointArray) == 0 {
		t.Fatal("expected a non-empty outbound endpoint array")
	}

	foundControlPlane := false

	for i, ep := range resp.OutboundEnvironmentEndpointArray {
		if ep == nil {
			t.Fatalf("outbound endpoint %d is nil", i)
		}

		if ep.Category == nil || *ep.Category == "" {
			t.Fatalf("outbound endpoint %d has an empty category", i)
		}

		if *ep.Category == controlPlaneCategory {
			foundControlPlane = true
		}

		if len(ep.Endpoints) == 0 {
			t.Fatalf("category %q has no endpoints", *ep.Category)
		}

		hasDomainWithHTTPS := false

		for _, dep := range ep.Endpoints {
			if dep == nil || dep.DomainName == nil || *dep.DomainName == "" {
				continue
			}

			for _, detail := range dep.EndpointDetails {
				if detail != nil && detail.Port != nil && *detail.Port == 443 {
					hasDomainWithHTTPS = true
				}
			}
		}

		if !hasDomainWithHTTPS {
			t.Fatalf("category %q has no domain with a port 443 endpoint detail", *ep.Category)
		}
	}

	if !foundControlPlane {
		t.Fatalf("expected category %q in the outbound endpoints", controlPlaneCategory)
	}
}

func TestSDKOutboundListWorkspaceNotFound(t *testing.T) {
	opts, sub := newARMOptions(t)

	client, err := armdatabricks.NewOutboundNetworkDependenciesEndpointsClient(sub, fakeCred{}, opts)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.List(context.Background(), testRG, "does-not-exist", nil)
	if err == nil {
		t.Fatal("expected error for missing workspace")
	}
}
