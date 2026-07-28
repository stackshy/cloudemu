package resourcediscovery

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeK8s struct {
	clusters []DiscoveredCluster
	err      error
}

func (f fakeK8s) DiscoverClusters(context.Context) ([]DiscoveredCluster, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.clusters, nil
}

// A managed cluster that never appears in the inventory is one an operator
// can't schedule, cost, or clean up. Each cluster and every node group must
// surface, tagged to the right cloud's canonical ARN/ID.
func TestWalkKubernetesSurfacesClustersAndNodeGroups(t *testing.T) {
	ctx := context.Background()

	drv := fakeK8s{clusters: []DiscoveredCluster{
		{
			Name:       "prod",
			Region:     "us-west-2",
			Tags:       map[string]string{"env": "prod"},
			NodeGroups: []string{"ng-a", "ng-b"},
		},
		{Name: "bare"}, // no region, no node groups
	}}

	cases := []struct {
		provider  string
		arnSubstr string
	}{
		{ProviderAWS, "eks"},
		// exact: the GCP self-link must embed the cluster's own region
		// (us-west-2), not the engine default (us-east-1).
		{ProviderGCP, "projects/acct-1/locations/us-west-2/clusters/prod"},
		{ProviderAzure, "Microsoft.ContainerService"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			eng := New(tc.provider, "acct-1", "us-east-1", &Drivers{Kubernetes: drv})

			got, err := eng.walkKubernetes(ctx)
			if err != nil {
				t.Fatalf("walkKubernetes: %v", err)
			}

			var clusters, nodeGroups int
			var prodCluster *Resource

			for i := range got {
				r := got[i]
				if r.Service != ServiceKubernetes {
					t.Errorf("unexpected service %q", r.Service)
				}

				switch r.Type {
				case TypeCluster:
					clusters++
					if r.ID == "prod" {
						prodCluster = &got[i]
					}
				case TypeNodeGroup:
					nodeGroups++
				default:
					t.Errorf("unexpected type %q", r.Type)
				}
			}

			if clusters != 2 || nodeGroups != 2 {
				t.Fatalf("got %d clusters / %d node groups, want 2 / 2", clusters, nodeGroups)
			}

			if prodCluster == nil {
				t.Fatal("prod cluster missing")
			}

			if prodCluster.Region != "us-west-2" {
				t.Errorf("prod region = %q, want us-west-2", prodCluster.Region)
			}

			if !strings.Contains(prodCluster.ARN, tc.arnSubstr) {
				t.Errorf("prod ARN %q does not contain %q", prodCluster.ARN, tc.arnSubstr)
			}
		})
	}
}

// A cluster with no explicit region inherits the engine default.
func TestWalkKubernetesRegionFallback(t *testing.T) {
	eng := New(ProviderAWS, "acct-1", "eu-west-1", &Drivers{
		Kubernetes: fakeK8s{clusters: []DiscoveredCluster{{Name: "c"}}},
	})

	got, err := eng.walkKubernetes(context.Background())
	if err != nil {
		t.Fatalf("walkKubernetes: %v", err)
	}

	if len(got) != 1 || got[0].Region != "eu-west-1" {
		t.Fatalf("region fallback failed: %+v", got)
	}
}

// A driver that models clusters and then fails to list them has a real
// problem; swallowing it would report an inventory missing whatever the walk
// could not read.
func TestWalkKubernetesPropagatesErrors(t *testing.T) {
	eng := New(ProviderGCP, "acct-1", "us-east-1", &Drivers{
		Kubernetes: fakeK8s{err: errors.New("list clusters failed")},
	})

	if _, err := eng.walkKubernetes(context.Background()); err == nil {
		t.Error("a failing cluster listing should surface, not be swallowed")
	}
}
