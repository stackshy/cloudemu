package serverkit

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
)

// countingTickable counts its calls and reports changed when told to.
type countingTickable struct {
	calls   atomic.Int64
	changed atomic.Bool
	sawZero atomic.Bool
}

func (c *countingTickable) Tick(now time.Time) bool {
	if now.IsZero() {
		c.sawZero.Store(true)
	}

	c.calls.Add(1)

	return c.changed.Load()
}

// waitTick polls cond until it holds or the deadline passes.
func waitTick(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// settledGoroutines waits for the goroutine count to drop to at most want.
func settledGoroutines(t *testing.T, want int) {
	t.Helper()

	waitTick(t, "goroutines to exit", func() bool {
		runtime.GC()

		return runtime.NumGoroutine() <= want
	})
}

func TestSchedulerTicksRegisteredTickables(t *testing.T) {
	a, b := &countingTickable{}, &countingTickable{}

	var dirty atomic.Int64

	s := newScheduler(config.RealClock{}, func() { dirty.Add(1) })
	s.add(time.Millisecond, func() []config.Tickable { return []config.Tickable{a, b} })
	s.start()
	defer s.stop()

	waitTick(t, "both tickables to tick", func() bool { return a.calls.Load() >= 3 && b.calls.Load() >= 3 })

	if a.sawZero.Load() {
		t.Fatal("Tick got a zero time")
	}

	if dirty.Load() != 0 {
		t.Fatalf("onChange ran %d times with no change", dirty.Load())
	}

	b.changed.Store(true)
	waitTick(t, "onChange after a change", func() bool { return dirty.Load() > 0 })
}

func TestSchedulerResolvesSourceEachTick(t *testing.T) {
	first, second := &countingTickable{}, &countingTickable{}

	var (
		mu  sync.Mutex
		cur config.Tickable = first
	)

	s := newScheduler(config.RealClock{}, nil)
	s.add(time.Millisecond, func() []config.Tickable {
		mu.Lock()
		defer mu.Unlock()

		return []config.Tickable{cur}
	})
	s.start()
	defer s.stop()

	waitTick(t, "first tickable", func() bool { return first.calls.Load() > 0 })

	mu.Lock()
	cur = second
	mu.Unlock()

	waitTick(t, "swapped tickable", func() bool { return second.calls.Load() > 0 })
}

func TestSchedulerStopHaltsTicksWithoutLeak(t *testing.T) {
	base := runtime.NumGoroutine()
	c := &countingTickable{}

	s := newScheduler(config.RealClock{}, nil)
	for range 3 {
		s.add(time.Millisecond, func() []config.Tickable { return []config.Tickable{c} })
	}

	s.start()
	waitTick(t, "ticks", func() bool { return c.calls.Load() >= 3 })

	s.stop()
	s.stop() // idempotent

	frozen := c.calls.Load()

	time.Sleep(20 * time.Millisecond)

	if got := c.calls.Load(); got != frozen {
		t.Fatalf("ticked after stop: %d -> %d", frozen, got)
	}

	settledGoroutines(t, base)
}

func TestSchedulerSkipsDisabledInterval(t *testing.T) {
	base := runtime.NumGoroutine()
	c := &countingTickable{}

	s := newScheduler(config.RealClock{}, nil)
	s.add(0, func() []config.Tickable { return []config.Tickable{c} })
	s.add(-time.Second, func() []config.Tickable { return []config.Tickable{c} })

	if len(s.entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(s.entries))
	}

	s.start()
	time.Sleep(10 * time.Millisecond)
	s.stop()

	if c.calls.Load() != 0 {
		t.Fatalf("disabled entry ticked %d times", c.calls.Load())
	}

	settledGoroutines(t, base)
}

// TestStopWithoutStart covers shutdown when Serve never started the ticks.
func TestStopWithoutStart(t *testing.T) {
	newScheduler(config.RealClock{}, nil).stop()
}

// TestNewTickerEntries checks which entries the App registers: the services
// entry follows TickInterval, the Kubernetes one needs progression.
func TestNewTickerEntries(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want int
	}{
		{"off by default", Config{}, 0},
		{"services tick", Config{TickInterval: time.Second}, 1},
		{"k8s progression", Config{K8sProgression: true, K8sPort: "0"}, 1},
		{"both", Config{TickInterval: time.Second, K8sProgression: true, K8sPort: "0"}, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			cfg.Providers = []string{"aws"}
			cfg.Host = "127.0.0.1"
			cfg.Ports = map[string]string{"aws": "0"}

			app := newTestApp(t, cfg)
			if got := len(app.ticker.entries); got != tc.want {
				t.Fatalf("entries = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestServiceTickablesCoverEveryRegion checks a lazily built region's
// CloudWatch is ticked too, and that a reset hands over the new providers.
func TestServiceTickablesCoverEveryRegion(t *testing.T) {
	app := newTestApp(t, Config{Providers: []string{"aws"}, Host: "127.0.0.1", Ports: map[string]string{"aws": "0"}})

	west := app.awsMux.GetOrCreate("us-west-2")

	got := app.serviceTickables()
	if len(got) != 2 {
		t.Fatalf("tickables = %d, want 2 (default region and us-west-2)", len(got))
	}

	found := false

	for _, tk := range got {
		if tk == config.Tickable(west.CloudWatch) {
			found = true
		}
	}

	if !found {
		t.Fatal("us-west-2 CloudWatch is not ticked")
	}

	app.Rebuild()

	if got := app.serviceTickables(); len(got) != 1 || got[0] == config.Tickable(west.CloudWatch) {
		t.Fatalf("after reset tickables = %v, want only the new default region", got)
	}
}
