package eks

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/config"
	eksdriver "github.com/stackshy/cloudemu/providers/aws/eks/driver"
)

func newTestMock() *Mock {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	opts := config.NewOptions(
		config.WithClock(fc),
		config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"),
	)

	return New(opts)
}

func TestCreateCluster(t *testing.T) {
	tests := []struct {
		name      string
		cfg       eksdriver.ClusterConfig
		expectErr bool
	}{
		{
			name: "success",
			cfg: eksdriver.ClusterConfig{
				Name:    "my-cluster",
				Version: "1.30",
				RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
				VPCConfig: eksdriver.VPCConfig{
					SubnetIDs: []string{"subnet-1", "subnet-2"},
				},
			},
		},
		{
			name:      "missing name",
			cfg:       eksdriver.ClusterConfig{Version: "1.30"},
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()

			cluster, err := m.CreateCluster(context.Background(), tc.cfg)
			assertError(t, err, tc.expectErr)

			if tc.expectErr {
				return
			}

			assertEqual(t, tc.cfg.Name, cluster.Name)
			assertEqual(t, "ACTIVE", cluster.Status)
			assertNotEmpty(t, cluster.ARN)
			assertNotEmpty(t, cluster.Endpoint)
			assertNotEmpty(t, cluster.CertificateAuthority)
		})
	}
}

func TestCreateCluster_Duplicate(t *testing.T) {
	m := newTestMock()
	cfg := eksdriver.ClusterConfig{Name: "c1", Version: "1.30"}

	_, err := m.CreateCluster(context.Background(), cfg)
	requireNoError(t, err)

	if _, err := m.CreateCluster(context.Background(), cfg); err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
}

func TestClusterLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	requireNoError(t, err)

	got, err := m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, "c1", got.Name)

	names, err := m.ListClusters(ctx)
	requireNoError(t, err)
	assertEqual(t, 1, len(names))

	upd, err := m.UpdateClusterVersion(ctx, "c1", "1.31")
	requireNoError(t, err)
	assertEqual(t, "Successful", upd.Status)

	got, err = m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, "1.31", got.Version)

	_, err = m.UpdateClusterConfig(ctx, "c1",
		eksdriver.VPCConfig{EndpointPublicAccess: true, PublicAccessCidrs: []string{"0.0.0.0/0"}},
		map[string]string{"env": "dev"})
	requireNoError(t, err)

	got, err = m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, true, got.VPCConfig.EndpointPublicAccess)
	assertEqual(t, "dev", got.Tags["env"])

	deleted, err := m.DeleteCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, "DELETING", deleted.Status)

	if _, err := m.DescribeCluster(ctx, "c1"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestUpdateCluster_NotFound(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.UpdateClusterVersion(ctx, "missing", "1.30"); err == nil {
		t.Fatal("expected error for missing cluster")
	}

	if _, err := m.UpdateClusterConfig(ctx, "missing", eksdriver.VPCConfig{}, nil); err == nil {
		t.Fatal("expected error for missing cluster")
	}
}

func TestNodegroupLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	requireNoError(t, err)

	ng, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName:   "c1",
		NodegroupName: "ng1",
		NodeRole:      "arn:aws:iam::123456789012:role/eks-node",
		Subnets:       []string{"subnet-1"},
		ScalingConfig: eksdriver.NodegroupScalingConfig{MinSize: 1, MaxSize: 3, DesiredSize: 2},
	})
	requireNoError(t, err)
	assertEqual(t, "ACTIVE", ng.Status)
	assertNotEmpty(t, ng.ARN)

	got, err := m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, "ng1", got.NodegroupName)

	names, err := m.ListNodegroups(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, 1, len(names))

	upd, err := m.UpdateNodegroupConfig(ctx, "c1", "ng1",
		&eksdriver.NodegroupScalingConfig{MinSize: 2, MaxSize: 5, DesiredSize: 3}, nil)
	requireNoError(t, err)
	assertEqual(t, "Successful", upd.Status)

	got, err = m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, 3, got.ScalingConfig.DesiredSize)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "ng1", "1.31", "")
	requireNoError(t, err)

	got, err = m.DescribeNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)
	assertEqual(t, "1.31", got.Version)

	_, err = m.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if _, err := m.DescribeNodegroup(ctx, "c1", "ng1"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestCreateNodegroup_ClusterMustExist(t *testing.T) {
	m := newTestMock()

	_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName:   "missing",
		NodegroupName: "ng1",
	})
	if err == nil {
		t.Fatal("expected NotFound when cluster missing")
	}
}

func TestFargateProfileLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	requireNoError(t, err)

	fp, err := m.CreateFargateProfile(ctx, eksdriver.FargateProfileConfig{
		ClusterName:        "c1",
		FargateProfileName: "fp1",
		PodExecutionRole:   "arn:aws:iam::123456789012:role/fargate",
		Selectors:          []eksdriver.FargateProfileSelector{{Namespace: "default"}},
	})
	requireNoError(t, err)
	assertEqual(t, "ACTIVE", fp.Status)
	assertNotEmpty(t, fp.ARN)

	got, err := m.DescribeFargateProfile(ctx, "c1", "fp1")
	requireNoError(t, err)
	assertEqual(t, "fp1", got.FargateProfileName)

	names, err := m.ListFargateProfiles(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, 1, len(names))

	_, err = m.DeleteFargateProfile(ctx, "c1", "fp1")
	requireNoError(t, err)

	if _, err := m.DescribeFargateProfile(ctx, "c1", "fp1"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

func TestAddonLifecycle(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	requireNoError(t, err)

	ad, err := m.CreateAddon(ctx, eksdriver.AddonConfig{
		ClusterName:  "c1",
		AddonName:    "vpc-cni",
		AddonVersion: "v1.0",
	})
	requireNoError(t, err)
	assertEqual(t, "ACTIVE", ad.Status)
	assertNotEmpty(t, ad.ARN)

	got, err := m.DescribeAddon(ctx, "c1", "vpc-cni")
	requireNoError(t, err)
	assertEqual(t, "v1.0", got.AddonVersion)

	names, err := m.ListAddons(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, 1, len(names))

	upd, err := m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "vpc-cni", AddonVersion: "v2.0",
	})
	requireNoError(t, err)
	assertEqual(t, "Successful", upd.Status)

	got, err = m.DescribeAddon(ctx, "c1", "vpc-cni")
	requireNoError(t, err)
	assertEqual(t, "v2.0", got.AddonVersion)

	_, err = m.DeleteAddon(ctx, "c1", "vpc-cni")
	requireNoError(t, err)

	if _, err := m.DescribeAddon(ctx, "c1", "vpc-cni"); err == nil {
		t.Fatal("expected NotFound after delete")
	}
}

// DeleteCluster must fail until child nodegroups, profiles, and addons are
// all cleared — matching real EKS behaviour.
func TestDeleteCluster_RejectsAttachedChildren(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", Version: "1.30"})
	requireNoError(t, err)

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)

	if _, err := m.DeleteCluster(ctx, "c1"); err == nil {
		t.Fatal("expected DeleteCluster to reject when nodegroup attached")
	}

	_, err = m.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	_, err = m.CreateFargateProfile(ctx, eksdriver.FargateProfileConfig{ClusterName: "c1", FargateProfileName: "fp1"})
	requireNoError(t, err)

	if _, err := m.DeleteCluster(ctx, "c1"); err == nil {
		t.Fatal("expected DeleteCluster to reject when Fargate profile attached")
	}

	_, err = m.DeleteFargateProfile(ctx, "c1", "fp1")
	requireNoError(t, err)

	_, err = m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "vpc-cni"})
	requireNoError(t, err)

	if _, err := m.DeleteCluster(ctx, "c1"); err == nil {
		t.Fatal("expected DeleteCluster to reject when addon attached")
	}

	_, err = m.DeleteAddon(ctx, "c1", "vpc-cni")
	requireNoError(t, err)

	_, err = m.DeleteCluster(ctx, "c1")
	requireNoError(t, err)
}

// requireNoError fails the test immediately if err is non-nil.
func requireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertError asserts that err matches the expectErr expectation.
func assertError(t *testing.T, err error, expectErr bool) {
	t.Helper()

	switch {
	case expectErr && err == nil:
		t.Fatal("expected error, got nil")
	case !expectErr && err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}

// assertEqual asserts that expected and actual are equal.
func assertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if expected != actual {
		t.Errorf("expected %v, got %v", expected, actual)
	}
}

// assertNotEmpty asserts that s is non-empty.
func assertNotEmpty(t *testing.T, s string) {
	t.Helper()

	if s == "" {
		t.Error("expected non-empty string")
	}
}
