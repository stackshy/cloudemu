package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// newHashTable creates a single-partition-key table (no sort key), the shape a
// put-if-absent / optimistic-lock test needs.
func newHashTable(m *Mock, name string) {
	_ = m.CreateTable(context.Background(), driver.TableConfig{Name: name, PartitionKey: "pk"})
}

// TestPutItemConditionalPutIfAbsentAtomic proves that attribute_not_exists
// put-if-absent is atomic: when N goroutines race to create the SAME key, EXACTLY
// one succeeds and every other gets ConditionalCheckFailed. The race is LOGICAL
// (each op is individually mutex-guarded, so -race alone stays silent), so the
// winners==1 invariant is asserted across many high-contention rounds.
func TestPutItemConditionalPutIfAbsentAtomic(t *testing.T) {
	const (
		rounds     = 300
		goroutines = 8
	)

	m := newTestMock()
	newHashTable(m, "t")

	ctx := context.Background()
	cond := driver.Condition{Expression: "attribute_not_exists(pk)"}

	for r := 0; r < rounds; r++ {
		pk := fmt.Sprintf("k-%d", r)

		var (
			winners atomic.Int64
			wg      sync.WaitGroup
		)

		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()

				item := map[string]any{"pk": pk, "v": expr.Number("1")}

				_, err := m.PutItemConditional(ctx, "t", item, cond)
				if err == nil {
					winners.Add(1)
					return
				}

				var ccf *driver.ConditionalCheckFailed
				if !errors.As(err, &ccf) {
					t.Errorf("round %d: unexpected error: %v", r, err)
				}
			}()
		}

		wg.Wait()

		if got := winners.Load(); got != 1 {
			t.Fatalf("round %d: put-if-absent not atomic: %d writers succeeded, want exactly 1", r, got)
		}
	}
}

// TestUpdateItemConditionalOptimisticLockAtomic proves optimistic locking is
// atomic: many goroutines race to bump a version-guarded item; exactly one wins
// per version and the final version equals the number of rounds (no lost update,
// no double-advance).
func TestUpdateItemConditionalOptimisticLockAtomic(t *testing.T) {
	const (
		rounds     = 200
		goroutines = 8
	)

	m := newTestMock()
	newHashTable(m, "t")

	ctx := context.Background()
	if err := m.PutItem(ctx, "t", map[string]any{"pk": "x", "ver": expr.Number("0")}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for r := 0; r < rounds; r++ {
		values := map[string]any{
			":cur": expr.Number(fmt.Sprintf("%d", r)),
			":new": expr.Number(fmt.Sprintf("%d", r+1)),
		}
		input := driver.UpdateItemInput{
			Table:            "t",
			Key:              map[string]any{"pk": "x"},
			UpdateExpression: "SET ver = :new",
			ExprValues:       values,
		}
		cond := driver.Condition{Expression: "ver = :cur", Values: values}

		var (
			winners atomic.Int64
			wg      sync.WaitGroup
		)

		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()

				_, _, err := m.UpdateItemConditional(ctx, input, cond)
				if err == nil {
					winners.Add(1)
					return
				}

				var ccf *driver.ConditionalCheckFailed
				if !errors.As(err, &ccf) {
					t.Errorf("round %d: unexpected error: %v", r, err)
				}
			}()
		}

		wg.Wait()

		if got := winners.Load(); got != 1 {
			t.Fatalf("round %d: optimistic update not atomic: %d writers won, want exactly 1", r, got)
		}
	}

	final, err := m.GetItem(ctx, "t", map[string]any{"pk": "x"})
	if err != nil {
		t.Fatalf("final read: %v", err)
	}

	if got := final["ver"]; got != expr.Number(fmt.Sprintf("%d", rounds)) {
		t.Fatalf("final version = %v, want %d (a lost update or double-advance occurred)", got, rounds)
	}
}

// TestTransactWriteAllOrNothing proves a transaction is all-or-nothing: one
// failing ConditionCheck cancels every write in the transaction, and the
// CancellationReasons name the exact failing operation.
func TestTransactWriteAllOrNothing(t *testing.T) {
	m := newTestMock()
	newHashTable(m, "t")

	ctx := context.Background()
	if err := m.PutItem(ctx, "t", map[string]any{"pk": "exists"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ops := []driver.TransactOp{
		{Kind: driver.TransactPut, Table: "t", Item: map[string]any{"pk": "new1"}},
		{Kind: driver.TransactPut, Table: "t", Item: map[string]any{"pk": "exists"},
			Condition: driver.Condition{Expression: "attribute_not_exists(pk)"}},
		{Kind: driver.TransactPut, Table: "t", Item: map[string]any{"pk": "new2"}},
	}

	err := m.TransactWrite(ctx, ops)

	var canceled *driver.TransactionCanceled
	if !errors.As(err, &canceled) {
		t.Fatalf("expected TransactionCanceled, got %v", err)
	}

	if len(canceled.FailedConditions) != 1 || canceled.FailedConditions[0] != 1 {
		t.Fatalf("FailedConditions = %v, want [1]", canceled.FailedConditions)
	}

	// The two unconditional puts must NOT have been applied.
	for _, pk := range []string{"new1", "new2"} {
		if _, gerr := m.GetItem(ctx, "t", map[string]any{"pk": pk}); gerr == nil {
			t.Fatalf("op wrote %q despite cancellation — transaction was not all-or-nothing", pk)
		}
	}
}

// TestTransactWriteConcurrentConflict proves two conflicting transactions cannot
// both commit: N goroutines each run a put-if-absent transaction on the same key,
// and exactly one commits per round.
func TestTransactWriteConcurrentConflict(t *testing.T) {
	const (
		rounds     = 300
		goroutines = 8
	)

	m := newTestMock()
	newHashTable(m, "t")

	ctx := context.Background()

	for r := 0; r < rounds; r++ {
		pk := fmt.Sprintf("k-%d", r)

		var (
			winners atomic.Int64
			wg      sync.WaitGroup
		)

		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()

				ops := []driver.TransactOp{{
					Kind: driver.TransactPut, Table: "t",
					Item:      map[string]any{"pk": pk},
					Condition: driver.Condition{Expression: "attribute_not_exists(pk)"},
				}}

				err := m.TransactWrite(ctx, ops)
				if err == nil {
					winners.Add(1)
					return
				}

				var canceled *driver.TransactionCanceled
				if !errors.As(err, &canceled) {
					t.Errorf("round %d: unexpected error: %v", r, err)
				}
			}()
		}

		wg.Wait()

		if got := winners.Load(); got != 1 {
			t.Fatalf("round %d: conflicting transactions not isolated: %d committed, want exactly 1", r, got)
		}
	}
}
