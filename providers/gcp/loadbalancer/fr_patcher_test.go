package loadbalancer

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

const frLabelTag = "cloudemu:gcpFrLabels"

func seedFR(t *testing.T, m *Mock, name string) string {
	t.Helper()

	lb, err := m.CreateLoadBalancer(context.Background(), driver.LBConfig{
		Name: name, Type: "network", Tags: map[string]string{frLabelTag: `{"a":"b"}`},
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}

	return lb.ARN
}

func storedTag(t *testing.T, m *Mock, arn, key string) string {
	t.Helper()

	lbs, err := m.DescribeLoadBalancers(context.Background(), []string{arn})
	if err != nil || len(lbs) != 1 {
		t.Fatalf("DescribeLoadBalancers: %v (%d)", err, len(lbs))
	}

	return lbs[0].Tags[key]
}

func TestPatchGCPForwardingRule(t *testing.T) {
	errBoom := errors.New("boom")

	tests := []struct {
		name     string
		target   string
		mutate   func(*driver.LBInfo) error
		wantErr  func(error) bool
		wantTags string
	}{
		{
			name:   "applies mutation",
			target: "fr",
			mutate: func(lb *driver.LBInfo) error {
				lb.Tags[frLabelTag] = `{"c":"d"}`
				return nil
			},
			wantErr:  func(err error) bool { return err == nil },
			wantTags: `{"c":"d"}`,
		},
		{
			name:   "error leaves record unchanged",
			target: "fr",
			mutate: func(lb *driver.LBInfo) error {
				lb.Tags[frLabelTag] = `{"x":"y"}`
				return errBoom
			},
			wantErr:  func(err error) bool { return errors.Is(err, errBoom) },
			wantTags: `{"a":"b"}`,
		},
		{
			name:     "missing rule is NotFound",
			target:   "nope",
			mutate:   func(*driver.LBInfo) error { return nil },
			wantErr:  cerrors.IsNotFound,
			wantTags: `{"a":"b"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMock()
			arn := seedFR(t, m, "fr")

			err := m.PatchGCPForwardingRule(context.Background(), tc.target, tc.mutate)
			if !tc.wantErr(err) {
				t.Fatalf("err = %v", err)
			}

			if got := storedTag(t, m, arn, frLabelTag); got != tc.wantTags {
				t.Fatalf("labels tag = %q, want %q", got, tc.wantTags)
			}
		})
	}
}

// TestPatchGCPForwardingRuleCopiesTags proves a Tags map retained from a mutate
// call does not alias the stored record.
func TestPatchGCPForwardingRuleCopiesTags(t *testing.T) {
	m := newTestMock()
	arn := seedFR(t, m, "fr")

	var kept map[string]string

	if err := m.PatchGCPForwardingRule(context.Background(), "fr", func(lb *driver.LBInfo) error {
		kept = lb.Tags
		return errors.New("rollback")
	}); err == nil {
		t.Fatal("want the mutate error back")
	}

	kept[frLabelTag] = "mutated"

	if got := storedTag(t, m, arn, frLabelTag); got != `{"a":"b"}` {
		t.Fatalf("stored tag = %q, aliased by the mutate copy", got)
	}
}

// TestPatchGCPForwardingRuleConcurrent checks no update is lost when patches
// overlap.
func TestPatchGCPForwardingRuleConcurrent(t *testing.T) {
	const workers = 8

	m := newTestMock()
	arn := seedFR(t, m, "fr")

	var wg sync.WaitGroup

	for range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = m.PatchGCPForwardingRule(context.Background(), "fr", func(lb *driver.LBInfo) error {
				n, _ := strconv.Atoi(lb.Tags["gen"])
				lb.Tags["gen"] = strconv.Itoa(n + 1)

				return nil
			})
		}()
	}

	wg.Wait()

	if got := storedTag(t, m, arn, "gen"); got != strconv.Itoa(workers) {
		t.Fatalf("gen = %s, want %d", got, workers)
	}
}

// TestPatchedForwardingRuleSurvivesSnapshot proves patched tags persist.
func TestPatchedForwardingRuleSurvivesSnapshot(t *testing.T) {
	ctx := context.Background()
	src := newTestMock()
	arn := seedFR(t, src, "fr")

	if err := src.PatchGCPForwardingRule(ctx, "fr", func(lb *driver.LBInfo) error {
		lb.Tags[frLabelTag] = `{"env":"prod"}`
		return nil
	}); err != nil {
		t.Fatalf("patch: %v", err)
	}

	data, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := newTestMock()
	if err := dst.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := storedTag(t, dst, arn, frLabelTag); got != `{"env":"prod"}` {
		t.Fatalf("restored labels = %q", got)
	}
}
