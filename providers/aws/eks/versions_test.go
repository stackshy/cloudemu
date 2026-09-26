package eks

import (
	"context"
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

func TestCreateClusterVersionFormat(t *testing.T) {
	for _, v := range []string{"0.1", "1.x", "2.0", "1.30.1", "1.", "v1.30", "1.-3"} {
		t.Run(v, func(t *testing.T) {
			_, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1", Version: v})
			requireInvalidArg(t, err, "unsupported Kubernetes version "+v)
		})
	}

	c, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{Name: "c1"})
	requireNoError(t, err)
	assertEqual(t, "1.36", c.Version)
}

func TestDefaultVersionIsNewestInCatalog(t *testing.T) {
	assertEqual(t, "1."+strconv.Itoa(catalogMaxMinor), defaultKubernetesVersion)
}

func TestUpdateClusterVersionRules(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		target string
		want   string
	}{
		{"1.32", "Unsupported Kubernetes minor version update from 1.30 to 1.32"},
		{"1.30", "Unsupported Kubernetes minor version update from 1.30 to 1.30"},
		{"1.29", "Unsupported Kubernetes minor version update from 1.30 to 1.29"},
		{"0.1", "unsupported Kubernetes version 0.1"},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			m, _ := newVersionMock(t, "1.30")
			_, err := m.UpdateClusterVersion(ctx, "c1", tc.target, true)
			requireInvalidArg(t, err, tc.want)

			got, err := m.DescribeCluster(ctx, "c1")
			requireNoError(t, err)
			assertEqual(t, "1.30", got.Version)
		})
	}

	m, _ := newVersionMock(t, "1.30")
	upd, err := m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)
	assertEqual(t, "VersionUpdate", upd.Type)
}

func TestUpdateClusterVersionRollback(t *testing.T) {
	ctx := context.Background()
	m, fc := newVersionMock(t, "1.30")

	_, err := m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)

	fc.Advance(6 * 24 * time.Hour)

	upd, err := m.UpdateClusterVersion(ctx, "c1", "1.30", false)
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)

	got, err := m.DescribeCluster(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, "1.30", got.Version)

	// The cluster was created at 1.30, so it can't roll back again.
	_, err = m.UpdateClusterVersion(ctx, "c1", "1.29", false)
	requireInvalidArg(t, err, "from 1.30 to 1.29")

	// Upgrading again after a rollback is fine.
	_, err = m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)
}

func TestUpdateClusterVersionRollbackWindow(t *testing.T) {
	ctx := context.Background()
	m, fc := newVersionMock(t, "1.30")

	_, err := m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)

	fc.Advance(rollbackWindow + time.Minute)

	_, err = m.UpdateClusterVersion(ctx, "c1", "1.30", true)
	requireInvalidArg(t, err, "from 1.31 to 1.30")
}

func TestUpdateClusterVersionRollbackBlockedByNodegroup(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.30")

	_, err := m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)

	_, err = m.UpdateClusterVersion(ctx, "c1", "1.30", false)
	if !cerrors.IsFailedPrecondition(err) || !strings.Contains(err.Error(), "ng1") {
		t.Fatalf("expected a FailedPrecondition naming ng1, got %v", err)
	}

	upd, err := m.UpdateClusterVersion(ctx, "c1", "1.30", true)
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)
}

func TestNodegroupVersionCappedByCluster(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.31")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "big", Version: "1.32"})
	requireInvalidArg(t, err, "cannot be newer than cluster c1 version 1.31")

	_, err = m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "bad", Version: "0.1"})
	requireInvalidArg(t, err, "unsupported Kubernetes version 0.1")

	ng, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "ng1"})
	requireNoError(t, err)
	assertEqual(t, "1.31", ng.Version)

	old, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "old", Version: "1.30"})
	requireNoError(t, err)
	assertEqual(t, "1.30", old.Version)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "old", "1.32", "")
	requireInvalidArg(t, err, "cannot be newer than cluster c1 version 1.31")

	// No version means the cluster version.
	_, err = m.UpdateNodegroupVersion(ctx, "c1", "old", "", "")
	requireNoError(t, err)

	got, err := m.DescribeNodegroup(ctx, "c1", "old")
	requireNoError(t, err)
	assertEqual(t, "1.31", got.Version)
}

func TestCreateAddonResolvesDefaultVersion(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		cluster string
		addon   string
		want    string
	}{
		{"1.30", "vpc-cni", "v1.23.1-eksbuild.1"},
		{"1.28", "coredns", "v1.10.1-eksbuild.38"},
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
	m, _ := newVersionMock(t, "1.30")

	_, err := m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "no-such-addon"})
	requireInvalidArg(t, err, "Addon no-such-addon specified is not supported in 1.30 kubernetes version")

	_, err = m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "vpc-cni", AddonVersion: "v9.9.9"})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	// A real kube-proxy build, but for 1.36 only.
	_, err = m.CreateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.36.0-eksbuild.21",
	})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	// A cluster version outside the catalog has no add-ons.
	m2, _ := newVersionMock(t, "1.50")
	_, err = m2.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "vpc-cni"})
	requireInvalidArg(t, err, "Addon vpc-cni specified is not supported in 1.50 kubernetes version")

	names, err := m.ListAddons(ctx, "c1")
	requireNoError(t, err)
	assertEqual(t, 0, len(names))
}

func TestUpdateAddonVersionRules(t *testing.T) {
	ctx := context.Background()
	m, _ := newVersionMock(t, "1.30")

	_, err := m.CreateAddon(ctx, eksdriver.AddonConfig{ClusterName: "c1", AddonName: "kube-proxy"})
	requireNoError(t, err)

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.31.14-eksbuild.32",
	})
	requireInvalidArg(t, err, "Addon version specified is not supported")

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", ConfigurationValues: "{not json",
	})
	requireInvalidArg(t, err, "ConfigurationValue provided in request is not supported")

	got, err := m.DescribeAddon(ctx, "c1", "kube-proxy")
	requireNoError(t, err)
	assertEqual(t, "v1.30.14-eksbuild.20", got.AddonVersion)
	assertEqual(t, "", got.ConfigurationValues)

	// After a cluster upgrade the newer build is compatible.
	_, err = m.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)

	_, err = m.UpdateAddon(ctx, eksdriver.AddonConfig{
		ClusterName: "c1", AddonName: "kube-proxy", AddonVersion: "v1.31.14-eksbuild.32",
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
			m, _ := newVersionMock(t, "1.30")

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
	src, _ := newVersionMock(t, "1.30")

	_, err := src.UpdateClusterVersion(ctx, "c1", "1.31", false)
	requireNoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newTestMock()
	requireNoError(t, dst.Restore(ctx, raw))

	upd, err := dst.UpdateClusterVersion(ctx, "c1", "1.30", false)
	requireNoError(t, err)
	assertEqual(t, "VersionRollback", upd.Type)
}
