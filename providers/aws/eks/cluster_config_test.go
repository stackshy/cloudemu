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

// TestUpdateClusterConfigAppliesLogging verifies UpdateClusterConfig actually
// applies a supplied logging change (not just VPC config/tags) to the stored
// cluster, so a subsequent DescribeCluster reflects it, and that the returned
// Update reports the LoggingUpdate type rather than a stale hardcoded one.
func TestUpdateClusterConfigAppliesLogging(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name:    "log-cluster",
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
	})
	requireNoError(t, err)

	upd, err := m.UpdateClusterConfig(ctx, "log-cluster", nil,
		[]eksdriver.ClusterLogging{{Types: []string{"api", "audit"}, Enabled: true}}, nil, nil)
	requireNoError(t, err)
	assertEqual(t, "LoggingUpdate", upd.Type)
	assertEqual(t, "Successful", upd.Status)

	got, err := m.DescribeCluster(ctx, "log-cluster")
	requireNoError(t, err)

	enabled := map[string]bool{}
	for _, l := range got.Logging {
		for _, ty := range l.Types {
			enabled[ty] = l.Enabled
		}
	}

	assertEqual(t, true, enabled["api"])
	assertEqual(t, true, enabled["audit"])
	// Types not mentioned in the update stay at their prior (default-disabled)
	// state rather than being dropped or reset.
	assertEqual(t, false, enabled["scheduler"])
}

// TestUpdateClusterConfigAppliesAccessConfig verifies UpdateClusterConfig
// applies a supplied accessConfig.authenticationMode change and returns an
// Update whose type reflects that (AccessConfigUpdate), matching real EKS.
func TestUpdateClusterConfigAppliesAccessConfig(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name:    "access-cluster",
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
	})
	requireNoError(t, err)

	upd, err := m.UpdateClusterConfig(ctx, "access-cluster", nil,
		nil, &eksdriver.AccessConfigUpdate{AuthenticationMode: "API_AND_CONFIG_MAP"}, nil)
	requireNoError(t, err)
	assertEqual(t, "AccessConfigUpdate", upd.Type)

	got, err := m.DescribeCluster(ctx, "access-cluster")
	requireNoError(t, err)
	assertEqual(t, "API_AND_CONFIG_MAP", got.AccessConfig.AuthenticationMode)
}

// TestUpdateClusterConfigPreservesEndpointAccess verifies a logging-only update
// (no resourcesVpcConfig in the request) does NOT reset the cluster's endpoint
// public/private access flags — real EKS only changes the fields the request
// actually carries, so an omitted resourcesVpcConfig leaves VPC config intact.
func TestUpdateClusterConfigPreservesEndpointAccess(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name:    "vpc-cluster",
		RoleArn: "arn:aws:iam::123456789012:role/eks-cluster",
		VPCConfig: eksdriver.VPCConfig{
			EndpointPublicAccess:  true,
			EndpointPrivateAccess: true,
		},
	})
	requireNoError(t, err)

	// A logging-only update must not touch the endpoint-access flags.
	_, err = m.UpdateClusterConfig(ctx, "vpc-cluster", nil,
		[]eksdriver.ClusterLogging{{Types: []string{"api"}, Enabled: true}}, nil, nil)
	requireNoError(t, err)

	got, err := m.DescribeCluster(ctx, "vpc-cluster")
	requireNoError(t, err)
	assertEqual(t, true, got.VPCConfig.EndpointPublicAccess)
	assertEqual(t, true, got.VPCConfig.EndpointPrivateAccess)
}
