package cloudformation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// blockProv is slowProv whose Create of "slow" blocks until release closes.
type blockProv struct {
	slowProv
	started chan struct{}
	release chan struct{}
}

func newBlockProv() blockProv {
	return blockProv{slowProv: newSlowProv(), started: make(chan struct{}), release: make(chan struct{})}
}

func (p blockProv) Create(ctx context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	if cfn.PropString(req.Properties, "Name") == "slow" {
		close(p.started)
		<-p.release
	}

	return p.slowProv.Create(ctx, req)
}

func newBlockMock(p blockProv, async bool) (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	opts := []config.Option{config.WithClock(fc), config.WithRegion("us-east-1"), config.WithAccountID("123456789012")}
	if async {
		opts = append(opts, config.WithAsyncSettle())
	}

	m := New(config.NewOptions(opts...))
	m.SetRegistry(cfn.Registry{"Test::Slow": p})

	return m, fc
}

// waitBlocked reports whether done stays open for a while, that is the call
// behind it is held back.
func waitBlocked(done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	case <-time.After(50 * time.Millisecond):
		return true
	}
}

// A DeleteStack or Snapshot that arrives while an update is still applying
// waits for it, in both modes. Synchronously the delete then runs on the
// finished update. Under AsyncSettle the update is then pending, and the
// delete is refused instead of being lost.
func TestDeleteAndSnapshotWaitForAnApplyingUpdate(t *testing.T) {
	for _, async := range []bool{false, true} {
		p := newBlockProv()
		m, fc := newBlockMock(p, async)
		ctx := context.Background()

		_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
		requireNoError(t, err)
		fc.Advance(settled)
		m.Tick(fc.Now())

		updated := make(chan struct{})

		go func() {
			defer close(updated)

			_, uerr := m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a", "slow")})
			requireNoError(t, uerr)
		}()

		<-p.started

		var derr error

		deleted, snapped := make(chan struct{}), make(chan struct{})

		go func() { defer close(deleted); derr = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}) }()
		go func() { defer close(snapped); _, _ = m.Snapshot(ctx, false) }()

		if !waitBlocked(deleted) || !waitBlocked(snapped) {
			t.Fatalf("async=%v: delete or snapshot ran inside the update", async)
		}

		close(p.release)
		<-updated
		<-deleted
		<-snapped

		if async {
			if derr == nil || !strings.Contains(derr.Error(), cfn.StatusUpdateInProgress) {
				t.Fatalf("async delete of an updating stack: %v", derr)
			}

			fc.Advance(settled)
			requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
			fc.Advance(settled)
			m.Tick(fc.Now())
		} else {
			requireNoError(t, derr)
		}

		assertEqual(t, strings.Join(stackStatuses(t, m, "s")[2:], ","),
			"UPDATE_IN_PROGRESS,UPDATE_COMPLETE_CLEANUP_IN_PROGRESS,UPDATE_COMPLETE,DELETE_IN_PROGRESS,DELETE_COMPLETE",
			"history")
		assertDeletedOnce(t, p.slowProv, "a", "slow")
	}
}

// Snapshot copies every slice it marshals, so it can run beside a cancel,
// which drops events in place, and a delete, which drops resource rows.
func TestSnapshotRacesWithCancelAndDelete(t *testing.T) {
	for range 10 {
		p := newSlowProv()
		m, fc := newSlowAsyncMock(p)
		ctx := context.Background()

		_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a", "b")})
		requireNoError(t, err)
		fc.Advance(settled)

		_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a2", "b")})
		requireNoError(t, err)

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()

			for range 20 {
				_, _ = m.Snapshot(ctx, false)
			}
		}()

		go func() {
			defer wg.Done()

			_ = m.CancelUpdateStack(ctx, &cfn.CancelUpdateStackInput{StackName: "s"})
			fc.Advance(settled)
			m.Tick(fc.Now())
			_ = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"})
			fc.Advance(settled)
			m.Tick(fc.Now())
		}()

		wg.Wait()
		assertDeletedOnce(t, p, "a", "b", "a2")
	}
}
