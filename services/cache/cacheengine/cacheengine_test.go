package cacheengine_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/cache/cacheengine"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

type stub struct{ prov, deprov int }

func (s *stub) Provision(_ context.Context, _ config.CacheProvisionRequest) (config.ProvisionResult, error) {
	s.prov++
	return config.ProvisionResult{Host: "127.0.0.1", Port: 6390}, nil
}
func (s *stub) Deprovision(_ context.Context, _ string) error { s.deprov++; return nil }

func TestIsRedisFamily(t *testing.T) {
	for _, e := range []string{"redis", "Redis", "REDIS"} {
		if !cacheengine.IsRedisFamily(e) {
			t.Errorf("IsRedisFamily(%q) should be true", e)
		}
	}
	for _, e := range []string{"memcached", "", "valkey"} {
		if cacheengine.IsRedisFamily(e) {
			t.Errorf("IsRedisFamily(%q) should be false", e)
		}
	}
}

func TestProvisionOverridesEndpoint(t *testing.T) {
	s := &stub{}
	info := &cachedriver.CacheInfo{Name: "c1", Engine: "redis", Endpoint: "synthetic:6379"}
	if err := cacheengine.Provision(context.Background(), s, info); err != nil {
		t.Fatal(err)
	}
	if info.Endpoint != "127.0.0.1:6390" || s.prov != 1 {
		t.Fatalf("got endpoint=%q prov=%d", info.Endpoint, s.prov)
	}
	mc := &cachedriver.CacheInfo{Name: "m", Engine: "memcached", Endpoint: "keep"}
	_ = cacheengine.Provision(context.Background(), s, mc)
	if mc.Endpoint != "keep" {
		t.Fatal("memcached must stay synthetic")
	}
}
