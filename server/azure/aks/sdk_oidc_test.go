package aks_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v6"
)

var issuerURLPattern = regexp.MustCompile(
	`^https://eastus\.oic\.prod-aks\.azure\.com/[0-9a-f-]{36}/[0-9a-f-]{36}/$`)

func putOIDCCluster(t *testing.T, clusters *armcontainerservice.ManagedClustersClient,
	name string, oidc *armcontainerservice.ManagedClusterOIDCIssuerProfile,
) *armcontainerservice.ManagedCluster {
	t.Helper()

	ctx := context.Background()

	poller, err := clusters.BeginCreateOrUpdate(ctx, "rg-1", name, armcontainerservice.ManagedCluster{
		Location: to.Ptr("eastus"),
		Properties: &armcontainerservice.ManagedClusterProperties{
			DNSPrefix:         to.Ptr(name),
			OidcIssuerProfile: oidc,
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}

	resp, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}

	return &resp.ManagedCluster
}

func issuerOf(c *armcontainerservice.ManagedCluster) (enabled bool, url string) {
	p := c.Properties.OidcIssuerProfile
	if p == nil {
		return false, ""
	}

	if p.Enabled != nil {
		enabled = *p.Enabled
	}

	if p.IssuerURL != nil {
		url = *p.IssuerURL
	}

	return enabled, url
}

func TestSDKAKSOIDCIssuerURL(t *testing.T) {
	tests := []struct {
		name         string
		createOIDC   *armcontainerservice.ManagedClusterOIDCIssuerProfile
		updateOIDC   *armcontainerservice.ManagedClusterOIDCIssuerProfile
		wantCreate   bool
		wantAfterPut bool
	}{
		{
			name:         "enabled at create stays stable across an update that omits the profile",
			createOIDC:   &armcontainerservice.ManagedClusterOIDCIssuerProfile{Enabled: to.Ptr(true)},
			wantCreate:   true,
			wantAfterPut: true,
		},
		{
			name:         "enabled by a later update",
			updateOIDC:   &armcontainerservice.ManagedClusterOIDCIssuerProfile{Enabled: to.Ptr(true)},
			wantAfterPut: true,
		},
		{
			name:       "disabled has no issuer",
			createOIDC: &armcontainerservice.ManagedClusterOIDCIssuerProfile{Enabled: to.Ptr(false)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clusters, _, _ := newSDKClients(t)
			name := "oidc-cluster"

			created := putOIDCCluster(t, clusters, name, tc.createOIDC)
			enabled, createURL := issuerOf(created)

			if enabled != tc.wantCreate || (createURL != "") != tc.wantCreate {
				t.Fatalf("create: enabled=%v issuerURL=%q, want enabled=%v", enabled, createURL, tc.wantCreate)
			}

			if tc.wantCreate && !issuerURLPattern.MatchString(createURL) {
				t.Fatalf("create issuerURL %q does not match the AKS format", createURL)
			}

			updated := putOIDCCluster(t, clusters, name, tc.updateOIDC)
			enabled, updateURL := issuerOf(updated)

			if enabled != tc.wantAfterPut || (updateURL != "") != tc.wantAfterPut {
				t.Fatalf("update: enabled=%v issuerURL=%q, want enabled=%v", enabled, updateURL, tc.wantAfterPut)
			}

			if tc.wantAfterPut && !issuerURLPattern.MatchString(updateURL) {
				t.Fatalf("update issuerURL %q does not match the AKS format", updateURL)
			}

			if tc.wantCreate && updateURL != createURL {
				t.Fatalf("issuerURL changed across PUT: %q -> %q", createURL, updateURL)
			}

			got, err := clusters.Get(context.Background(), "rg-1", name, nil)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}

			if _, getURL := issuerOf(&got.ManagedCluster); getURL != updateURL {
				t.Fatalf("GET issuerURL %q, want %q", getURL, updateURL)
			}
		})
	}
}
