package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	driver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

func TestCreateCacheRejectsUnknownEngine(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "mysql"})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("mysql err = %v, want InvalidArgument", err)
	}

	for _, engine := range []string{"redis", "valkey", "memcached"} {
		if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c-" + engine, Engine: engine}); err != nil {
			t.Fatalf("engine %s: %v", engine, err)
		}
	}
}

func TestCreateReplicationGroupRejectsBadEngine(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	for _, engine := range []string{"mysql", "memcached"} {
		_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg-" + engine, Engine: engine})
		if !cerrors.IsInvalidArgument(err) {
			t.Fatalf("engine %s err = %v, want InvalidArgument", engine, err)
		}
	}

	if _, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg-v", Engine: "valkey"}); err != nil {
		t.Fatalf("valkey: %v", err)
	}
}

func TestNodeCountLimits(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "m1", Engine: "memcached", NumCacheNodes: -1})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("create -1 err = %v, want InvalidArgument", err)
	}

	if _, err = m.CreateCache(ctx, driver.CacheConfig{Name: "m2", Engine: "memcached", NumCacheNodes: 2}); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = m.ModifyCache(ctx, driver.ModifyCacheConfig{Name: "m2", NumCacheNodes: -1})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("modify -1 err = %v, want InvalidArgument", err)
	}

	_, err = m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg7", NumCacheNodes: 7})
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("rg 7 err = %v, want InvalidArgument", err)
	}

	if _, err = m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg6", NumCacheNodes: 6}); err != nil {
		t.Fatalf("rg 6: %v", err)
	}

	_, err = m.ModifyReplicationGroup(ctx, "rg6", 7)
	if !cerrors.IsInvalidArgument(err) {
		t.Fatalf("modify rg 7 err = %v, want InvalidArgument", err)
	}
}

func TestCreateCacheRequiresParameterGroup(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	_, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", ParameterGroupName: "totally-does-not-exist"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("missing group err = %v, want NotFound", err)
	}

	for _, name := range []string{"default.redis7", "default.redis6.x.cluster.on", "default.valkey8"} {
		if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c-" + name, ParameterGroupName: name}); err != nil {
			t.Fatalf("default group %s: %v", name, err)
		}
	}

	if _, err := m.CreateCacheParameterGroup(ctx, "pg", "redis7", "x"); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c2", ParameterGroupName: "pg"}); err != nil {
		t.Fatalf("custom group: %v", err)
	}
}

func TestDefaultParameterGroupDescribable(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	groups, err := m.DescribeCacheParameterGroups(ctx, []string{"default.redis7"})
	if err != nil || len(groups) != 1 || groups[0].Family != "redis7" {
		t.Fatalf("describe default.redis7 = %+v, %v", groups, err)
	}

	params, err := m.DescribeCacheParameters(ctx, "default.memcached1.6", "")
	if err != nil || len(params) == 0 {
		t.Fatalf("describe parameters = %d, %v", len(params), err)
	}

	if _, err := m.DescribeCacheParameterGroups(ctx, []string{"default.redis99"}); !cerrors.IsNotFound(err) {
		t.Fatalf("unknown family err = %v, want NotFound", err)
	}

	if _, err := m.CreateCacheParameterGroup(ctx, "default.redis7", "redis7", "x"); !cerrors.IsAlreadyExists(err) {
		t.Fatalf("create default err = %v, want AlreadyExists", err)
	}

	if err := m.ModifyCacheParameterGroup(ctx, "default.redis7", nil); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("modify default err = %v, want InvalidArgument", err)
	}
}

func TestDeleteCacheParameterGroupGuards(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	if err := m.DeleteCacheParameterGroup(ctx, "default.redis7"); !cerrors.IsInvalidArgument(err) {
		t.Fatalf("delete default err = %v, want InvalidArgument", err)
	}

	if _, err := m.CreateCacheParameterGroup(ctx, "pg", "redis7", "x"); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	if _, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", ParameterGroupName: "pg"}); err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if err := m.DeleteCacheParameterGroup(ctx, "pg"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("in-use delete err = %v, want FailedPrecondition", err)
	}

	if err := m.DeleteCache(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}

	if err := m.DeleteCacheParameterGroup(ctx, "pg"); err != nil {
		t.Fatalf("delete after cluster gone: %v", err)
	}
}

func TestReplicationGroupParameterGroup(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	_, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg0", ParameterGroupName: "nope"})
	if !cerrors.IsNotFound(err) {
		t.Fatalf("missing group err = %v, want NotFound", err)
	}

	if _, err = m.CreateCacheParameterGroup(ctx, "pg", "redis7", "x"); err != nil {
		t.Fatalf("CreateCacheParameterGroup: %v", err)
	}

	if _, err = m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1", ParameterGroupName: "pg"}); err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if err = m.DeleteCacheParameterGroup(ctx, "pg"); !cerrors.IsFailedPrecondition(err) {
		t.Fatalf("in-use delete err = %v, want FailedPrecondition", err)
	}

	if _, err = m.ModifyReplicationGroupParameterGroup(ctx, "rg1", "nope"); !cerrors.IsNotFound(err) {
		t.Fatalf("modify to missing err = %v, want NotFound", err)
	}

	rg, err := m.ModifyReplicationGroupParameterGroup(ctx, "rg1", "default.redis7")
	if err != nil || rg.ParameterGroupName != "default.redis7" {
		t.Fatalf("modify = %+v, %v", rg, err)
	}

	if err = m.DeleteCacheParameterGroup(ctx, "pg"); err != nil {
		t.Fatalf("delete after modify: %v", err)
	}
}

func TestDescribeCacheParameterGroupsListsDefaults(t *testing.T) {
	m := New(config.NewOptions())

	groups, err := m.DescribeCacheParameterGroups(context.Background(), nil)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}

	if len(groups) != len(defaultGroupNames()) {
		t.Fatalf("got %d groups, want %d defaults", len(groups), len(defaultGroupNames()))
	}

	if _, ok := defaultGroupFamily("default.memcached1.6.cluster.on"); ok {
		t.Fatalf("memcached cluster.on group must not exist")
	}
}

func TestDefaultParameterGroupName(t *testing.T) {
	tests := []struct{ engine, version, want string }{
		{"redis", "7.1", "default.redis7"},
		{"redis", "6.2", "default.redis6.x"},
		{"redis", "5.0.6", "default.redis5.0"},
		{"valkey", "8.0", "default.valkey8"},
		{"memcached", "1.6.22", "default.memcached1.6"},
		{"", "", ""},
	}

	for _, tt := range tests {
		if got := DefaultParameterGroupName(tt.engine, tt.version); got != tt.want {
			t.Errorf("DefaultParameterGroupName(%q, %q) = %q, want %q", tt.engine, tt.version, got, tt.want)
		}
	}
}
