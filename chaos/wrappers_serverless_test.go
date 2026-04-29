package chaos_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu"
	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	serverlessdriver "github.com/stackshy/cloudemu/serverless/driver"
)

func newChaosServerless(t *testing.T) (serverlessdriver.Serverless, *chaos.Engine) {
	t.Helper()

	e := chaos.New(config.RealClock{})
	t.Cleanup(e.Stop)

	return chaos.WrapServerless(cloudemu.NewAWS().Lambda, e), e
}

func TestWrapServerlessCreateFunctionChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()
	cfg := serverlessdriver.FunctionConfig{Name: "fn", Runtime: "go1.x", Handler: "main"}

	if _, err := s.CreateFunction(ctx, cfg); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	cfg.Name = "fn2"
	if _, err := s.CreateFunction(ctx, cfg); err == nil {
		t.Error("expected chaos error on CreateFunction")
	}
}

func TestWrapServerlessDeleteFunctionChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()
	_, _ = s.CreateFunction(ctx, serverlessdriver.FunctionConfig{Name: "del", Runtime: "go1.x", Handler: "main"})

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	if err := s.DeleteFunction(ctx, "del"); err == nil {
		t.Error("expected chaos error on DeleteFunction")
	}
}

func TestWrapServerlessGetFunctionChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()
	_, _ = s.CreateFunction(ctx, serverlessdriver.FunctionConfig{Name: "g", Runtime: "go1.x", Handler: "main"})

	if _, err := s.GetFunction(ctx, "g"); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	if _, err := s.GetFunction(ctx, "g"); err == nil {
		t.Error("expected chaos error on GetFunction")
	}
}

func TestWrapServerlessListFunctionsChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()

	if _, err := s.ListFunctions(ctx); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	if _, err := s.ListFunctions(ctx); err == nil {
		t.Error("expected chaos error on ListFunctions")
	}
}

func TestWrapServerlessUpdateFunctionChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()
	_, _ = s.CreateFunction(ctx, serverlessdriver.FunctionConfig{Name: "u", Runtime: "go1.x", Handler: "main"})

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	if _, err := s.UpdateFunction(ctx, "u", serverlessdriver.FunctionConfig{Name: "u", Runtime: "go1.x", Handler: "main"}); err == nil {
		t.Error("expected chaos error on UpdateFunction")
	}
}

func TestWrapServerlessInvokeChaos(t *testing.T) {
	s, e := newChaosServerless(t)
	ctx := context.Background()
	_, _ = s.CreateFunction(ctx, serverlessdriver.FunctionConfig{Name: "inv", Runtime: "go1.x", Handler: "main"})

	e.Apply(chaos.ServiceOutage("serverless", time.Hour))

	if _, err := s.Invoke(ctx, serverlessdriver.InvokeInput{FunctionName: "inv", Payload: []byte("{}")}); err == nil {
		t.Error("expected chaos error on Invoke")
	}
}
