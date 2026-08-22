package elasticache

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	driver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

type recordingCacheEngine struct {
	provisioned   []config.CacheProvisionRequest
	deprovisioned []string
	host          string
	port          int
}

func (e *recordingCacheEngine) Provision(_ context.Context, req config.CacheProvisionRequest) (config.ProvisionResult, error) {
	e.provisioned = append(e.provisioned, req)

	return config.ProvisionResult{Host: e.host, Port: e.port}, nil
}

func (e *recordingCacheEngine) Deprovision(_ context.Context, id string) error {
	e.deprovisioned = append(e.deprovisioned, id)

	return nil
}

func TestCreateCacheUsesEngineForRedis(t *testing.T) {
	eng := &recordingCacheEngine{host: "127.0.0.1", port: 6400}
	m := New(config.NewOptions(config.WithCacheEngine(eng)))
	ctx := context.Background()

	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if info.Endpoint != "127.0.0.1:6400" {
		t.Fatalf("endpoint not overridden by engine: got %q", info.Endpoint)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].CacheID != "c1" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteCache(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "c1" {
		t.Fatalf("expected one deprovision for c1, got %v", eng.deprovisioned)
	}
}

func TestCreateReplicationGroupUsesEngineForRedis(t *testing.T) {
	eng := &recordingCacheEngine{host: "127.0.0.1", port: 6400}
	m := New(config.NewOptions(config.WithCacheEngine(eng)))
	ctx := context.Background()

	rg, err := m.CreateReplicationGroup(ctx, driver.ReplicationGroupConfig{ID: "rg1", Engine: "redis"})
	if err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	// The primary endpoint must resolve to the real engine host:port a redis
	// client connects to, not the synthetic *.cache.amazonaws.com hostname.
	if rg.PrimaryAddress != "127.0.0.1" || rg.PrimaryPort != 6400 {
		t.Fatalf("primary endpoint not overridden by engine: got %s:%d", rg.PrimaryAddress, rg.PrimaryPort)
	}

	if len(eng.provisioned) != 1 || eng.provisioned[0].CacheID != "rg1" {
		t.Fatalf("unexpected provision calls: %+v", eng.provisioned)
	}

	if err := m.DeleteReplicationGroup(ctx, "rg1"); err != nil {
		t.Fatalf("DeleteReplicationGroup: %v", err)
	}

	if len(eng.deprovisioned) != 1 || eng.deprovisioned[0] != "rg1" {
		t.Fatalf("expected one deprovision for rg1, got %v", eng.deprovisioned)
	}
}

func TestCreateReplicationGroupNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	rg, err := m.CreateReplicationGroup(context.Background(), driver.ReplicationGroupConfig{ID: "rg2", Engine: "redis"})
	if err != nil {
		t.Fatalf("CreateReplicationGroup: %v", err)
	}

	if rg.PrimaryAddress == "" || rg.PrimaryAddress == "127.0.0.1" {
		t.Fatalf("without an engine the primary should be synthetic, got %q", rg.PrimaryAddress)
	}

	if rg.PrimaryPort != defaultRedisPort {
		t.Fatalf("synthetic primary port = %d, want %d", rg.PrimaryPort, defaultRedisPort)
	}
}

func TestCreateCacheSkipsEngineForMemcached(t *testing.T) {
	eng := &recordingCacheEngine{host: "127.0.0.1", port: 6400}
	m := New(config.NewOptions(config.WithCacheEngine(eng)))

	info, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "mc", Engine: "memcached"})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if len(eng.provisioned) != 0 {
		t.Fatalf("engine should not be used for memcached, got %+v", eng.provisioned)
	}

	if info.Endpoint == "127.0.0.1:6400" {
		t.Fatal("memcached endpoint should remain synthetic")
	}
}

func TestCreateCacheNoEngineIsSynthetic(t *testing.T) {
	m := New(config.NewOptions())

	info, err := m.CreateCache(context.Background(), driver.CacheConfig{Name: "c2", Engine: "redis"})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}

	if info.Endpoint == "" || info.Endpoint == "127.0.0.1:6400" {
		t.Fatalf("without an engine the endpoint should be synthetic, got %q", info.Endpoint)
	}
}
