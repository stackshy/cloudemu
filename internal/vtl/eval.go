package vtl

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
)

// Execution limits.
const (
	// DefaultMaxSteps bounds the nodes and expressions one render may evaluate.
	DefaultMaxSteps = 1_000_000
	// MaxForeachIterations caps every #foreach loop, as AWS does; the loop
	// silently stops after this many iterations.
	MaxForeachIterations = 1000
	// ctxCheckEvery is how often (in steps) the context deadline is checked.
	ctxCheckEvery = 64
)

// renderSlots bounds concurrent renders to the CPU count. A host Object must
// not render another template from inside a render, or it could wait on a
// slot its own caller holds.
var renderSlots = make(chan struct{}, runtime.GOMAXPROCS(0)) //nolint:gochecknoglobals // process-wide limit

// ErrStepBudget is returned when a render exceeds its step budget.
var ErrStepBudget = errors.New("vtl: template exceeded its execution step budget")

// Result is the outcome of a render.
type Result struct {
	// Output is the rendered text.
	Output string
	// Returned reports that the template ran #return.
	Returned bool
	// ReturnValue is the #return argument, nil when none was given.
	ReturnValue any
}

// RenderOptions tunes a render. A zero MaxSteps selects DefaultMaxSteps.
type RenderOptions struct {
	MaxSteps int
}

// Render evaluates the template with vars as its top-level references. vars is
// modified by #set. Rendering stops with ctx's error when ctx is done, and
// with a limit error when the output, memory or step budget is exhausted.
func (t *Template) Render(ctx context.Context, vars map[string]any, opts RenderOptions) (*Result, error) {
	if vars == nil {
		vars = map[string]any{}
	}

	// Bound how many renders run at once, so concurrent requests cannot
	// multiply the per-render memory budget.
	select {
	case renderSlots <- struct{}{}:
		defer func() { <-renderSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	mem := newSharedBudget()
	defer mem.release()

	st := newState(ctx, vars, opts.MaxSteps, mem)
	if st.maxSteps <= 0 {
		st.maxSteps = DefaultMaxSteps
	}

	err := st.run(t.body)

	var ret *returnSignal

	switch {
	case err == nil, errors.Is(err, errStop), errors.Is(err, errBreak):
		return &Result{Output: st.out.String()}, nil
	case errors.As(err, &ret):
		return &Result{Output: st.out.String(), Returned: true, ReturnValue: ret.value}, nil
	default:
		if errors.Is(err, ErrMemoryLimit) || errors.Is(err, ErrOutputLimit) {
			// A render that ran out of budget leaves up to a budget's worth of
			// garbage. Collect it now, before concurrent abusive renders pile
			// it up faster than the pacer would.
			runtime.GC()
		}

		return nil, err
	}
}

// Control-flow signals, carried as errors through the evaluator.
var (
	errStop  = errors.New("vtl: #stop")
	errBreak = errors.New("vtl: #break")
)

type returnSignal struct{ value any }

func (*returnSignal) Error() string { return "vtl: #return" }

type state struct {
	ctx      context.Context
	vars     map[string]any
	out      boundedWriter
	steps    int
	maxSteps int
	mem      *budget
}

func newState(ctx context.Context, vars map[string]any, maxSteps int, mem *budget) *state {
	return &state{ctx: ctx, vars: vars, maxSteps: maxSteps, mem: mem, out: boundedWriter{limit: MaxOutputBytes}}
}

func (s *state) step() error {
	s.steps++
	if s.steps > s.maxSteps {
		return ErrStepBudget
	}

	if s.steps%ctxCheckEvery == 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
	}

	return nil
}

func (s *state) run(body []node) error {
	for _, n := range body {
		if err := s.step(); err != nil {
			return err
		}

		if err := s.exec(n); err != nil {
			return err
		}
	}

	return nil
}

func (s *state) exec(n node) error {
	switch t := n.(type) {
	case *textNode:
		return s.execText(t)
	case *refNode:
		return s.execRef(t)
	case *setNode:
		return s.execSet(t)
	case *ifNode:
		return s.execIf(t)
	case *foreachNode:
		return s.execForeach(t)
	case *breakNode:
		return errBreak
	case *stopNode:
		return errStop
	case *returnNode:
		return s.execReturn(t)
	}

	return nil
}

func (s *state) execText(n *textNode) error {
	for _, part := range n.parts {
		if err := s.out.WriteString(part); err != nil {
			return err
		}
	}

	return nil
}

func (s *state) execRef(n *refNode) error {
	v, err := s.evalRef(n.ref)
	if err != nil {
		return err
	}

	text, err := format(v, s.out.limit-s.out.Len())
	if err != nil {
		return err
	}

	return s.out.WriteString(text)
}

func (s *state) execSet(n *setNode) error {
	v, err := s.eval(n.value)
	if err != nil {
		return err
	}

	if len(n.target.chain) == 0 {
		s.vars[n.target.name] = v

		return nil
	}

	parent, err := s.resolveChain(n.target.name, n.target.chain[:len(n.target.chain)-1])
	if err != nil {
		return err
	}

	last := n.target.chain[len(n.target.chain)-1]

	switch last.kind {
	case accProperty:
		if m, ok := parent.(*Map); ok {
			return s.put(m, last.name, v)
		}
	case accIndex:
		idx, err := s.eval(last.index)
		if err != nil {
			return err
		}

		return s.setIndex(parent, idx, v)
	case accMethod:
		return errorf("cannot #set a method call")
	}

	return nil
}

// put stores v in m, charging a new entry against the memory budget.
func (s *state) put(m *Map, key string, v any) error {
	if _, exists := m.Get(key); !exists {
		if err := s.mem.charge(slotBytes + len(key)); err != nil {
			return err
		}
	}

	m.Put(key, v)

	return nil
}

func (s *state) setIndex(target, idx, v any) error {
	switch t := target.(type) {
	case *Map:
		key, err := s.key(idx)
		if err != nil {
			return err
		}

		return s.put(t, key, v)
	case *List:
		if i, ok := toInt(idx); ok && i >= 0 && i < len(t.Items) {
			t.Items[i] = v
		}
	}

	return nil
}

// key renders a value used as a map key.
func (s *state) key(v any) (string, error) {
	if k, ok := v.(string); ok {
		return k, nil
	}

	k, err := format(v, MaxOutputBytes)
	if err != nil {
		return "", err
	}

	return k, s.mem.charge(len(k))
}

func (s *state) execIf(n *ifNode) error {
	for _, b := range n.branches {
		v, err := s.eval(b.cond)
		if err != nil {
			return err
		}

		if truthy(v) {
			return s.run(b.body)
		}
	}

	return s.run(n.elseBody)
}

func (s *state) execForeach(n *foreachNode) error {
	src, err := s.eval(n.iter)
	if err != nil {
		return err
	}

	items := iterItems(src, MaxForeachIterations)

	prevVar, hadVar := s.vars[n.varName]
	prevLoop, hadLoop := s.vars["foreach"]
	prevCount, hadCount := s.vars["velocityCount"]

	defer func() {
		restoreVar(s.vars, n.varName, prevVar, hadVar)
		restoreVar(s.vars, "foreach", prevLoop, hadLoop)
		restoreVar(s.vars, "velocityCount", prevCount, hadCount)
	}()

	for i, it := range items {
		s.vars[n.varName] = it
		s.vars["foreach"] = loopInfo(i, len(items))
		s.vars["velocityCount"] = int64(i + 1)

		err := s.run(n.body)
		if errors.Is(err, errBreak) {
			return nil
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// loopInfo is the $foreach object for iteration i of n.
func loopInfo(i, n int) *Map {
	m := NewMap()
	m.Put("index", int64(i))
	m.Put("count", int64(i+1))
	m.Put("hasNext", i < n-1)
	m.Put("first", i == 0)
	m.Put("last", i == n-1)

	return m
}

func restoreVar(vars map[string]any, name string, prev any, had bool) {
	if had {
		vars[name] = prev
	} else {
		delete(vars, name)
	}
}

// iterItems returns up to limit of the items #foreach walks: a list's items,
// a map's values, or nothing.
func iterItems(v any, limit int) []any {
	switch t := v.(type) {
	case *List:
		return append([]any(nil), t.Items[:min(limit, len(t.Items))]...)
	case *Map:
		keys := t.keys[:min(limit, len(t.keys))]
		out := make([]any, 0, len(keys))

		for _, k := range keys {
			out = append(out, t.vals[k])
		}

		return out
	default:
		return nil
	}
}

func (s *state) execReturn(n *returnNode) error {
	if n.value == nil {
		return &returnSignal{}
	}

	v, err := s.eval(n.value)
	if err != nil {
		return err
	}

	return &returnSignal{value: v}
}

func (s *state) eval(e expr) (any, error) {
	if err := s.step(); err != nil {
		return nil, err
	}

	switch t := e.(type) {
	case *literal:
		return t.value, nil
	case *interpolated:
		return s.evalInterpolated(t)
	case *refExpr:
		return s.evalRef(t)
	case *unaryExpr:
		return s.evalUnary(t)
	case *binaryExpr:
		return s.evalBinary(t)
	}

	return s.evalCollection(e)
}

func (s *state) evalCollection(e expr) (any, error) {
	switch t := e.(type) {
	case *listExpr:
		return s.evalList(t)
	case *rangeExpr:
		return s.evalRange(t)
	case *mapExpr:
		return s.evalMap(t)
	}

	return nil, errorf("unknown expression %T", e)
}

// evalInterpolated renders a double-quoted string as a template sharing the
// caller's variables, step budget and memory budget.
func (s *state) evalInterpolated(t *interpolated) (any, error) {
	sub := newState(s.ctx, s.vars, s.maxSteps, s.mem)
	sub.steps = s.steps

	err := sub.run(t.body)
	s.steps = sub.steps

	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}

	out := sub.out.String()

	return out, s.mem.charge(len(out))
}

func (s *state) evalList(t *listExpr) (any, error) {
	if err := s.mem.charge(len(t.items) * slotBytes); err != nil {
		return nil, err
	}

	l := NewList()

	for _, it := range t.items {
		v, err := s.eval(it)
		if err != nil {
			return nil, err
		}

		l.Items = append(l.Items, v)
	}

	return l, nil
}

func (s *state) evalRange(t *rangeExpr) (any, error) {
	from, err := s.eval(t.from)
	if err != nil {
		return nil, err
	}

	to, err := s.eval(t.to)
	if err != nil {
		return nil, err
	}

	a, okA := toInt(from)
	b, okB := toInt(to)

	if !okA || !okB {
		return nil, nil
	}

	l := NewList()

	step := 1
	if b < a {
		step = -1
	}

	for i := a; ; i += step {
		l.Items = append(l.Items, int64(i))

		if i == b || len(l.Items) > MaxForeachIterations {
			break
		}
	}

	return l, s.mem.charge(len(l.Items) * slotBytes)
}

func (s *state) evalMap(t *mapExpr) (any, error) {
	m := NewMap()

	for i := range t.keys {
		k, err := s.eval(t.keys[i])
		if err != nil {
			return nil, err
		}

		v, err := s.eval(t.vals[i])
		if err != nil {
			return nil, err
		}

		key, err := s.key(k)
		if err != nil {
			return nil, err
		}

		if err := s.put(m, key, v); err != nil {
			return nil, err
		}
	}

	return m, nil
}

func (s *state) evalUnary(t *unaryExpr) (any, error) {
	v, err := s.eval(t.x)
	if err != nil {
		return nil, err
	}

	if t.op == opNot {
		return !truthy(v), nil
	}

	switch n := v.(type) {
	case int64:
		return -n, nil
	case float64:
		return -n, nil
	default:
		return nil, nil
	}
}

func (s *state) evalBinary(t *binaryExpr) (any, error) {
	l, err := s.eval(t.l)
	if err != nil {
		return nil, err
	}

	// && and || short-circuit.
	if t.op == opAnd || t.op == opOr {
		if truthy(l) == (t.op == opOr) {
			return t.op == opOr, nil
		}

		r, rerr := s.eval(t.r)

		return truthy(r), rerr
	}

	r, err := s.eval(t.r)
	if err != nil {
		return nil, err
	}

	switch t.op {
	case opEq:
		return equal(l, r), nil
	case opNe:
		return !equal(l, r), nil
	case opLt, opGt, opLe, opGe:
		return compare(t.op, l, r), nil
	default:
		return s.arith(t.op, l, r)
	}
}

// evalRef resolves a reference and its chain. A missing value is nil.
func (s *state) evalRef(r *refExpr) (any, error) {
	return s.resolveChain(r.name, r.chain)
}

func (s *state) resolveChain(name string, chain []accessor) (any, error) {
	cur, ok := s.vars[name]
	if !ok {
		return nil, nil
	}

	for _, a := range chain {
		if cur == nil {
			return nil, nil
		}

		var err error
		if cur, err = s.access(cur, a); err != nil {
			return nil, err
		}
	}

	return cur, nil
}

// access applies one accessor step to cur.
func (s *state) access(cur any, a accessor) (any, error) {
	switch a.kind {
	case accProperty:
		return property(cur, a.name), nil
	case accIndex:
		idx, err := s.eval(a.index)
		if err != nil {
			return nil, err
		}

		return index(cur, idx), nil
	case accMethod:
		args := make([]any, len(a.args))

		for i, ae := range a.args {
			v, err := s.eval(ae)
			if err != nil {
				return nil, err
			}

			args[i] = v
		}

		r, err := callMethod(s.mem, cur, a.name, args)
		if err != nil {
			return nil, err
		}

		// A single method call can be slow on a large string, so the deadline
		// is checked after every one.
		return r, s.ctx.Err()
	}

	return nil, nil
}

func property(v any, name string) any {
	switch t := v.(type) {
	case *Map:
		got, _ := t.Get(name)

		return got
	case Object:
		got, _ := t.Get(name)

		return got
	}

	// Bean-style getters: $list.empty, $str.empty.
	if name == "empty" {
		if r, err := callMethod(&budget{}, v, mIsEmpty, nil); err == nil {
			return r
		}
	}

	return nil
}

func index(v, idx any) any {
	switch t := v.(type) {
	case *List:
		i, ok := toInt(idx)
		if !ok || i < 0 || i >= len(t.Items) {
			return nil
		}

		return t.Items[i]
	case *Map:
		got, _ := t.Get(Stringify(idx))

		return got
	case Object:
		got, _ := t.Get(Stringify(idx))

		return got
	}

	return nil
}

// truthy is Velocity's #if test: null and false are false, anything else true.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	default:
		return true
	}
}

// toInt narrows a template number to an int index, offset or range bound. Like
// the Java int those take in Velocity, only the int32 range is accepted; a value
// outside it, NaN or an infinity is not an int, so the caller treats it like any
// other non-integer argument. A fraction truncates toward zero.
func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int64:
		if t < math.MinInt32 || t > math.MaxInt32 {
			return 0, false
		}

		return int(t), true
	case float64:
		if math.IsNaN(t) || t < math.MinInt32 || t > math.MaxInt32 {
			return 0, false
		}

		return int(t), true
	default:
		return 0, false
	}
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	default:
		return 0, false
	}
}

func equal(l, r any) bool {
	if l == nil || r == nil {
		return l == nil && r == nil
	}

	if a, ok := toFloat(l); ok {
		if b, ok := toFloat(r); ok {
			return a == b
		}
	}

	if a, ok := l.(bool); ok {
		b, ok := r.(bool)

		return ok && a == b
	}

	return equalForms(l, r)
}

// equalForms compares values of different types, and collections, by their
// string form. Forms too large or deep to render are unequal.
func equalForms(l, r any) bool {
	if sameCollection(l, r) {
		return true
	}

	ls, lerr := format(l, MaxOutputBytes)
	rs, rerr := format(r, MaxOutputBytes)

	return lerr == nil && rerr == nil && ls == rs
}

// sameCollection reports whether l and r are the same list or map.
func sameCollection(l, r any) bool {
	switch lt := l.(type) {
	case *List:
		rt, ok := r.(*List)

		return ok && lt == rt
	case *Map:
		rt, ok := r.(*Map)

		return ok && lt == rt
	default:
		return false
	}
}

func compare(op string, l, r any) bool {
	var c int

	a, okA := toFloat(l)
	b, okB := toFloat(r)

	switch {
	case okA && okB:
		c = cmpFloat(a, b)
	default:
		ls, isLS := l.(string)
		rs, isRS := r.(string)

		if !isLS || !isRS {
			return false
		}

		c = strings.Compare(ls, rs)
	}

	switch op {
	case opLt:
		return c < 0
	case opGt:
		return c > 0
	case opLe:
		return c <= 0
	default:
		return c >= 0
	}
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// arith applies + - * / %. A string operand of + concatenates; integer
// operands keep integer arithmetic; a division by zero is null.
func (s *state) arith(op string, l, r any) (any, error) {
	if op == opAdd {
		_, ls := l.(string)
		_, rs := r.(string)

		if ls || rs {
			return s.concat(l, r)
		}
	}

	li, lInt := l.(int64)
	ri, rInt := r.(int64)

	if lInt && rInt {
		return intArith(op, li, ri), nil
	}

	a, okA := toFloat(l)
	b, okB := toFloat(r)

	if !okA || !okB {
		return nil, nil
	}

	return floatArith(op, a, b), nil
}

// concat joins two values as strings within the output and memory limits.
func (s *state) concat(l, r any) (any, error) {
	ls, err := format(l, MaxOutputBytes)
	if err != nil {
		return nil, err
	}

	rs, err := format(r, MaxOutputBytes-len(ls))
	if err != nil {
		return nil, err
	}

	if err := s.mem.charge(len(ls) + len(rs)); err != nil {
		return nil, err
	}

	return ls + rs, nil
}

func floatArith(op string, a, b float64) any {
	switch op {
	case opAdd:
		return a + b
	case opSub:
		return a - b
	case opMul:
		return a * b
	}

	if b == 0 {
		return nil
	}

	if op == opDiv {
		return a / b
	}

	return math.Mod(a, b)
}

func intArith(op string, a, b int64) any {
	switch op {
	case opAdd:
		return a + b
	case opSub:
		return a - b
	case opMul:
		return a * b
	}

	if b == 0 {
		return nil
	}

	if op == opDiv {
		return a / b
	}

	return a % b
}
