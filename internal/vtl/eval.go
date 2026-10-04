package vtl

import (
	"context"
	"errors"
	"math"
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
	ctxCheckEvery = 1024
)

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
// modified by #set. Rendering stops with ctx's error when ctx is done.
func (t *Template) Render(ctx context.Context, vars map[string]any, opts RenderOptions) (*Result, error) {
	if vars == nil {
		vars = map[string]any{}
	}

	st := &state{ctx: ctx, vars: vars, maxSteps: opts.MaxSteps}
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
	out      strings.Builder
	steps    int
	maxSteps int
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
		s.out.WriteString(t.text)
	case *refNode:
		v, err := s.evalRef(t.ref)
		if err != nil {
			return err
		}

		s.out.WriteString(Stringify(v))
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
			m.Put(last.name, v)
		}
	case accIndex:
		idx, err := s.eval(last.index)
		if err != nil {
			return err
		}

		setIndex(parent, idx, v)
	case accMethod:
		return errorf("cannot #set a method call")
	}

	return nil
}

func setIndex(target, idx, v any) {
	switch t := target.(type) {
	case *Map:
		t.Put(Stringify(idx), v)
	case *List:
		if i, ok := toInt(idx); ok && i >= 0 && i < len(t.Items) {
			t.Items[i] = v
		}
	}
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

	items := iterItems(src)
	if len(items) > MaxForeachIterations {
		items = items[:MaxForeachIterations]
	}

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
		s.vars["foreach"] = MapOf(
			"index", int64(i), "count", int64(i+1), "hasNext", i < len(items)-1,
			"first", i == 0, "last", i == len(items)-1,
		)
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

func restoreVar(vars map[string]any, name string, prev any, had bool) {
	if had {
		vars[name] = prev
	} else {
		delete(vars, name)
	}
}

// iterItems returns what #foreach walks: a list's items, a map's values, or
// nothing.
func iterItems(v any) []any {
	switch t := v.(type) {
	case *List:
		return append([]any(nil), t.Items...)
	case *Map:
		out := make([]any, 0, t.Len())
		for _, k := range t.keys {
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
// caller's variables and step budget.
func (s *state) evalInterpolated(t *interpolated) (any, error) {
	sub := &state{ctx: s.ctx, vars: s.vars, steps: s.steps, maxSteps: s.maxSteps}
	err := sub.run(t.body)
	s.steps = sub.steps

	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}

	return sub.out.String(), nil
}

func (s *state) evalList(t *listExpr) (any, error) {
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

	return l, nil
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

		m.Put(Stringify(k), v)
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
		return arith(t.op, l, r), nil
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

		return callMethod(cur, a.name, args)
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
		if r, err := callMethod(v, mIsEmpty, nil); err == nil {
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

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case int64:
		return int(t), true
	case float64:
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

	// Different types compare by their string form, as Velocity does.
	return Stringify(l) == Stringify(r)
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
func arith(op string, l, r any) any {
	if op == opAdd {
		_, ls := l.(string)
		_, rs := r.(string)

		if ls || rs {
			return Stringify(l) + Stringify(r)
		}
	}

	li, lInt := l.(int64)
	ri, rInt := r.(int64)

	if lInt && rInt {
		return intArith(op, li, ri)
	}

	a, okA := toFloat(l)
	b, okB := toFloat(r)

	if !okA || !okB {
		return nil
	}

	return floatArith(op, a, b)
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
