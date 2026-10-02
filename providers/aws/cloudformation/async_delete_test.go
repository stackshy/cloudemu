package cloudformation

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	cfn "github.com/stackshy/cloudemu/v2/services/cloudformation"
)

// slowProv is a concurrency-safe fake whose Delete takes a while, so a
// delete and a settling phase can overlap. Name replaces the resource.
type slowProv struct {
	mu      *sync.Mutex
	live    map[string]bool
	deletes map[string]int
}

func newSlowProv() slowProv {
	return slowProv{mu: &sync.Mutex{}, live: map[string]bool{}, deletes: map[string]int{}}
}

func (p slowProv) Create(_ context.Context, req cfn.ResourceRequest) (*cfn.ProvisionedResource, error) {
	name := cfn.PropString(req.Properties, "Name")

	p.mu.Lock()
	defer p.mu.Unlock()

	p.live[name] = true

	return &cfn.ProvisionedResource{PhysicalID: name}, nil
}

func (p slowProv) Delete(_ context.Context, physicalID string, _ map[string]any) error {
	time.Sleep(time.Millisecond)

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.live[physicalID] {
		return cerrors.Newf(cerrors.NotFound, "%s not found", physicalID)
	}

	delete(p.live, physicalID)
	p.deletes[physicalID]++

	return nil
}

func (slowProv) RequiresReplacement(property string) bool { return property == "Name" }

func (p slowProv) state() (live, deletes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	live = map[string]int{}
	for k := range p.live {
		live[k] = 1
	}

	deletes = map[string]int{}
	for k, v := range p.deletes {
		deletes[k] = v
	}

	return live, deletes
}

func newSlowAsyncMock(p slowProv) (*Mock, *config.FakeClock) {
	fc := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	m := New(config.NewOptions(config.WithClock(fc), config.WithRegion("us-east-1"),
		config.WithAccountID("123456789012"), config.WithAsyncSettle()))
	m.SetRegistry(cfn.Registry{"Test::Slow": p})

	return m, fc
}

func slowTemplate(names ...string) string {
	body := `{"Resources":{`

	for i, n := range names {
		if i > 0 {
			body += ","
		}

		body += `"R` + string(rune('A'+i)) + `":{"Type":"Test::Slow","Properties":{"Name":"` + n + `"}}`
	}

	return body + `}}`
}

// stackStatuses lists the stack-level statuses of the stack's events, oldest
// first.
func stackStatuses(t *testing.T, m *Mock, name string) []string {
	t.Helper()

	events, err := m.DescribeStackEvents(context.Background(), name)
	requireNoError(t, err)

	var out []string

	for i := len(events) - 1; i >= 0; i-- {
		if events[i].LogicalID == name && events[i].ResourceType == stackResourceType {
			out = append(out, events[i].Status)
		}
	}

	return out
}

func assertDeletedOnce(t *testing.T, p slowProv, names ...string) {
	t.Helper()

	live, deletes := p.state()
	if len(live) != 0 {
		t.Fatalf("resources left after the delete: %v", live)
	}

	for _, n := range names {
		if deletes[n] != 1 {
			t.Fatalf("%s deleted %d times, want once (%v)", n, deletes[n], deletes)
		}
	}
}

// A DeleteStack that lands while the ticker finishes an update's cleanup
// waits for it. The stack's history never goes back from a delete to the
// update, and each resource is deleted once.
func TestDeleteStackDuringSettleDoesNotInterleave(t *testing.T) {
	for range 20 {
		p := newSlowProv()
		m, fc := newSlowAsyncMock(p)
		ctx := context.Background()

		_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a", "b")})
		requireNoError(t, err)
		fc.Advance(settled)

		_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a2", "b2")})
		requireNoError(t, err)

		now := fc.Now().Add(settled)
		fc.Set(now)

		var (
			wg   sync.WaitGroup
			derr error
		)

		wg.Add(2)

		go func() { defer wg.Done(); m.Tick(now) }()
		go func() { defer wg.Done(); derr = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}) }()

		wg.Wait()

		if derr != nil && !cerrors.IsInvalidArgument(derr) {
			t.Fatalf("DeleteStack: %v", derr)
		}

		if derr != nil {
			requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
		}

		fc.Advance(settled)
		m.Tick(fc.Now())

		statuses := stackStatuses(t, m, "s")
		deleting := false

		for _, s := range statuses {
			if s == cfn.StatusDeleteInProgress {
				deleting = true
			} else if deleting && s != cfn.StatusDeleteComplete {
				t.Fatalf("status went back to %s after DELETE_IN_PROGRESS: %v", s, statuses)
			}
		}

		assertEqual(t, statuses[len(statuses)-1], cfn.StatusDeleteComplete, "final status")
		assertDeletedOnce(t, p, "a", "b", "a2", "b2")
	}
}

func TestDeleteStackWhileDeletingIsANoOp(t *testing.T) {
	p := newSlowProv()
	m, fc := newSlowAsyncMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
	requireNoError(t, err)
	fc.Advance(settled)

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusDeleteInProgress, "deleting")
	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))

	fc.Advance(settled)

	statuses := stackStatuses(t, m, "s")
	assertEqual(t, len(statuses), 4, "one delete in the history: "+strings.Join(statuses, ","))
	assertDeletedOnce(t, p, "a")
}

// DeleteStack cancels a create in progress: the resource the create was
// still making fails, and every resource it made is deleted once.
func TestDeleteStackCancelsACreateInProgress(t *testing.T) {
	p := newSlowProv()
	m, fc := newSlowAsyncMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a", "b")})
	requireNoError(t, err)

	// Only RA's CREATE_IN_PROGRESS has arrived.
	fc.Advance(stackEventStep)

	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
	assertEqual(t, stackStatus(t, m, "s").Status, cfn.StatusDeleteInProgress, "deleting")
	assertEqual(t, lastEventReason(t, m, "s", "RA", cfn.ResourceCreateFailed), reasonCreateCancelled, "cancelled resource")

	fc.Advance(settled)

	statuses := stackStatuses(t, m, "s")
	assertEqual(t, strings.Join(statuses, ","), "CREATE_IN_PROGRESS,DELETE_IN_PROGRESS,DELETE_COMPLETE", "history")
	assertDeletedOnce(t, p, "a", "b")
}

func TestDeleteStackRefusedDuringUpdate(t *testing.T) {
	p := newSlowProv()
	m, fc := newSlowAsyncMock(p)
	ctx := context.Background()

	_, err := m.CreateStack(ctx, &cfn.CreateStackInput{StackName: "s", TemplateBody: slowTemplate("a")})
	requireNoError(t, err)
	fc.Advance(settled)

	_, err = m.UpdateStack(ctx, &cfn.UpdateStackInput{StackName: "s", TemplateBody: slowTemplate("a2")})
	requireNoError(t, err)

	err = m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"})
	assertValidation(t, err, "Stack [s] cannot be deleted while in status UPDATE_IN_PROGRESS")

	fc.Advance(settled)
	requireNoError(t, m.DeleteStack(ctx, &cfn.DeleteStackInput{StackName: "s"}))
	fc.Advance(settled)
	m.Tick(fc.Now())
	assertDeletedOnce(t, p, "a", "a2")
}
