package eks

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

const (
	testRoleArn     = "arn:aws:iam::123456789012:role/dev/apps/my-role"
	testUserArn     = "arn:aws:iam::123456789012:user/alice"
	testAdminPolicy = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
	testViewPolicy  = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSViewPolicy"
)

func newAPICluster(t *testing.T, mode string) *Mock {
	t.Helper()

	m := newTestMock()
	if _, err := m.CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name: "c1", Version: "1.30",
		AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: mode},
	}); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	return m
}

func TestAccessEntryLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API_AND_CONFIG_MAP")

	e, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: testRoleArn, ClientRequestToken: "tok-1",
		Tags: map[string]string{"team": "a"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if e.Type != eksdriver.AccessEntryTypeStandard {
		t.Fatalf("type = %q, want STANDARD", e.Type)
	}

	// The generated username drops the role path, as real EKS does.
	if want := "arn:aws:sts::123456789012:assumed-role/my-role/{{SessionName}}"; e.Username != want {
		t.Fatalf("username = %q, want %q", e.Username, want)
	}

	if !strings.HasPrefix(e.ARN, "arn:aws:eks:us-east-1:123456789012:access-entry/c1/role/123456789012/my-role/") {
		t.Fatalf("unexpected ARN %q", e.ARN)
	}

	// A retry with the same client token returns the same entry.
	again, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: testRoleArn, ClientRequestToken: "tok-1",
	})
	if err != nil || again.ARN != e.ARN {
		t.Fatalf("idempotent create: arn %q err %v", again.ARN, err)
	}

	_, err = m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c1", PrincipalArn: testRoleArn})
	if !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate create: want AlreadyExists, got %v", err)
	}

	groups := []string{"viewers"}
	name := "dev:{{SessionName}}"

	upd, err := m.UpdateAccessEntry(ctx, eksdriver.AccessEntryUpdate{
		ClusterName: "c1", PrincipalArn: testRoleArn, KubernetesGroups: &groups, Username: &name,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if upd.Username != name || len(upd.KubernetesGroups) != 1 || upd.KubernetesGroups[0] != "viewers" {
		t.Fatalf("update not applied: %+v", upd)
	}

	// Leaving fields out keeps them.
	kept, err := m.UpdateAccessEntry(ctx, eksdriver.AccessEntryUpdate{ClusterName: "c1", PrincipalArn: testRoleArn})
	if err != nil || kept.Username != name || len(kept.KubernetesGroups) != 1 {
		t.Fatalf("empty update changed the entry: %+v err %v", kept, err)
	}

	list, err := m.ListAccessEntries(ctx, "c1", "")
	if err != nil || len(list) != 1 || list[0] != testRoleArn {
		t.Fatalf("list = %v err %v", list, err)
	}

	if err := m.DeleteAccessEntry(ctx, "c1", testRoleArn); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := m.DescribeAccessEntry(ctx, "c1", testRoleArn); !cerrors.IsNotFound(err) {
		t.Fatalf("describe after delete: want NotFound, got %v", err)
	}

	if err := m.DeleteAccessEntry(ctx, "c1", testRoleArn); !cerrors.IsNotFound(err) {
		t.Fatalf("second delete: want NotFound, got %v", err)
	}
}

func TestAccessEntryDefaultUsernames(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	tests := []struct {
		principal string
		typ       string
		want      string
	}{
		{testUserArn, "", testUserArn},
		{"arn:aws:iam::123456789012:role/node", eksdriver.AccessEntryTypeEC2Linux, "system:node:{{EC2PrivateDNSName}}"},
		{"arn:aws:iam::123456789012:role/fargate", eksdriver.AccessEntryTypeFargateLinux, "system:node:{{SessionName}}"},
		{"arn:aws:iam::123456789012:role/hybrid", eksdriver.AccessEntryTypeHybridLinux, "system:node:{{SessionName}}"},
	}

	for _, tc := range tests {
		e, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
			ClusterName: "c1", PrincipalArn: tc.principal, Type: tc.typ,
		})
		if err != nil {
			t.Fatalf("%s: create: %v", tc.principal, err)
		}

		if e.Username != tc.want {
			t.Errorf("%s: username = %q, want %q", tc.principal, e.Username, tc.want)
		}
	}
}

func TestAccessEntryRequiresAPIAuthMode(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "CONFIG_MAP")

	_, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c1", PrincipalArn: testRoleArn})
	if !cerrors.IsFailedPrecondition(err) || !strings.Contains(err.Error(), "[API, API_AND_CONFIG_MAP]") {
		t.Fatalf("want FailedPrecondition naming the modes, got %v", err)
	}

	if _, err := m.ListAccessEntries(ctx, "c1", ""); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("list on CONFIG_MAP cluster: want FailedPrecondition, got %v", err)
	}

	if _, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "missing", PrincipalArn: testRoleArn,
	}); !cerrors.IsNotFound(err) {
		t.Fatalf("missing cluster: want NotFound, got %v", err)
	}
}

func TestAccessEntryValidation(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	tests := []struct {
		name string
		cfg  eksdriver.AccessEntryConfig
	}{
		{"not an arn", eksdriver.AccessEntryConfig{PrincipalArn: "my-role"}},
		{"not iam", eksdriver.AccessEntryConfig{PrincipalArn: "arn:aws:s3:::bucket"}},
		{"group principal", eksdriver.AccessEntryConfig{PrincipalArn: "arn:aws:iam::123456789012:group/g"}},
		{"bad account", eksdriver.AccessEntryConfig{PrincipalArn: "arn:aws:iam::12:role/r"}},
		{"sts session", eksdriver.AccessEntryConfig{PrincipalArn: "arn:aws:sts::123456789012:assumed-role/r/s"}},
		{"service-linked role", eksdriver.AccessEntryConfig{
			PrincipalArn: "arn:aws:iam::123456789012:role/aws-service-role/eks.amazonaws.com/AWSServiceRoleForAmazonEKS",
		}},
		{"unknown type", eksdriver.AccessEntryConfig{PrincipalArn: testRoleArn, Type: "ROBOT"}},
		{"node type with groups", eksdriver.AccessEntryConfig{
			PrincipalArn: testRoleArn, Type: eksdriver.AccessEntryTypeEC2Linux, KubernetesGroups: []string{"g"},
		}},
		{"node type with username", eksdriver.AccessEntryConfig{
			PrincipalArn: testRoleArn, Type: eksdriver.AccessEntryTypeEC2Windows, Username: "n",
		}},
		{"node type for a user", eksdriver.AccessEntryConfig{PrincipalArn: testUserArn, Type: eksdriver.AccessEntryTypeEC2Linux}},
		{"node type cross account", eksdriver.AccessEntryConfig{
			PrincipalArn: "arn:aws:iam::999999999999:role/r", Type: eksdriver.AccessEntryTypeFargateLinux,
		}},
		{"reserved username", eksdriver.AccessEntryConfig{PrincipalArn: testRoleArn, Username: "system:admin"}},
		{"session name without colon", eksdriver.AccessEntryConfig{PrincipalArn: testRoleArn, Username: "john{{SessionName}}"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.cfg.ClusterName = "c1"
			if _, err := m.CreateAccessEntry(ctx, tc.cfg); !cerrors.IsInvalidArgument(err) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
		})
	}

	// A cross-account role is fine for a STANDARD entry.
	if _, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: "arn:aws:iam::999999999999:role/r",
	}); err != nil {
		t.Fatalf("cross-account STANDARD: %v", err)
	}
}

func TestAccessEntryNodeTypeUpdateRejected(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	e, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: testRoleArn, Type: eksdriver.AccessEntryTypeEC2Linux,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	groups := []string{"g"}
	if _, err := m.UpdateAccessEntry(ctx, eksdriver.AccessEntryUpdate{
		ClusterName: "c1", PrincipalArn: testRoleArn, KubernetesGroups: &groups,
	}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("groups on EC2_LINUX: want InvalidArgument, got %v", err)
	}

	// Sending back the current username is a no-op, not an error.
	same := e.Username
	if _, err := m.UpdateAccessEntry(ctx, eksdriver.AccessEntryUpdate{
		ClusterName: "c1", PrincipalArn: testRoleArn, Username: &same,
	}); err != nil {
		t.Fatalf("same username: %v", err)
	}
}

func TestAccessPolicyAssociation(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	fc, ok := m.opts.Clock.(*config.FakeClock)
	if !ok {
		t.Fatal("test mock must use a FakeClock")
	}

	if _, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c1", PrincipalArn: testRoleArn}); err != nil {
		t.Fatalf("create: %v", err)
	}

	first, err := m.AssociateAccessPolicy(ctx, "c1", testRoleArn, testAdminPolicy,
		eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}

	if _, err := m.AssociateAccessPolicy(ctx, "c1", testRoleArn, testViewPolicy,
		eksdriver.AccessScope{Type: eksdriver.AccessScopeNamespace, Namespaces: []string{"dev-*"}}); err != nil {
		t.Fatalf("associate view: %v", err)
	}

	fc.Advance(time.Minute)

	// Associating again replaces the scope and keeps associatedAt.
	upd, err := m.AssociateAccessPolicy(ctx, "c1", testRoleArn, testAdminPolicy,
		eksdriver.AccessScope{Type: eksdriver.AccessScopeNamespace, Namespaces: []string{"prod"}})
	if err != nil {
		t.Fatalf("re-associate: %v", err)
	}

	if !upd.AssociatedAt.Equal(first.AssociatedAt) || !upd.ModifiedAt.After(first.ModifiedAt) ||
		upd.AccessScope.Type != eksdriver.AccessScopeNamespace {
		t.Fatalf("re-associate: %+v", upd)
	}

	list, err := m.ListAssociatedAccessPolicies(ctx, "c1", testRoleArn)
	if err != nil || len(list) != 2 {
		t.Fatalf("list associated = %+v err %v", list, err)
	}

	filtered, err := m.ListAccessEntries(ctx, "c1", testViewPolicy)
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filter by policy = %v err %v", filtered, err)
	}

	if none, _ := m.ListAccessEntries(ctx, "c1", accessPolicyARNPrefix+"AmazonEKSEditPolicy"); len(none) != 0 {
		t.Fatalf("filter by unused policy = %v", none)
	}

	if err := m.DisassociateAccessPolicy(ctx, "c1", testRoleArn, testViewPolicy); err != nil {
		t.Fatalf("disassociate: %v", err)
	}

	if err := m.DisassociateAccessPolicy(ctx, "c1", testRoleArn, testViewPolicy); !cerrors.IsNotFound(err) {
		t.Fatalf("second disassociate: want NotFound, got %v", err)
	}

	if _, err := m.AssociateAccessPolicy(ctx, "c1", "arn:aws:iam::123456789012:role/none", testAdminPolicy,
		eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster}); !cerrors.IsNotFound(err) {
		t.Fatalf("associate on missing entry: want NotFound, got %v", err)
	}
}

func TestAccessPolicyAssociationValidation(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")
	node := "arn:aws:iam::123456789012:role/node"

	for _, cfg := range []eksdriver.AccessEntryConfig{
		{ClusterName: "c1", PrincipalArn: testRoleArn},
		{ClusterName: "c1", PrincipalArn: node, Type: eksdriver.AccessEntryTypeEC2Linux},
	} {
		if _, err := m.CreateAccessEntry(ctx, cfg); err != nil {
			t.Fatalf("create %s: %v", cfg.PrincipalArn, err)
		}
	}

	cluster := eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster}
	tests := []struct {
		name      string
		principal string
		policy    string
		scope     eksdriver.AccessScope
	}{
		{"unknown policy", testRoleArn, accessPolicyARNPrefix + "NoSuchPolicy", cluster},
		{"service-linked only policy", testRoleArn, accessPolicyARNPrefix + "AmazonEKSPodIdentityPolicy", cluster},
		{"node entry", node, testAdminPolicy, cluster},
		{"cluster scope with namespaces", testRoleArn, testAdminPolicy,
			eksdriver.AccessScope{Type: eksdriver.AccessScopeCluster, Namespaces: []string{"a"}}},
		{"namespace scope without namespaces", testRoleArn, testAdminPolicy,
			eksdriver.AccessScope{Type: eksdriver.AccessScopeNamespace}},
		{"unknown scope", testRoleArn, testAdminPolicy, eksdriver.AccessScope{Type: "world"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := m.AssociateAccessPolicy(ctx, "c1", tc.principal, tc.policy, tc.scope); !cerrors.IsInvalidArgument(err) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
		})
	}
}

func TestDeleteClusterRemovesAccessEntries(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	if _, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c1", PrincipalArn: testRoleArn}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := m.DeleteCluster(ctx, "c1"); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}

	// A new cluster with the same name starts with no entries.
	if _, err := m.CreateCluster(ctx, eksdriver.ClusterConfig{
		Name: "c1", AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: "API"},
	}); err != nil {
		t.Fatalf("recreate: %v", err)
	}

	if list, err := m.ListAccessEntries(ctx, "c1", ""); err != nil || len(list) != 0 {
		t.Fatalf("entries after cluster delete = %v err %v", list, err)
	}
}

func TestAccessEntryTags(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "API")

	e, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{
		ClusterName: "c1", PrincipalArn: testRoleArn, Tags: map[string]string{"a": "1"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.TagResource(ctx, e.ARN, map[string]string{"b": "2"}); err != nil {
		t.Fatalf("tag: %v", err)
	}

	if err := m.UntagResource(ctx, e.ARN, []string{"a"}); err != nil {
		t.Fatalf("untag: %v", err)
	}

	tags, err := m.ListResourceTags(ctx, e.ARN)
	if err != nil || len(tags) != 1 || tags["b"] != "2" {
		t.Fatalf("tags = %v err %v", tags, err)
	}

	got, _ := m.DescribeAccessEntry(ctx, "c1", testRoleArn)
	if got.Tags["b"] != "2" {
		t.Fatalf("describe tags = %v", got.Tags)
	}

	if _, err := m.ListResourceTags(ctx, e.ARN+"x"); !cerrors.IsNotFound(err) {
		t.Fatalf("unknown entry ARN: want NotFound, got %v", err)
	}
}

func TestListAccessPolicies(t *testing.T) {
	policies, err := newTestMock().ListAccessPolicies(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	found := false

	for i, p := range policies {
		if i > 0 && policies[i-1].Name >= p.Name {
			t.Fatalf("not sorted at %d: %s >= %s", i, policies[i-1].Name, p.Name)
		}

		if p.ARN == testAdminPolicy && p.Name == "AmazonEKSClusterAdminPolicy" {
			found = true
		}
	}

	if !found {
		t.Fatal("AmazonEKSClusterAdminPolicy missing from the catalog")
	}
}
