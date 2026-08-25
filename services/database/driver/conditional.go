package driver

// This file defines the shared types for ATOMIC conditional writes and
// transactions. A DynamoDB ConditionExpression must be evaluated and the write
// applied under a single hold of the provider's table lock; evaluating it in the
// wire layer with a separate GetItem (check) followed by a separate mutation
// (act) leaves a TOCTOU window in which two concurrent conditional writes on one
// key can both succeed. These types carry the raw condition down to the provider
// so the check and the write are one atomic step.
//
// They are consumed by the AWS DynamoDB wire handler and the AWS provider via an
// optional, type-asserted capability (like UpdateThroughput / SetPITR) and are
// deliberately kept off the cross-cloud Database interface.

// Condition is a DynamoDB ConditionExpression carried to the provider so it can
// parse (via expr.ParseCondition) and evaluate the condition against the current
// stored item while holding the table lock, making the check and the subsequent
// write atomic. An empty Expression is an unconditional write (always passes).
// Values are already decoded to native Go (expr.Number, []byte, nested maps),
// matching the stored item shape the evaluator compares against.
type Condition struct {
	Expression string
	Names      map[string]string
	Values     map[string]any
}

// TransactOp kinds, mirroring the four DynamoDB TransactWriteItems actions.
const (
	TransactPut            = "Put"
	TransactDelete         = "Delete"
	TransactUpdate         = "Update"
	TransactConditionCheck = "ConditionCheck"
)

// TransactOp is one operation in an atomic TransactWriteItems. Kind selects the
// mutation. Condition (optional) is evaluated against the current item before
// ANY op is applied — all conditions are checked, then all writes applied, under
// a single lock hold, so the transaction is all-or-nothing. For a Put, Item
// carries the full item; for the others Key identifies the target and
// UpdateExpression/ExprNames/ExprValues drive an Update. A ConditionCheck asserts
// its Condition but writes nothing.
type TransactOp struct {
	Kind             string
	Table            string
	Item             map[string]any
	Key              map[string]any
	Condition        Condition
	UpdateExpression string
	ExprNames        map[string]string
	ExprValues       map[string]any
}

// ConditionalCheckFailed is returned by a conditional write whose
// ConditionExpression evaluated to false. Item carries the conflicting stored
// item (nil when the item was absent) so the wire layer can satisfy
// ReturnValuesOnConditionCheckFailure=ALL_OLD without a second, racy lookup.
type ConditionalCheckFailed struct {
	Item map[string]any
}

// Error reports the exact DynamoDB message a ConditionalCheckFailedException
// carries.
func (*ConditionalCheckFailed) Error() string { return "The conditional request failed" }

// TransactionCanceled is returned by an atomic transaction when one or more
// ConditionExpressions evaluated to false. FailedConditions holds the indices
// (in request order) of the operations whose condition failed; the wire layer
// maps them onto the per-operation CancellationReasons array (ConditionalCheckFailed
// for those indices, None for the rest). No write is applied when this is returned.
type TransactionCanceled struct {
	FailedConditions []int
}

// Error describes the transaction cancellation. The verbatim DynamoDB wire
// message is written by the wire layer, not here.
func (*TransactionCanceled) Error() string {
	return "transaction canceled: one or more condition checks failed"
}
