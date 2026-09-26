package cloudformation

// Condition function names.
const (
	fnEquals     = "Fn::Equals"
	fnAnd        = "Fn::And"
	fnOr         = "Fn::Or"
	fnNot        = "Fn::Not"
	keyCondition = "Condition"
)

// Fn::And and Fn::Or take between 2 and 10 conditions.
const (
	minBoolArgs = 2
	maxBoolArgs = 10
)

// Prepare binds the resolver to a template and evaluates its conditions with
// the resolver's parameter values. It returns a copy of the template in which
// resources and outputs whose condition is false are gone, each Fn::If is
// replaced by the branch it selects, and each Fn::FindInMap by its value. It
// then checks that nothing left refers to a missing resource. Call it before
// any resource is created, so errors are returned to the caller.
func (r *Resolver) Prepare(t *Template) (*Template, error) {
	r.bind(t)

	if err := r.evalConditions(t.Conditions); err != nil {
		return nil, err
	}

	out := *t

	var err error

	if out.Resources, err = r.pruneResources(t.Resources); err != nil {
		return nil, err
	}

	if out.Outputs, err = r.pruneOutputs(t.Outputs); err != nil {
		return nil, err
	}

	if refErr := checkReferences(&out); refErr != nil {
		return nil, refErr
	}

	return &out, nil
}

// bind reads what the resolver needs from the template: its mappings and
// which parameters hold lists.
func (r *Resolver) bind(t *Template) {
	r.mappings = t.Mappings
	r.conditions = map[string]bool{}
	r.listParams = map[string]bool{}

	for name := range t.Parameters {
		if IsListType(t.Parameters[name].Type) {
			r.listParams[name] = true
		}
	}
}

// included reports whether a resource or output with this Condition exists.
func (r *Resolver) included(cond string) bool {
	return cond == "" || r.conditions[cond]
}

func (r *Resolver) pruneResources(in map[string]ResourceDef) (map[string]ResourceDef, error) {
	out := make(map[string]ResourceDef, len(in))

	for _, id := range sortedKeys(in) {
		res := in[id]
		if !r.included(res.Condition) {
			continue
		}

		props, err := r.prune(res.Properties)
		if err != nil {
			return nil, err
		}

		res.Properties, _ = props.(map[string]any)
		out[id] = res
	}

	return out, nil
}

func (r *Resolver) pruneOutputs(in map[string]OutputDef) (map[string]OutputDef, error) {
	out := make(map[string]OutputDef, len(in))

	for _, name := range sortedKeys(in) {
		od := in[name]
		if !r.included(od.Condition) {
			continue
		}

		var err error

		if od.Value, err = r.prune(od.Value); err != nil {
			return nil, err
		}

		if od.Export != nil {
			exportName, pErr := r.prune(od.Export.Name)
			if pErr != nil {
				return nil, pErr
			}

			od.Export = &ExportDef{Name: exportName}
		}

		out[name] = od
	}

	return out, nil
}

// prune replaces each Fn::If with its selected branch and each Fn::FindInMap
// with its value. Everything else is copied.
func (r *Resolver) prune(node any) (any, error) {
	switch v := node.(type) {
	case map[string]any:
		if fn, arg, ok := intrinsic(v); ok && (fn == fnIf || fn == fnFindInMap) {
			return r.pruneIntrinsic(fn, arg)
		}

		out := make(map[string]any, len(v))

		for k, e := range v {
			pe, err := r.prune(e)
			if err != nil {
				return nil, err
			}

			out[k] = pe
		}

		return out, nil
	case []any:
		out := make([]any, 0, len(v))

		for _, e := range v {
			pe, err := r.prune(e)
			if err != nil {
				return nil, err
			}

			out = append(out, pe)
		}

		return out, nil
	default:
		return node, nil
	}
}

func (r *Resolver) pruneIntrinsic(fn string, arg any) (any, error) {
	if fn == fnFindInMap {
		return r.findInMap(arg)
	}

	branch, err := r.ifBranch(arg)
	if err != nil {
		return nil, err
	}

	return r.prune(branch)
}

// evalConditions evaluates every declared condition, as CloudFormation does.
func (r *Resolver) evalConditions(defs map[string]any) error {
	ev := &condEval{r: r, defs: defs, visiting: map[string]bool{}}

	for _, name := range sortedKeys(defs) {
		if _, err := ev.named(name, keyCondition); err != nil {
			return err
		}
	}

	return nil
}

// condEval evaluates conditions once each, storing results on the resolver.
type condEval struct {
	r        *Resolver
	defs     map[string]any
	visiting map[string]bool
}

func (c *condEval) named(name, from string) (bool, error) {
	if v, ok := c.r.conditions[name]; ok {
		return v, nil
	}

	def, ok := c.defs[name]
	if !ok {
		return false, templateErr("unresolved condition dependency %s in %s", name, from)
	}

	if c.visiting[name] {
		return false, formatErr("Circular dependency between conditions: [%s]", name)
	}

	c.visiting[name] = true
	v, err := c.eval(def)
	delete(c.visiting, name)

	if err != nil {
		return false, err
	}

	c.r.conditions[name] = v

	return v, nil
}

func (c *condEval) eval(node any) (bool, error) {
	if m, ok := node.(map[string]any); ok && len(m) == 1 {
		for fn, arg := range m {
			switch fn {
			case fnEquals:
				return c.equals(arg)
			case fnAnd, fnOr:
				return c.andOr(fn, arg)
			case fnNot:
				return c.not(arg)
			case keyCondition:
				return c.named(scalarString(arg), fn)
			}
		}
	}

	return false, formatErr("Conditions can only be boolean operations on parameters and other conditions")
}

// equals compares two values as strings, after resolving intrinsics.
func (c *condEval) equals(arg any) (bool, error) {
	const equalsArgs = 2

	l, ok := argList(arg, equalsArgs)
	if !ok {
		return false, templateErr("Fn::Equals requires a list argument with two elements")
	}

	a, err := c.r.Resolve(l[0])
	if err != nil {
		return false, err
	}

	b, err := c.r.Resolve(l[1])
	if err != nil {
		return false, err
	}

	return scalarString(a) == scalarString(b), nil
}

func (c *condEval) andOr(fn string, arg any) (bool, error) {
	l, ok := arg.([]any)
	if !ok || len(l) < minBoolArgs || len(l) > maxBoolArgs {
		return false, templateErr("every %s object requires a list of at least %d and at most %d boolean parameters.",
			fn, minBoolArgs, maxBoolArgs)
	}

	isAnd := fn == fnAnd
	result := isAnd

	for _, e := range l {
		v, err := c.eval(e)
		if err != nil {
			return false, err
		}

		if isAnd {
			result = result && v
		} else {
			result = result || v
		}
	}

	return result, nil
}

func (c *condEval) not(arg any) (bool, error) {
	l, ok := argList(arg, 1)
	if !ok {
		return false, templateErr("every Fn::Not object requires a list with exactly 1 boolean parameter.")
	}

	v, err := c.eval(l[0])

	return !v, err
}
