package asl

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

// defaultMaxSteps bounds the number of state transitions a single execution may
// make, so a Choice/Next cycle fails loudly instead of spinning forever.
const defaultMaxSteps = 10000

// handler evaluates one state. It returns the state's effective output, the name
// of the next state (empty when terminal), whether the execution terminates
// here (SUCCEEDED), or an error (mapped to ExecutionFailed). Each handler emits
// its own <Type>StateEntered/<Type>StateExited events; the walker emits the
// terminal ExecutionSucceeded/ExecutionFailed.
type handler func(ctx context.Context, it *interp, st *State, raw any) (out any, next string, terminal bool, err error)

// stateError is a run-time failure carrying the ASL error code and cause that
// surface on the ExecutionFailed event and the execution's Error/Cause.
type stateError struct {
	Code  string
	Cause string
}

func (e *stateError) Error() string { return e.Code + ": " + e.Cause }

// asStateError extracts a *stateError from err, mapping any other error to a
// generic States.Runtime failure.
func asStateError(err error) *stateError {
	var se *stateError
	if stderrors.As(err, &se) {
		return se
	}

	return &stateError{Code: "States.Runtime", Cause: err.Error()}
}

// RunInput carries the per-execution context the interpreter needs.
type RunInput struct {
	Input      string
	ExecArn    string
	ExecName   string
	SMArn      string
	SMName     string
	RoleArn    string
	StartTime  time.Time
	SettleBase time.Duration
	MaxSteps   int
}

// RunResult is the outcome of interpreting a definition against an input.
type RunResult struct {
	Status    string
	Output    string
	Error     string
	Cause     string
	History   []driver.HistoryEvent
	WaitTotal time.Duration
}

type interp struct {
	def       *StateMachineDef
	baseTime  time.Time
	offset    time.Duration
	waitTotal time.Duration
	steps     int
	maxSteps  int
	hist      []driver.HistoryEvent
	lastID    int64
	ctxObj    map[string]any
	handlers  map[string]handler
}

// Run interprets def against the execution input, returning the terminal status,
// output (or error/cause), the full event history, and the accumulated Wait
// duration (used to extend the settle window under AsyncSettle).
func Run(_ context.Context, def *StateMachineDef, in *RunInput) *RunResult {
	maxSteps := in.MaxSteps
	if maxSteps <= 0 {
		maxSteps = defaultMaxSteps
	}

	it := &interp{
		def:      def,
		baseTime: in.StartTime,
		maxSteps: maxSteps,
		handlers: buildHandlers(),
	}

	input := parseInput(in.Input)
	it.buildContext(in, input)

	// ExecutionStarted sits at the start instant; subsequent events sit at
	// baseTime+SettleBase (+Wait offsets), so the settle overlay hides them until
	// the execution's window elapses.
	it.emit(&driver.HistoryEvent{Type: "ExecutionStarted", Timestamp: in.StartTime, Input: emptyOr(in.Input)})
	it.offset = in.SettleBase

	res := it.walk(context.Background(), input)
	res.History = it.hist
	res.WaitTotal = it.waitTotal

	return res
}

func buildHandlers() map[string]handler {
	return map[string]handler{
		TypePass:     passHandler,
		TypeChoice:   choiceHandler,
		TypeWait:     waitHandler,
		TypeSucceed:  succeedHandler,
		TypeFail:     failHandler,
		TypeTask:     unsupportedHandler,
		TypeParallel: unsupportedHandler,
		TypeMap:      unsupportedHandler,
	}
}

// walk runs the state graph from StartAt following Next/End until a terminal
// state, an unrecovered error, or the step guard trips.
func (it *interp) walk(ctx context.Context, input any) *RunResult {
	cur := it.def.StartAt
	raw := input

	for {
		it.steps++
		if it.steps > it.maxSteps {
			return it.fail("CloudEmu.StateTransitionLimitExceeded",
				fmt.Sprintf("execution exceeded the %d state-transition limit", it.maxSteps))
		}

		st := it.def.States[cur]
		it.enterStateContext(st)

		out, next, terminal, err := it.handlers[st.Type](ctx, it, st, raw)
		if err != nil {
			se := asStateError(err)

			return it.fail(se.Code, se.Cause)
		}

		if terminal {
			return it.succeed(out)
		}

		raw = out
		cur = next
	}
}

func (it *interp) succeed(out any) *RunResult {
	s := toJSON(out)
	it.emit(&driver.HistoryEvent{Type: "ExecutionSucceeded", Output: s})

	return &RunResult{Status: driver.ExecStatusSucceeded, Output: s}
}

func (it *interp) fail(code, cause string) *RunResult {
	it.emit(&driver.HistoryEvent{Type: "ExecutionFailed", Error: code, Cause: cause})

	return &RunResult{Status: driver.ExecStatusFailed, Error: code, Cause: cause}
}

// emit assigns the monotonic ID, chains PreviousEventID, and fills the virtual
// timestamp (baseTime+offset) when the caller left it zero.
func (it *interp) emit(ev *driver.HistoryEvent) {
	it.lastID++
	ev.ID = it.lastID

	if it.lastID > 1 {
		ev.PreviousEventID = it.lastID - 1
	}

	if ev.Timestamp.IsZero() {
		ev.Timestamp = it.baseTime.Add(it.offset)
	}

	it.hist = append(it.hist, *ev)
}

func (it *interp) enterState(st *State, input any) {
	it.emit(&driver.HistoryEvent{Type: st.Type + "StateEntered", StateName: st.name, Input: toJSON(input)})
}

func (it *interp) exitState(st *State, output any) {
	it.emit(&driver.HistoryEvent{Type: st.Type + "StateExited", StateName: st.name, Output: toJSON(output)})
}

func (it *interp) buildContext(in *RunInput, input any) {
	it.ctxObj = map[string]any{
		"Execution": map[string]any{
			"Id": in.ExecArn, "Input": input, "Name": in.ExecName,
			"RoleArn": in.RoleArn, "StartTime": in.StartTime.UTC().Format(time.RFC3339),
		},
		"State":        map[string]any{"Name": "", "EnteredTime": ""},
		"StateMachine": map[string]any{"Id": in.SMArn, "Name": in.SMName},
	}
}

func (it *interp) enterStateContext(st *State) {
	sc, _ := it.ctxObj["State"].(map[string]any)
	if sc == nil {
		return
	}

	sc["Name"] = st.name
	sc["EnteredTime"] = it.baseTime.Add(it.offset).UTC().Format(time.RFC3339)
}

// resolvePath evaluates a JSONPath against the state input, or against the $$
// context object when the path is $$-prefixed.
func (it *interp) resolvePath(path string, input any) (value any, present bool, err error) {
	if strings.HasPrefix(path, "$$") {
		return evalPath("$"+path[2:], it.ctxObj)
	}

	return evalPath(path, input)
}

func parseInput(s string) any {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}
	}

	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return map[string]any{}
	}

	return v
}

func emptyOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return emptyObject
	}

	return s
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return emptyObject
	}

	return string(b)
}
