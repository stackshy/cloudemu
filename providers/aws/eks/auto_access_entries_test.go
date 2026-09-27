package eks

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

const (
	testNodeRole    = "arn:aws:iam::123456789012:role/eks-node"
	testPodExecRole = "arn:aws:iam::123456789012:role/eks-fargate-pods"
)

func listEntries(t *testing.T, m *Mock, cluster string) []string {
	t.Helper()

	entries, err := m.ListAccessEntries(context.Background(), cluster, "")
	requireNoError(t, err)
	sort.Strings(entries)

	return entries
}

func createNodegroup(t *testing.T, m *Mock, name, role, amiType string) {
	t.Helper()

	_, err := m.CreateNodegroup(context.Background(), eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: name, NodeRole: role, AmiType: amiType,
	})
	requireNoError(t, err)
}

func createFargateProfile(t *testing.T, m *Mock, name, role string) {
	t.Helper()

	_, err := m.CreateFargateProfile(context.Background(), eksdriver.FargateProfileConfig{
		ClusterName: "c1", FargateProfileName: name, PodExecutionRole: role,
	})
	requireNoError(t, err)
}

func TestNodegroupCreatesNodeAccessEntry(t *testing.T) {
	tests := []struct {
		name     string
		amiType  string
		wantType string
	}{
		{"linux", "AL2023_x86_64_STANDARD", eksdriver.AccessEntryTypeEC2Linux},
		{"bottlerocket", "BOTTLEROCKET_ARM_64", eksdriver.AccessEntryTypeEC2Linux},
		{"default ami", "", eksdriver.AccessEntryTypeEC2Linux},
		{"windows", "WINDOWS_CORE_2022_x86_64", eksdriver.AccessEntryTypeEC2Windows},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newAPICluster(t, "API")
			createNodegroup(t, m, "ng1", testNodeRole, tc.amiType)

			e, err := m.DescribeAccessEntry(context.Background(), "c1", testNodeRole)
			requireNoError(t, err)
			assertEqual(t, tc.wantType, e.Type)
			assertEqual(t, "system:node:{{EC2PrivateDNSName}}", e.Username)

			if !strings.Contains(e.ARN, ":access-entry/c1/role/123456789012/eks-node/") {
				t.Fatalf("ARN = %s", e.ARN)
			}
		})
	}
}

func TestFargateProfileCreatesAccessEntry(t *testing.T) {
	m := newAPICluster(t, "API_AND_CONFIG_MAP")
	createFargateProfile(t, m, "fp1", testPodExecRole)

	e, err := m.DescribeAccessEntry(context.Background(), "c1", testPodExecRole)
	requireNoError(t, err)
	assertEqual(t, eksdriver.AccessEntryTypeFargateLinux, e.Type)
	assertEqual(t, "system:node:{{SessionName}}", e.Username)
}

func TestNoNodeEntryOnConfigMapCluster(t *testing.T) {
	m := newAPICluster(t, "CONFIG_MAP")
	createNodegroup(t, m, "ng1", testNodeRole, "")
	createFargateProfile(t, m, "fp1", testPodExecRole)

	if got := len(m.accessEntries.All()); got != 0 {
		t.Fatalf("stored entries = %d, want 0", got)
	}
}

func TestNodeEntryKeepsUserEntry(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	_, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: testNodeRole, Type: eksdriver.AccessEntryTypeEC2Linux,
		Tags: map[string]string{"owner": "me"},
	})
	requireNoError(t, err)

	createNodegroup(t, m, "ng1", testNodeRole, "")

	_, err = m.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	e, err := m.DescribeAccessEntry(ctx, "c1", testNodeRole)
	requireNoError(t, err)
	assertEqual(t, "me", e.Tags["owner"])
}

func TestNodeEntryRemovedWithLastOwner(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")
	createNodegroup(t, m, "ng1", testNodeRole, "")
	createNodegroup(t, m, "ng2", testNodeRole, "")

	_, err := m.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if _, err := m.DescribeAccessEntry(ctx, "c1", testNodeRole); err != nil {
		t.Fatalf("entry gone while ng2 still uses the role: %v", err)
	}

	_, err = m.DeleteNodegroup(ctx, "c1", "ng2")
	requireNoError(t, err)

	if _, err := m.DescribeAccessEntry(ctx, "c1", testNodeRole); !cerrors.IsNotFound(err) {
		t.Fatalf("entry after last nodegroup delete: want NotFound, got %v", err)
	}
}

func TestFargateEntryRemovedWithProfile(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")
	createFargateProfile(t, m, "fp1", testPodExecRole)

	_, err := m.DeleteFargateProfile(ctx, "c1", "fp1")
	requireNoError(t, err)

	if got := listEntries(t, m, "c1"); len(got) != 0 {
		t.Fatalf("entries = %v, want none", got)
	}
}

func TestSharedRoleNodegroupAndFargate(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")
	createNodegroup(t, m, "ng1", testNodeRole, "")
	createFargateProfile(t, m, "fp1", testNodeRole)

	// One principal can hold only one entry. The node entry stays EC2_LINUX
	// and is kept while the Fargate profile still uses the role.
	_, err := m.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	e, err := m.DescribeAccessEntry(ctx, "c1", testNodeRole)
	requireNoError(t, err)
	assertEqual(t, eksdriver.AccessEntryTypeEC2Linux, e.Type)

	_, err = m.DeleteFargateProfile(ctx, "c1", "fp1")
	requireNoError(t, err)

	if got := listEntries(t, m, "c1"); len(got) != 0 {
		t.Fatalf("entries = %v, want none", got)
	}
}

func TestModeUpgradeBackfillsEntries(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "c1", CreatorPrincipalArn: "arn:aws:sts::123456789012:assumed-role/admin/s1",
	})
	requireNoError(t, err)

	createNodegroup(t, m, "ng1", testNodeRole, "")
	createFargateProfile(t, m, "fp1", testPodExecRole)

	requireNoError(t, updateAuthMode(m, "API_AND_CONFIG_MAP"))

	want := []string{"arn:aws:iam::123456789012:role/admin", testPodExecRole, testNodeRole}
	sort.Strings(want)

	got := listEntries(t, m, "c1")
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v", got, want)
	}

	admin, err := m.ListAccessEntries(ctx, "c1", testAdminPolicy)
	requireNoError(t, err)

	if len(admin) != 1 || admin[0] != "arn:aws:iam::123456789012:role/admin" {
		t.Fatalf("admin entries = %v", admin)
	}

	// The next step to API adds nothing new.
	requireNoError(t, updateAuthMode(m, "API"))

	if got := listEntries(t, m, "c1"); len(got) != len(want) {
		t.Fatalf("entries after API = %v", got)
	}
}

func TestAutoEntriesSurviveSnapshot(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")
	createNodegroup(t, m, "ng1", testNodeRole, "")

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newTestMock()
	requireNoError(t, restored.Restore(ctx, json.RawMessage(snap)))

	_, err = restored.DeleteNodegroup(ctx, "c1", "ng1")
	requireNoError(t, err)

	if got := listEntries(t, restored, "c1"); len(got) != 0 {
		t.Fatalf("restored auto entry not removed with its nodegroup: %v", got)
	}
}

func TestCreatorKeptAcrossSnapshotForBackfill(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	_, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{Name: "c1", CreatorPrincipalArn: testUserArn})
	requireNoError(t, err)

	snap, err := m.Snapshot(ctx, false)
	requireNoError(t, err)

	restored := newTestMock()
	requireNoError(t, restored.Restore(ctx, json.RawMessage(snap)))
	requireNoError(t, updateAuthMode(restored, "API_AND_CONFIG_MAP"))

	if got := listEntries(t, restored, "c1"); len(got) != 1 || got[0] != testUserArn {
		t.Fatalf("entries = %v, want [%s]", got, testUserArn)
	}
}

func TestUpdateNodegroupVersionLaunchTemplate(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{
		ClusterName: "c1", NodegroupName: "lt",
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{ID: "lt-0abc", Name: "tmpl", Version: "1"},
	})
	requireNoError(t, err)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "lt", eksdriver.NodegroupVersionUpdate{
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{ID: "lt-0abc", Version: "2"},
	})
	requireNoError(t, err)

	got, err := m.DescribeNodegroup(ctx, "c1", "lt")
	requireNoError(t, err)
	assertEqual(t, "2", got.LaunchTemplate.Version)
	assertEqual(t, "lt-0abc", got.LaunchTemplate.ID)
	assertEqual(t, "tmpl", got.LaunchTemplate.Name)

	// By name works too.
	_, err = m.UpdateNodegroupVersion(ctx, "c1", "lt", eksdriver.NodegroupVersionUpdate{
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{Name: "tmpl", Version: "3"},
	})
	requireNoError(t, err)

	got, _ = m.DescribeNodegroup(ctx, "c1", "lt")
	assertEqual(t, "3", got.LaunchTemplate.Version)

	// A different template is rejected.
	_, err = m.UpdateNodegroupVersion(ctx, "c1", "lt", eksdriver.NodegroupVersionUpdate{
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{ID: "lt-0other", Version: "1"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("other template: want InvalidArgument, got %v", err)
	}

	// Neither id nor name is rejected.
	_, err = m.UpdateNodegroupVersion(ctx, "c1", "lt", eksdriver.NodegroupVersionUpdate{
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{Version: "4"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("no id or name: want InvalidArgument, got %v", err)
	}
}

func TestUpdateNodegroupVersionLaunchTemplateOnPlainNodegroup(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()
	mustCluster(t, m, "c1")

	_, err := m.CreateNodegroup(ctx, eksdriver.NodegroupConfig{ClusterName: "c1", NodegroupName: "plain"})
	requireNoError(t, err)

	_, err = m.UpdateNodegroupVersion(ctx, "c1", "plain", eksdriver.NodegroupVersionUpdate{
		LaunchTemplate: &eksdriver.LaunchTemplateSpecification{ID: "lt-0abc", Version: "2"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}
