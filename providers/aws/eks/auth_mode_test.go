package eks

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	eksdriver "github.com/stackshy/cloudemu/v2/providers/aws/eks/driver"
)

func updateAuthMode(m *Mock, mode string) error {
	_, err := m.UpdateClusterConfig(context.Background(), "c1", nil, nil,
		&eksdriver.AccessConfigUpdate{AuthenticationMode: mode}, nil)

	return err
}

func TestAuthModeTransitions(t *testing.T) {
	tests := []struct {
		from, to string
		ok       bool
	}{
		{"CONFIG_MAP", "API_AND_CONFIG_MAP", true},
		{"API_AND_CONFIG_MAP", "API", true},
		{"CONFIG_MAP", "API", false},
		{"API_AND_CONFIG_MAP", "CONFIG_MAP", false},
		{"API", "API_AND_CONFIG_MAP", false},
		{"API", "CONFIG_MAP", false},
		{"CONFIG_MAP", "IAM", false},
	}

	for _, tc := range tests {
		t.Run(tc.from+"->"+tc.to, func(t *testing.T) {
			m := newAPICluster(t, tc.from)
			err := updateAuthMode(m, tc.to)

			if tc.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}

			if !tc.ok && !cerrors.IsInvalidArgument(err) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}

			c, _ := m.DescribeCluster(context.Background(), "c1")

			want := tc.from
			if tc.ok {
				want = tc.to
			}

			if c.AccessConfig.AuthenticationMode != want {
				t.Fatalf("mode = %s, want %s", c.AccessConfig.AuthenticationMode, want)
			}
		})
	}
}

func TestAuthModeSameValueIsNoOp(t *testing.T) {
	m := newAPICluster(t, "API")
	if err := updateAuthMode(m, "API"); err != nil {
		t.Fatalf("same mode: %v", err)
	}
}

func TestCreateClusterRejectsUnknownAuthMode(t *testing.T) {
	_, err := newTestMock().CreateCluster(context.Background(), eksdriver.ClusterConfig{
		Name: "c1", AccessConfig: eksdriver.AccessConfigRequest{AuthenticationMode: "IAM"},
	})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("want InvalidArgument, got %v", err)
	}
}

func TestAccessEntriesOpenAfterModeUpgrade(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "CONFIG_MAP")

	if err := updateAuthMode(m, "API_AND_CONFIG_MAP"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	if _, err := m.CreateAccessEntry(ctx, eksdriver.AccessEntryConfig{ClusterName: "c1", PrincipalArn: testRoleArn}); err != nil {
		t.Fatalf("create after upgrade: %v", err)
	}
}

func TestBootstrapCreatorAdminEntry(t *testing.T) {
	ctx := context.Background()
	no := false

	tests := []struct {
		name        string
		cfg         eksdriver.ClusterConfig
		wantCreator string
	}{
		{"assumed role maps to the role", eksdriver.ClusterConfig{
			CreatorPrincipalArn: "arn:aws:sts::123456789012:assumed-role/admin/session-1",
		}, "arn:aws:iam::123456789012:role/admin"},
		{"iam user kept", eksdriver.ClusterConfig{
			CreatorPrincipalArn: "arn:aws:iam::123456789012:user/bob",
		}, "arn:aws:iam::123456789012:user/bob"},
		{"access key only", eksdriver.ClusterConfig{CreatorAccessKeyID: "test"}, "arn:aws:iam::123456789012:user/test"},
		{"no credentials", eksdriver.ClusterConfig{}, "arn:aws:iam::123456789012:user/cloudemu"},
		{"bootstrap off", eksdriver.ClusterConfig{
			AccessConfig: eksdriver.AccessConfigRequest{BootstrapClusterCreatorAdminPermissions: &no},
		}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			tc.cfg.Name = "c1"

			if tc.cfg.AccessConfig.AuthenticationMode == "" {
				tc.cfg.AccessConfig.AuthenticationMode = "API"
			}

			if _, err := m.CreateCluster(ctx, tc.cfg); err != nil {
				t.Fatalf("create: %v", err)
			}

			entries, err := m.ListAccessEntries(ctx, "c1", testAdminPolicy)
			if err != nil {
				t.Fatalf("list: %v", err)
			}

			if tc.wantCreator == "" {
				if len(entries) != 0 {
					t.Fatalf("entries = %v, want none", entries)
				}

				return
			}

			if len(entries) != 1 || entries[0] != tc.wantCreator {
				t.Fatalf("entries = %v, want [%s]", entries, tc.wantCreator)
			}

			policies, _ := m.ListAssociatedAccessPolicies(ctx, "c1", tc.wantCreator)
			if len(policies) != 1 || policies[0].AccessScope.Type != eksdriver.AccessScopeCluster {
				t.Fatalf("policies = %+v", policies)
			}
		})
	}
}

func TestNoBootstrapEntryOnConfigMapCluster(t *testing.T) {
	ctx := context.Background()
	m := newAPICluster(t, "CONFIG_MAP")

	if err := updateAuthMode(m, "API_AND_CONFIG_MAP"); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	if entries, _ := m.ListAccessEntries(ctx, "c1", ""); len(entries) != 0 {
		t.Fatalf("entries = %v, want none", entries)
	}
}
