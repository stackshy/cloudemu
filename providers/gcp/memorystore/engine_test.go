package memorystore

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	driver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

type recordingCacheEngine struct {
	prov, deprov int
	host         string
	port         int
}

func (e *recordingCacheEngine) Provision(_ context.Context, _ config.CacheProvisionRequest) (config.ProvisionResult, error) {
	e.prov++
	return config.ProvisionResult{Host: e.host, Port: e.port}, nil
}
func (e *recordingCacheEngine) Deprovision(_ context.Context, _ string) error { e.deprov++; return nil }

func TestCreateCacheUsesEngine(t *testing.T) {
	eng := &recordingCacheEngine{host: "127.0.0.1", port: 6402}
	m := New(config.NewOptions(config.WithCacheEngine(eng)))
	ctx := context.Background()
	info, err := m.CreateCache(ctx, driver.CacheConfig{Name: "c1", Engine: "redis"})
	if err != nil {
		t.Fatalf("CreateCache: %v", err)
	}
	if info.Endpoint != "127.0.0.1:6402" || eng.prov != 1 {
		t.Fatalf("endpoint=%q prov=%d", info.Endpoint, eng.prov)
	}
	if err := m.DeleteCache(ctx, "c1"); err != nil {
		t.Fatalf("DeleteCache: %v", err)
	}
	if eng.deprov != 1 {
		t.Fatalf("expected 1 deprovision, got %d", eng.deprov)
	}
}
