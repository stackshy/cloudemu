package eks

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

// requireInvalidArg fails unless err is InvalidArgument and mentions want.
func requireInvalidArg(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected InvalidArgument %q, got nil", want)
	}

	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}

	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

// requireException fails unless err carries the given EKS exception name and
// mentions want.
func requireException(t *testing.T, err error, exception, want string) {
	t.Helper()

	var ex interface{ EKSException() (string, int) }
	if !errors.As(err, &ex) {
		t.Fatalf("expected %s, got %v", exception, err)
	}

	if name, _ := ex.EKSException(); name != exception {
		t.Fatalf("exception = %s, want %s (%v)", name, exception, err)
	}

	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

func newVersionMock(t *testing.T, version string) (*Mock, *config.FakeClock) {
	t.Helper()

	m := newTestMock()

	fc, ok := m.opts.Clock.(*config.FakeClock)
	if !ok {
		t.Fatal("test mock must use a FakeClock")
	}

	_, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1", Version: version})
	requireNoError(t, err)

	return m, fc
}

func toVersion(v string) eksdriver.ClusterVersionUpdate {
	return eksdriver.ClusterVersionUpdate{Version: v}
}

func TestCreateClusterVersionFormat(t *testing.T) {
	for _, v := range []string{"0.1", "1.x", "2.0", "1.30.1", "1.", "v1.33", "1.-3"} {
		t.Run(v, func(t *testing.T) {
			_, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1", Version: v})
			requireInvalidArg(t, err, "unsupported Kubernetes version "+v)
		})
	}

	c, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1"})
	requireNoError(t, err)
	assertEqual(t, "1.36", c.Version)
}

// TestCreateClusterSupportedRange checks the window real EKS offers today:
// 1.34-1.36 in standard support and 1.31-1.33 in extended support.
func TestCreateClusterSupportedRange(t *testing.T) {
	for _, v := range []string{"1.28", "1.29", "1.30", "1.37", "1.99"} {
		t.Run(v, func(t *testing.T) {
			_, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1", Version: v})
			requireInvalidArg(t, err, "unsupported Kubernetes version "+v)
		})
	}

	for _, v := range []string{"1.31", "1.33", "1.34", "1.36"} {
		t.Run(v, func(t *testing.T) {
			_, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1", Version: v})
			requireNoError(t, err)
		})
	}
}

func TestDefaultVersionIsNewestInCatalog(t *testing.T) {
	assertEqual(t, "1."+strconv.Itoa(catalogMaxMinor), defaultKubernetesVersion)
	assertEqual(t, 31, supportedMinMinor)
}

func TestUpdateClusterVersionRules(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		target string
		want   string
	}{
		{"1.34", "Unsupported Kubernetes minor version update from 1.32 to 1.34"},
		{"1.32", "Unsupported Kubernetes minor version update from 1.32 to 1.32"},
		{"1.31", "Unsupported Kubernetes minor version update from 1.32 to 1.31"},
		{"0.1", "unsupported Kubernetes version 0.1"},
		{"1.30", "unsupported Kubernetes version 1.30"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			m, _ := newVersionMock(t, "1.32")
			_, err := m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{Version: tc.target, Force: true})
			requireInvalidArg(t, err, tc.want)

			got, err := m.DescribeCluster(ctx, "c1")
			requireNoError(t, err)
			assertEqual(t, "1.32", got.Version)
		})
	}

	m, _ := newVersionMock(t, "1.32")
	upd, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)
	assertEqual(t, "VersionUpdate", upd.Type)

	// The newest version has nothing above it.
	top, _ := newVersionMock(t, "1.36")
	_, err = top.UpdateClusterVersion(ctx, "c1", toVersion("1.37"))
	requireInvalidArg(t, err, "unsupported Kubernetes version 1.37")
}

func TestUpdateClusterVersionNeedsNodegroupsAtClusterVersion(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.33")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "old", Version: "1.32"})
	requireNoError(t, err)

	// force does not override this rule.
	_, err = m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{Version: "1.34", Force: true})
	if !cerrors.IsFailedPrecondition(err) || !strings.Contains(err.Error(), "must be at the cluster version 1.33") {
		t.Fatalf("expected FailedPrecondition naming the cluster version, got %v", err)
	}

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "old", eksdriver.NodegroupVersionUpdate{})
	requireNoError(t, err)

	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.34"))
	requireNoError(t, err)
}

func TestUpdateClusterVersionRollback(t *testing.T) {
	ctx := context.Background()
	m, fc := newVersionMock(t, "1.32")

	_, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)

	fc.Advance(6 * 24 * time.Hour)

	upd, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)

	got, err := m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, "1.32", got.Version)

	// The cluster was created at 1.32, so it can't roll back again.
	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.31"))
	requireInvalidArg(t, err, "from 1.32 to 1.31")

	// Upgrading again after a rollback is fine.
	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)
}

func TestUpdateClusterVersionRollbackWindow(t *testing.T) {
	ctx := context.Background()
	m, fc := newVersionMock(t, "1.32")

	_, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)

	fc.Advance(rollbackWindow + time.Minute)

	_, err = m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{Version: "1.32", Force: true})
	requireInvalidArg(t, err, "from 1.33 to 1.32")
}

func TestUpdateClusterVersionRollbackBlockedByNodegroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.32")

	_, err := m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)

	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireException(t, err, "InvalidStateException", "ng1")

	upd, err := m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{Version: "1.32", Force: true})
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)
}

func TestUpdateClusterVersionRollbackConfig(t *testing.T) {
	ctx := context.Background()

	for _, bad := range []int{0, 119, 10081} {
		t.Run(strconv.Itoa(bad), func(t *testing.T) {
			m, _ := newVersionMock(t, "1.32")
			timeout := bad
			_, err := m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{
				Version: "1.33", RollbackTimeoutMinutes: &timeout,
			})
			requireInvalidArg(t, err, "rollbackConfig.timeoutMinutes must be between 120 and 10080")
		})
	}

	m, _ := newVersionMock(t, "1.32")
	timeout := 720
	_, err := m.UpdateClusterVersion(ctx, "c1", eksdriver.ClusterVersionUpdate{
		Version: "1.33", RollbackTimeoutMinutes: &timeout,
	})
	requireNoError(t, err)
}

func TestNodegroupVersionCappedByCluster(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.33")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "big", Version: "1.34"})
	requireInvalidArg(t, err, "cannot be newer than cluster c1 version 1.33")

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "bad", Version: "0.1"})
	requireInvalidArg(t, err, "unsupported Kubernetes version 0.1")

	ng, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)
	assertEqual(t, "1.33", ng.Version)

	old, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "old", Version: "1.32"})
	requireNoError(t, err)
	assertEqual(t, "1.32", old.Version)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "old", eksdriver.NodegroupVersionUpdate{Version: "1.34"})
	requireInvalidArg(t, err, "cannot be newer than cluster c1 version 1.33")

	// No version means the cluster version.
	_, err = m.UpdateNodegroupVersion(ctx, "c1", "old", eksdriver.NodegroupVersionUpdate{})
	requireNoError(t, err)

	got, err := m.DescribeNodegroup(ctx, "c1", "old")
	requireNoError(t, err)
	assertEqual(t, "1.33", got.Version)
}

func TestCreateAddonResolvesDefaultVersion(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		cluster string
		addon   string
		want    string
	}{
		{"1.32", "vpc-cni", "v1.23.1-eksbuild.1"},
		{"1.31", "coredns", "v1.11.4-eksbuild.53"},
		{"1.34", "coredns", "v1.13.2-eksbuild.24"},
		{"1.31", "kube-proxy", "v1.31.14-eksbuild.32"},
		{"1.36", "kube-proxy", "v1.36.0-eksbuild.21"},
	}

	for _, tc := range tests {
		t.Run(tc.cluster+"/"+tc.addon, func(t *testing.T) {
			m, _ := newVersionMock(t, tc.cluster)

			ad, err := m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: tc.addon})
			requireNoError(t, err)
			assertEqual(t, tc.want, ad.AddonVersion)

			got, err := m.DescribeAddon(ctx, "c1", tc.addon)
			requireNoError(t, err)
			assertEqual(t, tc.want, got.AddonVersion)
		})
	}
}

func TestCreateAddonRejectsUnsupported(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.32")

	_, err := m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "no-such-addon"})
	requireInvalidArg(t, err, "Addon no-such-addon specified is not supported in 1.32 kubernetes version")

	_, err = m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "vpc-cni", AddonVersion: "v9.9.9"})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	// A real kube-proxy build, but for 1.36 only.
	_, err = m.CreateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.36.0-eksbuild.21",
	})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	names, err := m.ListAddons(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, 0, len(names))
}

func TestUpdateAddonVersionRules(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.32")

	_, err := m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "kube-proxy"})
	requireNoError(t, err)

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.33.10-eksbuild.25",
	})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", ConfigurationValues: "{not json",
	})
	requireInvalidArg(t, err, "ConfigurationValue provided in request is not supported")

	got, err := m.DescribeAddon(ctx, "c1", "kube-proxy")
	requireNoError(t, err)
	assertEqual(t, "v1.32.13-eksbuild.28", got.AddonVersion)
	assertEqual(t, "", got.ConfigurationValues)

	// After a cluster upgrade the newer build is compatible.
	_, err = m.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.33.10-eksbuild.25",
	})
	requireNoError(t, err)
}

func TestAddonConfigurationValues(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name   string
		values string
		ok     bool
	}{
		{"json", `{"env":{"ENABLE_PREFIX_DELEGATION":"true"}}`, true},
		{"yaml", "env:\n  ENABLE_PREFIX_DELEGATION: \"true\"\n", true},
		{"broken json", `{"env":`, false},
		{"scalar", "just-a-string", false},
		{"list", "- a\n- b\n", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newVersionMock(t, "1.32")

			ad, err := m.CreateAddon(ctx, eksdriver.AddonConfig{
				ClusterName: "c1", AddonName: "vpc-cni", ConfigurationValues: tc.values,
			})
			if !tc.ok {
				requireInvalidArg(t, err, "ConfigurationValue provided in request is not supported")

				return
			}

			requireNoError(t, err)
			assertEqual(t, tc.values, ad.ConfigurationValues)
		})
	}
}

func TestSnapshotKeepsRollbackState(t *testing.T) {
	ctx := context.Background()
	src, _ := newVersionMock(t, "1.32")

	_, err := src.UpdateClusterVersion(ctx, "c1", toVersion("1.33"))
	requireNoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, raw))

	upd, err := dst.UpdateClusterVersion(ctx, "c1", toVersion("1.32"))
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)
}
