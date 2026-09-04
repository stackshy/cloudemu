package dynamodb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
	"github.com/stackshy/cloudemu/v2/services/database/driver/expr"
)

// transactIdempotencyWindow is how long a TransactWriteItems ClientRequestToken
// is remembered so a retry with the same token is treated as a replay rather
// than a second application (real DynamoDB uses ~10 minutes).
const transactIdempotencyWindow = 10 * time.Minute

// txIdempotencyRecord is a remembered ClientRequestToken: the fingerprint of the
// request it first committed and when, so a replay with a matching body is a
// no-op while a different body with the same token is a parameter mismatch.
type txIdempotencyRecord struct {
	fingerprint string
	storedAt    time.Time
}

// This file holds the ATOMIC conditional-write and transaction primitives. The
// wire handler must NOT evaluate a ConditionExpression with a standalone GetItem
// and then issue the mutation as a separate call — dropping the table lock in
// between opens a TOCTOU window where two concurrent conditional writes on one
// key both succeed. Each method here evaluates the condition and applies the
// write under a SINGLE hold of m.mu, so no concurrent writer can interleave.
//
// PutItem/DeleteItem/UpdateItem delegate to these with an empty Condition, so the
// unconditional and conditional paths share one implementation.

// evalCondition parses (reusing expr.ParseCondition — no duplicated parser) and
// evaluates a ConditionExpression against the current stored item. An empty
// expression always passes. A missing item is evaluated against an empty item so
// attribute_not_exists is true and attribute_exists is false, matching DynamoDB
// create-if-absent semantics.
func evalCondition(cond driver.Condition, existing map[string]any) (bool, error) {
	if strings.TrimSpace(cond.Expression) == "" {
		return true, nil
	}

	node, err := expr.ParseCondition(cond.Expression, cond.Names, cond.Values)
	if err != nil {
		return false, err
	}

	if existing == nil {
		existing = map[string]any{}
	}

	return expr.Eval(node, existing)
}

// condExisting returns the item to evaluate a condition against: the stored item
// when present, otherwise nil (evalCondition treats nil as an empty item).
func condExisting(item map[string]any, present bool) map[string]any {
	if !present {
		return nil
	}

	return item
}

// oldImage returns a defensive copy of the stored item for return to the caller
// (ReturnValues=ALL_OLD / the conflicting item on a failed condition), or nil
// when there was no stored item.
func oldImage(item map[string]any, present bool) map[string]any {
	if !present {
		return nil
	}

	return maps.Clone(item)
}

// checkConditionLocked evaluates cond against the current stored item, returning
// a *driver.ConditionalCheckFailed (carrying the conflicting item) on a failed
// condition, the parse error on a malformed expression, or nil when it passes.
// Caller must hold m.mu.
func checkConditionLocked(cond driver.Condition, item map[string]any, present bool) error {
	ok, err := evalCondition(cond, condExisting(item, present))
	if err != nil {
		return err
	}

	if !ok {
		return &driver.ConditionalCheckFailed{Item: oldImage(item, present)}
	}

	return nil
}

// emitWriteMetrics pushes the per-write CloudWatch metrics shared by every
// mutating operation.
func (m *Mock) emitWriteMetrics(table string) {
	dims := map[string]string{"TableName": table}
	m.emitMetric("ConsumedWriteCapacityUnits", 1, dims)
	m.emitMetric("SuccessfulRequestCount", 1, dims)
}

// PutItemConditional writes item only if cond passes, evaluating the condition
// and the write atomically under m.mu. It returns the previous stored item (nil
// if none) for ReturnValues=ALL_OLD, or a *driver.ConditionalCheckFailed when the
// condition fails.
func (m *Mock) PutItemConditional(
	ctx context.Context, table string, item map[string]any, cond driver.Condition,
) (map[string]any, error) {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if err := validateItemKeys(td.config, item); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	if err := validateItemSize(item); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	key := itemKey(td.config, item)
	oldItem, hadOld := td.items.Get(key)

	if err := checkConditionLocked(cond, oldItem, hadOld); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	stored := maps.Clone(item)
	td.items.Set(key, stored)
	m.recordStreamEvent(td, oldItem, stored, hadOld)
	m.mu.Unlock()
	m.flushStreamDeliveries(ctx)

	m.emitWriteMetrics(table)

	return oldImage(oldItem, hadOld), nil
}

// DeleteItemConditional deletes the item at key only if cond passes, evaluating
// the condition and the delete atomically under m.mu. It returns the deleted
// item (nil if none) for ReturnValues=ALL_OLD, or a *driver.ConditionalCheckFailed
// when the condition fails.
func (m *Mock) DeleteItemConditional(
	ctx context.Context, table string, key map[string]any, cond driver.Condition,
) (map[string]any, error) {
	m.mu.Lock()

	td, exists := m.tables[table]
	if !exists {
		m.mu.Unlock()
		return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", table)
	}

	if err := validateKeySchema(td.config, key); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	k := itemKey(td.config, key)
	oldItem, hadOld := td.items.Get(k)

	if err := checkConditionLocked(cond, oldItem, hadOld); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	td.items.Delete(k)

	if hadOld {
		m.recordStreamRemove(td, oldItem)
	}

	m.mu.Unlock()
	m.flushStreamDeliveries(ctx)

	m.emitWriteMetrics(table)

	return oldImage(oldItem, hadOld), nil
}

// UpdateItemConditional applies input's update only if cond passes, evaluating
// the condition and the update atomically under m.mu (upserting a missing item
// exactly like UpdateItem). It returns the post-update item and the pre-update
// image (nil when the item was created), or a *driver.ConditionalCheckFailed when
// the condition fails.
//
//nolint:gocritic // hugeParam: input mirrors the driver's UpdateItem signature.
func (m *Mock) UpdateItemConditional(
	ctx context.Context, input driver.UpdateItemInput, cond driver.Condition,
) (updated, old map[string]any, err error) {
	m.mu.Lock()

	td, exists := m.tables[input.Table]
	if !exists {
		m.mu.Unlock()
		return nil, nil, cerrors.Newf(cerrors.NotFound, "table %s not found", input.Table)
	}

	if verr := validateKeySchema(td.config, input.Key); verr != nil {
		m.mu.Unlock()
		return nil, nil, verr
	}

	k := itemKey(td.config, input.Key)
	item, ok := td.items.Get(k)

	if cerr := checkConditionLocked(cond, item, ok); cerr != nil {
		m.mu.Unlock()
		return nil, nil, cerr
	}

	base, oldItem := updateBaseImage(item, ok, input.Key)

	result, aerr := driver.ApplyUpdate(base, input)
	if aerr != nil {
		m.mu.Unlock()
		return nil, nil, aerr
	}

	if verr := validateItemSize(result); verr != nil {
		m.mu.Unlock()
		return nil, nil, verr
	}

	td.items.Set(k, result)
	m.recordStreamEvent(td, oldItem, result, true)
	m.mu.Unlock()
	m.flushStreamDeliveries(ctx)

	m.emitWriteMetrics(input.Table)

	return maps.Clone(result), oldItem, nil
}

// updateBaseImage resolves the base item an UpdateExpression mutates and the
// pre-update image reported for ALL_OLD/UPDATED_OLD. A present item seeds both; a
// missing item upserts from the key attributes with no pre-image.
func updateBaseImage(item map[string]any, present bool, key map[string]any) (base, old map[string]any) {
	if present {
		return copyItem(item), copyItem(item)
	}

	return copyItem(key), nil
}

// TransactWrite executes an atomic TransactWriteItems: every ConditionExpression
// is evaluated, and only if all pass are all writes applied — the whole set under
// a single hold of m.mu, so the transaction is all-or-nothing and isolated from
// concurrent single-item writes. On any failed condition it returns a
// *driver.TransactionCanceled naming the failed operations and writes nothing.
func (m *Mock) TransactWrite(ctx context.Context, ops []driver.TransactOp, clientRequestToken string) error {
	m.mu.Lock()
	// flush registers before the unlock defer so it runs after the lock is
	// released (defers are LIFO), delivering stream records outside m.mu.
	defer func() { m.flushStreamDeliveries(ctx) }()
	defer m.mu.Unlock()

	if done, err := m.checkTransactToken(clientRequestToken, ops); done {
		return err
	}

	tds, err := m.resolveTransactTables(ops)
	if err != nil {
		return err
	}

	failed, err := evalTransactConditions(tds, ops)
	if err != nil {
		return err
	}

	if len(failed) > 0 {
		return &driver.TransactionCanceled{FailedConditions: failed}
	}

	plans, err := planTransactMutations(tds, ops)
	if err != nil {
		return err
	}

	m.commitTransactMutations(plans)
	m.rememberTransactToken(clientRequestToken, ops)

	return nil
}

// checkTransactToken applies TransactWriteItems idempotency. A replay carrying a
// ClientRequestToken still inside the window short-circuits: done=true with a
// nil error returns the cached success without re-applying when the request body
// matches, or an *IdempotentParameterMismatch when the same token carries a
// different body. done=false means apply the transaction normally. Caller holds
// m.mu.
func (m *Mock) checkTransactToken(token string, ops []driver.TransactOp) (done bool, err error) {
	if token == "" {
		return false, nil
	}

	m.pruneTransactTokens()

	rec, ok := m.txIdempotency[token]
	if !ok {
		return false, nil
	}

	if rec.fingerprint != transactFingerprint(ops) {
		return true, &driver.IdempotentParameterMismatch{}
	}

	return true, nil
}

// rememberTransactToken records a committed transaction's token and fingerprint
// so a later retry is recognized as a replay. A no-op for an empty token. Caller
// holds m.mu.
func (m *Mock) rememberTransactToken(token string, ops []driver.TransactOp) {
	if token == "" {
		return
	}

	m.txIdempotency[token] = txIdempotencyRecord{
		fingerprint: transactFingerprint(ops),
		storedAt:    m.opts.Clock.Now(),
	}
}

// pruneTransactTokens drops tokens older than the idempotency window so the map
// does not grow without bound. Caller holds m.mu.
func (m *Mock) pruneTransactTokens() {
	now := m.opts.Clock.Now()
	for tok, rec := range m.txIdempotency {
		if now.Sub(rec.storedAt) > transactIdempotencyWindow {
			delete(m.txIdempotency, tok)
		}
	}
}

// transactFingerprint is a stable digest of a transaction's operations, letting
// a ClientRequestToken replay tell an identical request (idempotent no-op) from
// a different one (parameter mismatch). json.Marshal sorts map keys, so the
// digest is deterministic across replays.
func transactFingerprint(ops []driver.TransactOp) string {
	b, err := json.Marshal(ops)
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// resolveTransactTables resolves and structurally validates every operation's
// target before any condition is evaluated or any write applied. A missing table
// is a ResourceNotFound (not a cancellation), matching real DynamoDB. Caller must
// hold m.mu.
func (m *Mock) resolveTransactTables(ops []driver.TransactOp) ([]*tableData, error) {
	tds := make([]*tableData, len(ops))

	for i := range ops {
		td, exists := m.tables[ops[i].Table]
		if !exists {
			return nil, cerrors.Newf(cerrors.NotFound, "table %s not found", ops[i].Table)
		}

		if err := validateTransactOp(td, &ops[i]); err != nil {
			return nil, err
		}

		tds[i] = td
	}

	return tds, nil
}

// validateTransactOp enforces the per-operation structural rules (a Put carries a
// valid, sized item; the keyed operations carry a non-empty key) before the
// transaction commits anything.
func validateTransactOp(td *tableData, op *driver.TransactOp) error {
	if op.Kind == driver.TransactPut {
		if err := validateItemKeys(td.config, op.Item); err != nil {
			return err
		}

		return validateItemSize(op.Item)
	}

	return validateKeySchema(td.config, op.Key)
}

// opKeySource returns the attributes that identify an operation's target item:
// the full item for a Put, the Key for the others.
func opKeySource(op *driver.TransactOp) map[string]any {
	if op.Kind == driver.TransactPut {
		return op.Item
	}

	return op.Key
}

// evalTransactConditions evaluates every operation's ConditionExpression against
// current state, returning the indices (request order) whose condition failed. A
// malformed expression surfaces as a non-nil error. Caller must hold m.mu.
func evalTransactConditions(tds []*tableData, ops []driver.TransactOp) ([]int, error) {
	var failed []int

	for i := range ops {
		td := tds[i]
		cur, had := td.items.Get(itemKey(td.config, opKeySource(&ops[i])))

		ok, err := evalCondition(ops[i].Condition, condExisting(cur, had))
		if err != nil {
			return nil, err
		}

		if !ok {
			failed = append(failed, i)
		}
	}

	return failed, nil
}

// txMutation is one planned, condition-passed transaction write, computed before
// anything is committed so a compute error (e.g. an oversized update) aborts the
// whole transaction with nothing written.
type txMutation struct {
	td       *tableData
	key      string
	newItem  map[string]any
	oldItem  map[string]any
	hadOld   bool
	isDelete bool
	isNoop   bool // ConditionCheck: asserts a condition, writes nothing
}

// planTransactMutations computes the resulting write for every operation without
// applying any, so a compute/validation error leaves the store untouched. Caller
// must hold m.mu.
func planTransactMutations(tds []*tableData, ops []driver.TransactOp) ([]txMutation, error) {
	plans := make([]txMutation, len(ops))

	for i := range ops {
		plan, err := planTransactOp(tds[i], &ops[i])
		if err != nil {
			return nil, err
		}

		plans[i] = plan
	}

	return plans, nil
}

// planTransactOp computes a single operation's write against current state.
func planTransactOp(td *tableData, op *driver.TransactOp) (txMutation, error) {
	switch op.Kind {
	case driver.TransactPut:
		key := itemKey(td.config, op.Item)
		old, had := td.items.Get(key)

		return txMutation{td: td, key: key, newItem: maps.Clone(op.Item), oldItem: old, hadOld: had}, nil
	case driver.TransactDelete:
		key := itemKey(td.config, op.Key)
		old, had := td.items.Get(key)

		return txMutation{td: td, key: key, oldItem: old, hadOld: had, isDelete: true}, nil
	case driver.TransactUpdate:
		return planTransactUpdate(td, op)
	default: // ConditionCheck
		return txMutation{isNoop: true}, nil
	}
}

// planTransactUpdate runs the UpdateExpression against the current (or upserted)
// item, validating the result size, and returns the staged write.
func planTransactUpdate(td *tableData, op *driver.TransactOp) (txMutation, error) {
	key := itemKey(td.config, op.Key)
	item, ok := td.items.Get(key)
	base, oldItem := updateBaseImage(item, ok, op.Key)

	result, err := driver.ApplyUpdate(base, driver.UpdateItemInput{
		Table:            op.Table,
		Key:              op.Key,
		UpdateExpression: op.UpdateExpression,
		ExprNames:        op.ExprNames,
		ExprValues:       op.ExprValues,
	})
	if err != nil {
		return txMutation{}, err
	}

	if err := validateItemSize(result); err != nil {
		return txMutation{}, err
	}

	return txMutation{td: td, key: key, newItem: result, oldItem: oldItem, hadOld: true}, nil
}

// commitTransactMutations applies every staged write and records its stream
// event. Conditions and computes have already succeeded, so this cannot fail.
// Caller must hold m.mu.
func (m *Mock) commitTransactMutations(plans []txMutation) {
	for i := range plans {
		p := plans[i]

		switch {
		case p.isNoop:
			continue
		case p.isDelete:
			p.td.items.Delete(p.key)

			if p.hadOld {
				m.recordStreamRemove(p.td, p.oldItem)
			}
		default:
			p.td.items.Set(p.key, p.newItem)
			m.recordStreamEvent(p.td, p.oldItem, p.newItem, p.hadOld)
		}
	}
}
