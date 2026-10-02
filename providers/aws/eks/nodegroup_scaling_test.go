package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

func intPtr(n int) *int { return &n }

// TestCreateNodegroupScalingDefault checks that a nodegroup created without a
// scalingConfig gets the EKS defaults (min 1, max 2, desired 2) and that the
// data plane runs two Nodes for it.
func TestCreateNodegroupScalingDefault(t *testing.T) {
	m := newTestMock()
	base := nodesFixture(t, m)

	created, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng1", Subnets: []string{"subnet-a"},
	})
	requireNoError(t, err)

	want := eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 2, DesiredSize: 2}
	if created.ScalingConfig != want {
		t.Fatalf("scalingConfig = %+v, want %+v", created.ScalingConfig, want)
	}

	if n := len(listNodes(t, base)); n != 2 {
		t.Fatalf("nodes = %d, want 2", n)
	}
}

func TestCreateNodegroupScalingValidation(t *testing.T) {
	tests := []struct {
		name    string
		scaling eksdriver.NodegroupScalingConfig
		want    string
	}{
		{"min above desired", eksdriver.NodegroupScalingConfig{MinSize: 2, MaxSize: 3, DesiredSize: 1},
			"Minimum capacity 2 can't be greater than desired size 1"},
		{"desired above max", eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 1, DesiredSize: 2},
			"desired capacity 2 can't be greater than max size 1"},
		{"min above max", eksdriver.NodegroupScalingConfig{MinSize: 5, MaxSize: 2, DesiredSize: 1},
			"Minimum capacity 5 can't be greater than desired size 1"},
		{"explicit zero max", eksdriver.NodegroupScalingConfig{},
			"maxSize must be greater than or equal to 1"},
		{"negative min", eksdriver.NodegroupScalingConfig{MinSize: -1, MaxSize: 2, DesiredSize: 1},
			"minSize must be greater than or equal to 0"},
		{"negative desired", eksdriver.NodegroupScalingConfig{MinSize: 0, MaxSize: 2, DesiredSize: -1},
			"desiredSize must be greater than or equal to 0"},
		{"max above quota", eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 451, DesiredSize: 1},
			"maxSize can't be greater than 450"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			mustCluster(t, m, "c1")

			scaling := tc.scaling
			_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
				ClusterName: "c1", NodegroupName: "ng1", ScalingConfig: &scaling,
			})
			requireInvalidArg(t, err, tc.want)
		})
	}
}

// TestCreateNodegroupScalingBounds checks the edges that must be accepted:
// a scale-to-zero group and one at the node quota.
func TestCreateNodegroupScalingBounds(t *testing.T) {
	for _, s := range []eksdriver.NodegroupScalingConfig{
		{MinSize: 0, MaxSize: 1, DesiredSize: 0},
		{MinSize: 0, MaxSize: 450, DesiredSize: 0},
	} {
		m := newTestMock()
		mustCluster(t, m, "c1")

		scaling := s
		got, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
			ClusterName: "c1", NodegroupName: "ng1", ScalingConfig: &scaling,
		})
		requireNoError(t, err)

		if got.ScalingConfig != s {
			t.Fatalf("scalingConfig = %+v, want %+v", got.ScalingConfig, s)
		}
	}
}

// TestUpdateNodegroupConfigPartialScaling checks that an update merges only
// the sizes it names onto the current config, and validates the merged result.
func TestUpdateNodegroupConfigPartialScaling(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "ng1",
		ScalingConfig: &eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
	})
	requireNoError(t, err)

	_, err = m.UpdateNodegroupConfig(ctx, "c1", "ng1", eksdriver.NodegroupConfigUpdate{
		Scaling: &eksdriver.NodegroupScalingUpdate{DesiredSize: intPtr(3)},
	})
	requireNoError(t, err)

	got, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	want := eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 3}
	if got.ScalingConfig != want {
		t.Fatalf("scalingConfig = %+v, want %+v", got.ScalingConfig, want)
	}

	// Lowering maxSize below the current desiredSize is rejected on the
	// merged config, and nothing changes.
	_, err = m.UpdateNodegroupConfig(ctx, "c1", "ng1", eksdriver.NodegroupConfigUpdate{
		Scaling: &eksdriver.NodegroupScalingUpdate{MaxSize: intPtr(2)},
	})
	requireInvalidArg(t, err, "desired capacity 3 can't be greater than max size 2")

	_, err = m.UpdateNodegroupConfig(ctx, "c1", "ng1", eksdriver.NodegroupConfigUpdate{
		Scaling: &eksdriver.NodegroupScalingUpdate{MinSize: intPtr(4)},
	})
	requireInvalidArg(t, err, "Minimum capacity 4 can't be greater than desired size 3")

	got, err = m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if got.ScalingConfig != want {
		t.Fatalf("after rejected updates scalingConfig = %+v, want %+v", got.ScalingConfig, want)
	}
}
