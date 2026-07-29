package rds

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

func TestDBProxyLifecycle(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	proxy, err := m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{
		Name:         "px",
		EngineFamily: "MYSQL",
		RoleARN:      "arn:aws:iam::123456789012:role/proxy",
		VPCSubnetIDs: []string{"subnet-a", "subnet-b"},
		Auth:         []rdsdriver.ProxyAuth{{AuthScheme: "SECRETS", SecretARN: "arn:secret", IAMAuth: "DISABLED"}},
	})
	if err != nil {
		t.Fatalf("CreateDBProxy: %v", err)
	}

	if proxy.ARN == "" || proxy.Endpoint == "" || proxy.Status != "available" {
		t.Fatalf("proxy fields wrong: %+v", proxy)
	}

	if _, err := m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{Name: "px", EngineFamily: "MYSQL"}); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("duplicate: want AlreadyExists, got %v", err)
	}

	if _, err := m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{Name: "px2"}); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("missing engine family: want InvalidArgument, got %v", err)
	}

	// Modify: toggle RequireTLS.
	tlsOn := true
	got, err := m.ModifyDBProxy(ctx, "px", rdsdriver.ModifyDBProxyInput{RequireTLS: &tlsOn})
	if err != nil {
		t.Fatalf("ModifyDBProxy: %v", err)
	}

	if !got.RequireTLS {
		t.Error("RequireTLS not applied")
	}

	// Register targets: one instance, one cluster.
	if _, err := m.CreateInstance(ctx, rdsdriver.InstanceConfig{ID: "db", Engine: "mysql"}); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if _, err := m.CreateCluster(ctx, rdsdriver.ClusterConfig{ID: "cl", Engine: "aurora-mysql"}); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	added, err := m.RegisterDBProxyTargets(ctx, "px", "default", []string{"db"}, []string{"cl"})
	if err != nil {
		t.Fatalf("RegisterDBProxyTargets: %v", err)
	}

	if len(added) != 2 {
		t.Fatalf("registered %d targets, want 2", len(added))
	}

	targets, _ := m.DescribeDBProxyTargets(ctx, "px", "default")
	if len(targets) != 2 {
		t.Fatalf("describe targets: got %d, want 2", len(targets))
	}

	groups, _ := m.DescribeDBProxyTargetGroups(ctx, "px")
	if len(groups) != 1 || !groups[0].IsDefault {
		t.Fatalf("target groups wrong: %+v", groups)
	}

	// Deregister the instance target.
	if err := m.DeregisterDBProxyTargets(ctx, "px", "default", []string{"db"}, nil); err != nil {
		t.Fatalf("DeregisterDBProxyTargets: %v", err)
	}

	if targets, _ := m.DescribeDBProxyTargets(ctx, "px", "default"); len(targets) != 1 {
		t.Fatalf("after deregister: got %d, want 1", len(targets))
	}

	if _, err := m.DeleteDBProxy(ctx, "px"); err != nil {
		t.Fatalf("DeleteDBProxy: %v", err)
	}

	if _, err := m.DescribeDBProxies(ctx, []string{"px"}); !cerrors.IsNotFound(err) {
		t.Fatalf("describe deleted: want NotFound, got %v", err)
	}
}

func TestDescribeDBProxiesReturnsIndependentCopies(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{
		Name: "px", EngineFamily: "MYSQL", VPCSubnetIDs: []string{"s1", "s2"},
	}); err != nil {
		t.Fatalf("CreateDBProxy: %v", err)
	}

	// Mutating a returned slice must not corrupt the store (copy-on-read),
	// via the named-lookup branch...
	got, _ := m.DescribeDBProxies(ctx, []string{"px"})
	got[0].VPCSubnetIDs[0] = "MUTATED"

	// ...and the list-all (len==0) branch.
	all, _ := m.DescribeDBProxies(ctx, nil)
	all[0].VPCSubnetIDs[0] = "MUTATED-TOO"

	again, _ := m.DescribeDBProxies(ctx, []string{"px"})
	if again[0].VPCSubnetIDs[0] != "s1" {
		t.Fatalf("returned slice aliased the store: %v", again[0].VPCSubnetIDs)
	}
}

func TestDBProxyErrors(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	if _, err := m.RegisterDBProxyTargets(ctx, "ghost", "default", []string{"db"}, nil); !cerrors.IsNotFound(err) {
		t.Fatalf("register to missing proxy: want NotFound, got %v", err)
	}

	if _, err := m.CreateDBProxy(ctx, rdsdriver.DBProxyConfig{Name: "px", EngineFamily: "MYSQL"}); err != nil {
		t.Fatalf("CreateDBProxy: %v", err)
	}

	if _, err := m.RegisterDBProxyTargets(ctx, "px", "default", []string{"ghost-db"}, nil); !cerrors.IsNotFound(err) {
		t.Fatalf("register missing instance: want NotFound, got %v", err)
	}
}
