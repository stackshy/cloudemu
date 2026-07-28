package aws

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// fakeEKS lets a test drive DescribeCluster/ListNodegroups outcomes per cluster
// name without racing a real DeleteCluster.
type fakeEKS struct {
	names      []string
	describe   func(name string) (*eksdriver.Cluster, error)
	nodegroups func(name string) ([]string, error)
}

func (f fakeEKS) ListClusters(context.Context) ([]string, error) { return f.names, nil }

func (f fakeEKS) DescribeCluster(_ context.Context, name string) (*eksdriver.Cluster, error) {
	return f.describe(name)
}

func (f fakeEKS) ListNodegroups(_ context.Context, name string) ([]string, error) {
	return f.nodegroups(name)
}

// A DeleteCluster racing between ListClusters and the per-cluster reads makes
// DescribeCluster/ListNodegroups return NotFound. That cluster must be omitted,
// not turned into an error — engine.List would otherwise propagate it and drop
// every provider's inventory, not just this cluster.
func TestEKSDiscoverySkipsVanishedCluster(t *testing.T) {
	ctx := context.Background()

	tests := map[string]fakeEKS{
		"cluster deleted before describe": {
			names: []string{"gone", "live"},
			describe: func(name string) (*eksdriver.Cluster, error) {
				if name == "gone" {
					return nil, cerrors.New(cerrors.NotFound, "cluster gone")
				}

				return &eksdriver.Cluster{Name: name, ARN: "arn:aws:eks:us-east-1:123456789012:cluster/" + name}, nil
			},
			nodegroups: func(string) ([]string, error) { return []string{"ng"}, nil },
		},
		"cluster deleted before node-group list": {
			names: []string{"gone", "live"},
			describe: func(name string) (*eksdriver.Cluster, error) {
				return &eksdriver.Cluster{Name: name, ARN: "arn:aws:eks:us-east-1:123456789012:cluster/" + name}, nil
			},
			nodegroups: func(name string) ([]string, error) {
				if name == "gone" {
					return nil, cerrors.New(cerrors.NotFound, "cluster gone")
				}

				return []string{"ng"}, nil
			},
		},
	}

	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			out, err := eksDiscovery{f}.DiscoverClusters(ctx)
			if err != nil {
				t.Fatalf("a vanished cluster must be skipped, not error: %v", err)
			}

			if len(out) != 1 || out[0].Name != "live" {
				t.Fatalf("want only the live cluster, got %+v", out)
			}

			if out[0].Region != "us-east-1" {
				t.Errorf("region = %q, want us-east-1 (parsed from the verbatim ARN)", out[0].Region)
			}
		})
	}
}

// Any error that is not NotFound is a real fault and must propagate — a scan
// that half-read the inventory must not be reported as complete.
func TestEKSDiscoveryPropagatesRealErrors(t *testing.T) {
	f := fakeEKS{
		names:      []string{"x"},
		describe:   func(string) (*eksdriver.Cluster, error) { return nil, cerrors.New(cerrors.Internal, "boom") },
		nodegroups: func(string) ([]string, error) { return nil, nil },
	}

	if _, err := (eksDiscovery{f}).DiscoverClusters(context.Background()); err == nil {
		t.Error("a non-NotFound error must propagate, not be swallowed")
	}
}
