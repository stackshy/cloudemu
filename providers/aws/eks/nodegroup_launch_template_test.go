package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// TestCreateNodegroupLaunchTemplateRoundTrip verifies a nodegroup created with
// a launchTemplate stores it and returns it on both Create and Describe,
// matching real EKS (launchTemplate is not a silently dropped field).
func TestCreateNodegroupLaunchTemplateRoundTrip(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	created, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "lt-ng",
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{
			ID:      "lt-0123456789abcdef0",
			Version: "3",
		},
	})
	requireNoError(t, err)

	if created.LaunchTemplate == nil {
		t.Fatal("CreateNodegroup: launchTemplate = nil, want the supplied spec")
	}

	assertEqual(t, "lt-0123456789abcdef0", created.LaunchTemplate.ID)
	assertEqual(t, "3", created.LaunchTemplate.Version)

	got, err := m.DescribeNodegroup(ctx, "c1", "lt-ng")
	requireNoError(t, err)

	if got.LaunchTemplate == nil {
		t.Fatal("DescribeNodegroup: launchTemplate = nil, want the supplied spec")
	}

	assertEqual(t, "lt-0123456789abcdef0", got.LaunchTemplate.ID)
	assertEqual(t, "3", got.LaunchTemplate.Version)
}

// TestCreateNodegroupNoLaunchTemplate verifies a nodegroup created without a
// launchTemplate reports a nil one, not a synthesized/zero-value spec.
func TestCreateNodegroupNoLaunchTemplate(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()
	mustCluster(t, m, "c1")

	created, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "plain-ng"})
	requireNoError(t, err)

	if created.LaunchTemplate != nil {
		t.Fatalf("LaunchTemplate = %+v, want nil", created.LaunchTemplate)
	}
}
