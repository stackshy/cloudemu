package eks

import (
	"context"
	"testing"

	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// TestCreateClusterConfigEcho verifies CreateCluster faithfully round-trips the
// caller's logging, kubernetesNetworkConfig, and accessConfig into the stored
// cluster read back by DescribeCluster.
func TestCreateClusterConfigEcho(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	bootstrap := false
	cfg := eksdriver.ClusterConfig{
		Name:    "echo-cluster",
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
		Logging: []eksdriver.ClusterLogging{
			{Types: []string{"api", "audit"}, Enabled: true},
			{Types: []string{"authenticator", "controllerManager", "scheduler"}, Enabled: false},
		},
		NetworkConfig: eksdriver.NetworkConfig{
			ServiceIPv4CIDR: "172.20.0.0/16",
			IPFamily:        "ipv4",
		},
		AccessConfig: eksdriver.AccessConfigRequest{
			AuthenticationMode:                      "API",
			BootstrapClusterCreatorAdminPermissions: &bootstrap,
		},
	}

	if _, err := m.CreateCluster(ctx, cfg); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(ctx, "echo-cluster")
	requireNoError(t, err)

	if len(got.Logging) != 2 {
		t.Fatalf("logging entries = %d, want 2", len(got.Logging))
	}

	assertEqual(t, true, got.Logging[0].Enabled)
	assertEqual(t, "api", got.Logging[0].Types[0])
	assertEqual(t, false, got.Logging[1].Enabled)

	assertEqual(t, "172.20.0.0/16", got.NetworkConfig.ServiceIPv4CIDR)
	assertEqual(t, "ipv4", got.NetworkConfig.IPFamily)

	assertEqual(t, "API", got.AccessConfig.AuthenticationMode)
	assertEqual(t, false, got.AccessConfig.BootstrapClusterCreatorAdminPermissions)
}

// TestCreateClusterConfigDefaults verifies that omitting logging,
// kubernetesNetworkConfig, and accessConfig yields the real-EKS defaults so an
// aws_eks_cluster read does not drift after create.
func TestCreateClusterConfigDefaults(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name:    "default-cluster",
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(ctx, "default-cluster")
	requireNoError(t, err)

	// Logging default: one entry, all five control-plane types, disabled.
	if len(got.Logging) != 1 {
		t.Fatalf("logging entries = %d, want 1", len(got.Logging))
	}

	assertEqual(t, false, got.Logging[0].Enabled)
	assertEqual(t, 5, len(got.Logging[0].Types))
	assertEqual(t, "api", got.Logging[0].Types[0])
	assertEqual(t, "scheduler", got.Logging[0].Types[4])

	// Network default: ipv4 family with an auto-assigned service CIDR.
	assertEqual(t, "ipv4", got.NetworkConfig.IPFamily)
	assertEqual(t, "10.100.0.0/16", got.NetworkConfig.ServiceIPv4CIDR)
	assertEqual(t, "", got.NetworkConfig.ServiceIPv6CIDR)

	// Access default: CONFIG_MAP (EKS API/SDK path) + bootstrap true.
	assertEqual(t, "CONFIG_MAP", got.AccessConfig.AuthenticationMode)
	assertEqual(t, true, got.AccessConfig.BootstrapClusterCreatorAdminPermissions)
}

// TestCreateClusterConfigIPv6 verifies the ipv6 ipFamily path assigns a service
// IPv6 CIDR rather than an IPv4 one.
func TestCreateClusterConfigIPv6(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	if _, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name:          "ipv6-cluster",
		RoleArn:       "arn:aws:iam::123456789012:role/eks-cluster",
		NetworkConfig: eksdriver.NetworkConfig{IPFamily: "ipv6"},
	}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	got, err := m.DescribeCluster(ctx, "ipv6-cluster")
	requireNoError(t, err)

	assertEqual(t, "ipv6", got.NetworkConfig.IPFamily)
	assertEqual(t, "fd00::/108", got.NetworkConfig.ServiceIPv6CIDR)
	assertEqual(t, "", got.NetworkConfig.ServiceIPv4CIDR)
}
