package chaos_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/chaos"
	"github.com/stackshy/cloudemu/config"
	cerrors "github.com/stackshy/cloudemu/errors"
	awsec2 "github.com/stackshy/cloudemu/providers/aws/ec2"
	awss3 "github.com/stackshy/cloudemu/providers/aws/s3"
)

func TestEngineNilSafe(t *testing.T) {
	var e *chaos.Engine

	if got := e.Check("storage", "PutObject"); got.Error != nil || got.Latency != 0 {
		t.Fatalf("nil engine should be a no-op, got %+v", got)
	}
}

func TestServiceOutage(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	eff := e.Check("storage", "PutObject")
	if eff.Error == nil {
		t.Fatal("expected error during outage, got nil")
	}

	if cerrors.GetCode(eff.Error) != cerrors.Unavailable {
		t.Errorf("expected Unavailable, got %v", eff.Error)
	}

	// Different service should not be affected.
	if eff := e.Check("compute", "RunInstances"); eff.Error != nil {
		t.Errorf("compute should be unaffected, got %v", eff.Error)
	}
}

func TestServiceOutageExpiresAndUnregisters(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", 10*time.Millisecond))

	if e.Check("storage", "Op").Error == nil {
		t.Fatal("should fail while active")
	}

	time.Sleep(20 * time.Millisecond)

	if e.Check("storage", "Op").Error != nil {
		t.Fatal("should recover after window")
	}
}

func TestLatencySpike(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", 50*time.Millisecond, time.Hour))

	if eff := e.Check("storage", "Op"); eff.Latency != 50*time.Millisecond {
		t.Errorf("latency = %v, want 50ms", eff.Latency)
	}

	// Wrong service: no latency.
	if eff := e.Check("compute", "Op"); eff.Latency != 0 {
		t.Errorf("expected 0 latency for compute, got %v", eff.Latency)
	}
}

func TestProbabilisticFailure(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("test failure")
	e.Apply(chaos.ProbabilisticFailure("storage", "GetObject", myErr, 1.0, time.Hour))

	// p=1.0 means every call should fail.
	for range 20 {
		if eff := e.Check("storage", "GetObject"); eff.Error == nil {
			t.Fatal("p=1.0 should always inject")
		}
	}

	// Non-targeted op should not be affected.
	if eff := e.Check("storage", "PutObject"); eff.Error != nil {
		t.Errorf("PutObject should not be affected: %v", eff.Error)
	}
}

func TestProbabilisticFailureZeroProbability(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("never")
	e.Apply(chaos.ProbabilisticFailure("storage", "Op", myErr, 0.0, time.Hour))

	for range 20 {
		if eff := e.Check("storage", "Op"); eff.Error != nil {
			t.Fatalf("p=0.0 should never inject, got %v", eff.Error)
		}
	}
}

func TestThrottle(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.Throttle("compute", "RunInstances", 3, time.Hour))

	// First 3 calls in the same second pass.
	for i := range 3 {
		if eff := e.Check("compute", "RunInstances"); eff.Error != nil {
			t.Fatalf("call %d should pass under threshold, got %v", i, eff.Error)
		}
	}

	// 4th in same second should be throttled.
	if eff := e.Check("compute", "RunInstances"); eff.Error == nil {
		t.Fatal("4th call should be throttled")
	}
}

func TestComposite(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	myErr := errors.New("composite-err")
	e.Apply(chaos.Composite(
		chaos.LatencySpike("storage", 10*time.Millisecond, time.Hour),
		chaos.ProbabilisticFailure("storage", "Op", myErr, 1.0, time.Hour),
	))

	eff := e.Check("storage", "Op")
	if eff.Latency != 10*time.Millisecond {
		t.Errorf("composite latency = %v, want 10ms", eff.Latency)
	}

	if eff.Error == nil {
		t.Error("composite should propagate inner failure")
	}
}

func TestActiveStop(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	a := e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if eff := e.Check("storage", "Op"); eff.Error == nil {
		t.Fatal("should fail while active")
	}

	a.Stop()

	if eff := e.Check("storage", "Op"); eff.Error != nil {
		t.Errorf("should be cleared after Stop, got %v", eff.Error)
	}
}

func TestRecorded(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	for range 3 {
		_ = e.Check("storage", "Op")
	}

	rec := e.Recorded()
	if len(rec) != 3 {
		t.Fatalf("len=%d want 3", len(rec))
	}

	e.Reset()

	if len(e.Recorded()) != 0 {
		t.Error("Reset should clear recorded events")
	}
}

func TestEngineConcurrentSafe(t *testing.T) {
	e := chaos.New(config.RealClock{})
	defer e.Stop()

	e.Apply(chaos.LatencySpike("storage", time.Microsecond, time.Hour))

	const workers = 50

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 100 {
				_ = e.Check("storage", "Op")
			}
		}()
	}

	wg.Wait()

	rec := e.Recorded()
	if len(rec) != workers*100 {
		t.Errorf("recorded=%d want %d", len(rec), workers*100)
	}
}

func TestWrapBucketAppliesChaos(t *testing.T) {
	opts := config.NewOptions()
	bucket := awss3.New(opts)
	e := chaos.New(config.RealClock{})

	wrapped := chaos.WrapBucket(bucket, e)

	ctx := context.Background()
	if err := wrapped.CreateBucket(ctx, "test-bucket"); err != nil {
		t.Fatalf("baseline call should succeed: %v", err)
	}

	e.Apply(chaos.ServiceOutage("storage", time.Hour))

	if err := wrapped.CreateBucket(ctx, "another"); err == nil {
		t.Fatal("expected outage error after chaos applied")
	}
}

func TestWrapComputeAppliesChaos(t *testing.T) {
	opts := config.NewOptions()
	ec2 := awsec2.New(opts)
	e := chaos.New(config.RealClock{})

	wrapped := chaos.WrapCompute(ec2, e)
	ctx := context.Background()

	// Baseline: instance launches fine.
	if _, err := wrapped.RunInstances(ctx, computeInstanceConfig(), 1); err != nil {
		t.Fatalf("baseline RunInstances: %v", err)
	}

	// Apply outage: next call should fail with chaos error before reaching driver.
	e.Apply(chaos.ServiceOutage("compute", time.Hour))

	if _, err := wrapped.RunInstances(ctx, computeInstanceConfig(), 1); err == nil {
		t.Fatal("expected chaos error during compute outage")
	}
}

// computeInstanceConfig builds a minimal InstanceConfig the AWS EC2 mock accepts.
func computeInstanceConfig() computeConfig {
	return computeConfig{
		ImageID:      "ami-test",
		InstanceType: "t2.micro",
	}
}

// computeConfig type-aliases the driver type so the test file doesn't need
// to import the deeply-nested compute/driver package.
type computeConfig = struct {
	ImageID        string
	InstanceType   string
	Tags           map[string]string
	SubnetID       string
	SecurityGroups []string
	KeyName        string
	UserData       string
}